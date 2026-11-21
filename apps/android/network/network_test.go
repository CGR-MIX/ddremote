package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/netmon"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

type testAccount struct {
	state     *ipnstate.Status
	loggedOut atomic.Bool
}

func (a *testAccount) Status(ctx context.Context) (*ipnstate.Status, error) {
	return a.state, ctx.Err()
}
func (a *testAccount) StartLoginInteractive(ctx context.Context) error { return ctx.Err() }
func (a *testAccount) Logout(ctx context.Context) error                { a.loggedOut.Store(true); return ctx.Err() }

func testManager(t *testing.T, handler http.HandlerFunc) (*manager, *testAccount, *atomic.Bool) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	account := &testAccount{state: &ipnstate.Status{
		BackendState: "Running", Self: &ipnstate.PeerStatus{UserID: 17},
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{key.NewNode().Public(): {ID: "n-owner-device", UserID: 17, Online: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.70.1.2")}}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	closed := new(atomic.Bool)
	m := &manager{local: account, sessionCtx: ctx, sessionCancel: cancel, sessionToken: 1,
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "100.70.1.2:48201" {
				t.Errorf("unexpected account destination: %s %s", network, address)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "http://"))
		}, closeServer: func() error { closed.Store(true); return nil },
	}
	t.Cleanup(func() { m.logout(time.Second) })
	return m, account, closed
}

func awaitSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(label + " timed out")
	}
}

func TestLogoutCancelsProductionPairRequest(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	m, account, closed := testManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pair" {
			t.Errorf("wrong pairing path")
		}
		// Drain the body so net/http monitors the closed connection.
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	})
	result := make(chan error, 1)
	go func() {
		_, err := m.call("request", `{"sessionToken":1,"ip":"100.70.1.2","path":"/v1/pair","body":"{}"}`)
		result <- err
	}()
	awaitSignal(t, entered, "pair request")
	start := time.Now()
	if _, err := m.call("logout", `{"sessionToken":2}`); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("logout waited behind pair request")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pair returned %v, expected cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pair did not return")
	}
	awaitSignal(t, cancelled, "server cancellation")
	if !account.loggedOut.Load() || !closed.Load() {
		t.Fatal("account logout or network close was skipped")
	}
	if _, err := m.call("start", `{"sessionToken":1}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("late old start was accepted: %v", err)
	}
}

func TestLogoutCancelsDiscoveryWithoutStaleResults(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	m, _, _ := testManager(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	})
	result := make(chan error, 1)
	go func() {
		value, err := m.call("discover", `{"sessionToken":1}`)
		if value != nil {
			t.Error("cancelled discovery returned devices")
		}
		result <- err
	}()
	awaitSignal(t, entered, "discovery")
	if _, err := m.call("logout", `{"sessionToken":2}`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("discovery returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery not cancelled")
	}
	awaitSignal(t, cancelled, "discovery request cancellation")
}

func TestLogoutBoundsSlowCloseAndBlocksRestart(t *testing.T) {
	m, _, _ := testManager(t, func(w http.ResponseWriter, r *http.Request) {})
	entered, release := make(chan struct{}), make(chan struct{})
	m.closeServer = func() error { close(entered); <-release; return nil }
	start := time.Now()
	if _, err := m.logout(25 * time.Millisecond); err == nil {
		t.Fatal("slow cleanup reported complete")
	}
	if time.Since(start) > time.Second {
		t.Fatal("public logout exceeded bounded wait")
	}
	awaitSignal(t, entered, "network close")
	m.cancelMu.Lock()
	done := m.closeDone
	m.cancelMu.Unlock()
	if _, err := m.call("start", `{"sessionToken":3}`); err == nil || !strings.Contains(err.Error(), "正在退出") {
		t.Fatalf("restart during cleanup: %v", err)
	}
	close(release)
	awaitSignal(t, done, "cleanup complete")
	if _, err := m.call("start", `{"sessionToken":1}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("old generation survived cleanup: %v", err)
	}
}

func TestAccountDeviceStableIDBinding(t *testing.T) {
	var advertised atomic.Value
	advertised.Store("n-wrong-device")
	var probes atomic.Int32
	m, _, _ := testManager(t, func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		json.NewEncoder(w).Encode(device{Protocol: 1, ID: advertised.Load().(string), Name: "Desktop", Platform: "windows", Ready: true})
	})
	value, err := m.call("discover", `{"sessionToken":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.(*status).Devices) != 0 {
		t.Fatal("discovery accepted ID different from authenticated Peer StableID")
	}
	before := probes.Load()
	if _, err = m.call("prepare", `{"sessionToken":1,"ip":"100.70.1.2","id":"n-wrong-device"}`); err == nil {
		t.Fatal("prepare accepted forged selection ID")
	}
	if probes.Load() != before {
		t.Fatal("invalid selection reached device endpoint")
	}
	if _, err = m.call("prepare", `{"sessionToken":1,"ip":"100.70.1.2","id":"n-owner-device"}`); err == nil {
		t.Fatal("prepare accepted forged advertised ID")
	}
	if m.bridge != nil {
		t.Fatal("invalid identity created a bridge")
	}
	advertised.Store("n-owner-device")
	value, err = m.call("discover", `{"sessionToken":1}`)
	if err != nil {
		t.Fatal(err)
	}
	devices := value.(*status).Devices
	if len(devices) != 1 || devices[0].ID != "n-owner-device" || devices[0].NodeID != devices[0].ID {
		t.Fatalf("matching authenticated device not discovered: %+v", devices)
	}
}

func TestOldGenerationCannotUseNewSession(t *testing.T) {
	m, _, _ := testManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("stale request reached server") })
	m.cancelMu.Lock()
	m.minimumToken = 2
	m.sessionToken = 3
	m.cancelMu.Unlock()
	if _, err := m.call("request", `{"sessionToken":1,"ip":"100.70.1.2","path":"/v1/pair","body":"{}"}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale work crossed into new session: %v", err)
	}
	if _, err := m.call("status", `{"sessionToken":3}`); err != nil {
		t.Fatal("new generation rejected", err)
	}
}

func TestAccountOwnership(t *testing.T) {
	peerKey := key.NewNode().Public()
	peer := &ipnstate.PeerStatus{UserID: 17, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.70.1.2")}}
	st := &ipnstate.Status{BackendState: "Running", Self: &ipnstate.PeerStatus{UserID: 17}, Peer: map[key.NodePublic]*ipnstate.PeerStatus{peerKey: peer}}
	if !ownedIP(st, "100.70.1.2") {
		t.Fatal("same-owner device denied")
	}
	if ownedIP(st, "127.0.0.1") {
		t.Fatal("arbitrary IP accepted")
	}
	peer.UserID = 18
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("other owner accepted")
	}
	peer.UserID = 17
	peer.ShareeNode = true
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("sharee accepted")
	}
	peer.ShareeNode = false
	peer.AltSharerUserID = 25
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("shared node accepted")
	}
	peer.AltSharerUserID = 0
	tags := views.SliceOf([]string{"tag:server"})
	peer.Tags = &tags
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("tagged node accepted")
	}
	peer.Tags = nil
	st.Self.Tags = &tags
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("tagged client accepted")
	}
	st.Self.Tags = nil
	st.BackendState = "NeedsLogin"
	if ownedIP(st, "100.70.1.2") {
		t.Fatal("logged-out client accepted")
	}
	if owner(nil) != 0 {
		t.Fatal("nil backend has an owner")
	}
}

func TestAndroidInterfaceSnapshotPreservesHostAddresses(t *testing.T) {
	err := updateInterfaces(`{"interfaces":[{"name":"wlan0","index":3,"mtu":1500,"flags":49,"addresses":["192.0.2.27/24","2001:db8::27/64","invalid"]}],"defaultInterface":"wlan0"}`)
	if err != nil {
		t.Fatal(err)
	}
	interfaceMu.RLock()
	snapshot := append([]netmon.Interface{}, currentInterfaces...)
	interfaceMu.RUnlock()
	if len(snapshot) != 1 || !snapshot[0].IsUp() {
		t.Fatal("Android interface snapshot lost flags")
	}
	addresses, err := snapshot[0].Addrs()
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 || addresses[0].String() != "192.0.2.27/24" || addresses[1].String() != "2001:db8::27/64" {
		t.Fatalf("host addresses were masked or malformed: %v", addresses)
	}
}

func TestTCPProxyRoundtripAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serveTCP(ctx, ln, "100.70.1.2:47989", func(_ context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "100.70.1.2:47989" {
				t.Errorf("wrong destination %s %s", network, address)
			}
			a, b := net.Pipe()
			go func() { defer b.Close(); io.Copy(b, b) }()
			return a, nil
		}, &wg)
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("desktop request via authenticated tailnet")
	if _, err = client.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatal("TCP payload differs")
	}
	cancel()
	ln.Close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("TCP proxy did not stop")
	}
}

func TestUDPProxyKeepsIndependentClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	upstream, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, err := upstream.ReadFromUDP(buf)
			if err != nil {
				return
			}
			upstream.WriteToUDP(buf[:n], peer)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serveUDP(ctx, socket, "100.70.1.2:47998", func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "udp" || address != "100.70.1.2:47998" {
				t.Errorf("wrong UDP destination")
			}
			return (&net.Dialer{}).DialContext(ctx, "udp", upstream.LocalAddr().String())
		}, &wg)
	}()
	for _, payload := range []string{"video-client-one", "video-client-two"} {
		client, err := net.Dial("udp", socket.LocalAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		client.SetDeadline(time.Now().Add(3 * time.Second))
		client.Write([]byte(payload))
		buf := make([]byte, 1024)
		n, err := client.Read(buf)
		client.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(buf[:n]) != payload {
			t.Fatal("UDP flow mixed or corrupted")
		}
	}
	cancel()
	socket.Close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP proxy did not stop")
	}
}
