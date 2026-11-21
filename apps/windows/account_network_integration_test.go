//go:build ddnetworkintegration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tailscale.com/derp/derpserver"
	"tailscale.com/envknob"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

// TestLocalAccountNetwork uses real userspace WireGuard nodes against an
// in-process test control plane and loopback DERP. It never logs into a real
// account or invokes the system Tailscale service. This intentionally does not
// claim cloud deletion: the pinned testcontrol ignores registration Expiry and
// Ephemeral, and therefore retains its node entries after successful Logout.
func TestLocalAccountNetwork(t *testing.T) {
	if os.Getenv("DD_TEST_NETWORK") != "1" {
		t.Skip("set DD_TEST_NETWORK=1 for the isolated local tsnet integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	t.Cleanup(cancel)
	envknob.SetenvForTest(t, "TS_DEBUG_NEVER_DIRECT_UDP", "true")
	envknob.SetenvForTest(t, "TS_DISABLE_PORTMAPPER", "true")
	envknob.SetenvForTest(t, "TS_NO_LOGS_NO_SUPPORT", "true")
	// This changes only the test process's socket routing helper; no OS network
	// interface, route, registry, service, or user tailnet configuration is used.
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	control := &testcontrol.Server{
		DERPMap:          localAccountDERP(t),
		AllNodesSameUser: true,
		AllOnline:        true,
		Logf:             logger.Discard,
	}
	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)

	a := startLocalAccountNode(t, ctx, control.HTTPTestServer.URL, "DD local alpha")
	b := startLocalAccountNode(t, ctx, control.HTTPTestServer.URL, "DD local beta")
	awaitLocalAccountCondition(t, ctx, "both authenticated peer maps", func() bool {
		as, bs := a.network.Status(ctx), b.network.Status(ctx)
		return as.Running && bs.Running && as.Account.ID == "123" && bs.Account.ID == "123" && len(as.Peers) == 1 && len(bs.Peers) == 1
	})
	as, bs := a.network.Status(ctx), b.network.Status(ctx)
	if as.SelfID == "" || bs.SelfID == "" || as.SelfID == bs.SelfID {
		t.Fatalf("invalid independent node identities: %q / %q", as.SelfID, bs.SelfID)
	}
	if !as.Temporary || !bs.Temporary || as.Receiving || bs.Receiving {
		t.Fatal("test nodes must be ephemeral with desktop receiving disabled")
	}
	for _, pair := range []struct {
		from *App
		to   NetworkStatus
		name string
	}{{a, bs, "DD local beta"}, {b, as, "DD local alpha"}} {
		awaitLocalAccountCondition(t, ctx, "real account discovery for "+pair.name, func() bool {
			devices := localAccountDevices(t, ctx, pair.from.account)
			if len(devices) != 1 {
				return false
			}
			d := devices[0]
			return d.ID == pair.to.SelfID && d.IP == pair.to.IP && d.Name == pair.name && d.Online && !d.Ready && d.Platform == "windows" && d.Version == Version
		})
	}
	t.Log("PASS: two real ephemeral mem.Store tsnet nodes discovered each other through production networkAccountHandler, fresh LocalAPI WhoIs and AccountManager.Devices")
	if got := control.NumNodes(); got != 2 {
		t.Fatalf("expected two isolated control nodes, got %d", got)
	}
	b.network.mu.RLock()
	run := b.network.run
	b.network.mu.RUnlock()
	if err := b.network.Logout(ctx); err != nil {
		t.Fatalf("production Logout: %v", err)
	}
	stopped := b.network.Status(ctx)
	if stopped.Running || stopped.Receiving || stopped.Account.ID != "" || stopped.SelfID != "" || len(stopped.Peers) != 0 {
		t.Fatalf("logout retained active DD account state: %+v", stopped)
	}
	if got := localAccountDevices(t, ctx, b.account); len(got) != 0 {
		t.Fatalf("logged-out device list retained %d entries", len(got))
	}
	if _, err := b.network.DialContext(ctx, "tcp", as.IP+":48201"); err == nil {
		t.Fatal("logged-out node can still dial")
	}
	if _, err := os.Stat(run.temporaryDir); !os.IsNotExist(err) {
		t.Fatalf("temporary node directory not removed: %v", err)
	}
	if got := control.NumNodes(); got != 2 {
		t.Fatalf("pinned testcontrol retention changed: got %d nodes; review its logout model", got)
	}
	t.Log("PASS: real LocalAPI Logout completed; DD listeners, identity, devices and temporary directory were cleared")
	t.Log("LIMITATION: pinned testcontrol retains both nodes because it does not implement ephemeral logout deletion. This test does not verify Tailscale cloud deletion or peer removal from a cloud map.")
}

func localAccountDERP(t *testing.T) *tailcfg.DERPMap {
	t.Helper()
	d := derpserver.New(key.NewNode(), logger.Discard)
	srv := httptest.NewUnstartedServer(derpserver.Handler(d))
	srv.Config.ErrorLog = logger.StdLogger(logger.Discard)
	srv.StartTLS()
	t.Cleanup(func() {
		_ = d.Close()
		srv.CloseClientConnections()
		srv.Close()
	})
	return &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{
		1: {RegionID: 1, RegionCode: "dd-local", Nodes: []*tailcfg.DERPNode{{
			Name: "dd-local-1", RegionID: 1, HostName: "127.0.0.1", IPv4: "127.0.0.1", IPv6: "none",
			STUNPort: -1, DERPPort: srv.Listener.Addr().(*net.TCPAddr).Port, InsecureForTests: true,
		}}},
	}}
}

func startLocalAccountNode(t *testing.T, ctx context.Context, controlURL, name string) *App {
	t.Helper()
	dir := t.TempDir()
	m := NewNetworkManager(filepath.Join(dir, "unused-persistent-state"), nil)
	srv, temporaryDir, err := newNetworkServer(m.stateDir, name, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Store.(*mem.Store); !ok || !srv.Ephemeral {
		t.Fatal("production server factory did not create ephemeral memory state")
	}
	srv.ControlURL = controlURL
	// Explicit local test key prevents reading any TS_AUTHKEY in the caller's
	// environment. The in-memory testcontrol has no real account credentials.
	srv.AuthKey = "dd-local-integration-test-key"
	runCtx, runCancel := context.WithCancel(ctx)
	r := &networkRun{
		ctx: runCtx, cancel: runCancel, ready: make(chan struct{}), done: make(chan struct{}),
		server: srv, hostname: name, temporary: true, temporaryDir: temporaryDir,
		receive: make(chan networkReceiveRequest), receiveDone: make(chan struct{}),
	}
	m.run = r
	m.status = NetworkStatus{State: "Starting", Temporary: true, Peers: []NetworkPeer{}}
	a := &App{dir: dir, nativeDir: filepath.Join(dir, "native"), network: m}
	a.settings.DeviceName = name
	a.host = NewHost(a.nativeDir, &Secrets{}, func(string, ...any) {})
	a.account = newAccountManager(a)
	m.SetAccountHandler(a.account)
	go m.runNetwork(r)
	t.Cleanup(func() {
		if err := m.Stop(); err != nil {
			t.Errorf("stop local test node: %v", err)
		}
	})
	return a
}

func awaitLocalAccountCondition(t *testing.T, ctx context.Context, description string, fn func() bool) {
	t.Helper()
	for {
		if fn() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", description, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func localAccountDevices(t *testing.T, ctx context.Context, manager *AccountManager) []AccountDevice {
	t.Helper()
	result, err := manager.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Devices []AccountDevice `json:"devices"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	return response.Devices
}
