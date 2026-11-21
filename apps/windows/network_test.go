package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

func TestNetworkTCPProxyPreservesHalfCloseAndResponse(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	upDone := make(chan error, 1)
	go func() {
		c, err := up.Accept()
		if err != nil {
			upDone <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		body, err := io.ReadAll(c)
		if err == nil {
			_, err = c.Write(append([]byte("response:"), body...))
		}
		upDone <- err
	}()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); serveNetworkForward(ctx, front, "tcp", up.Addr().String(), &wg) }()
	defer func() { cancel(); front.Close(); wg.Wait() }()
	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	payload := bytes.Repeat([]byte("DD-stream\x00\xff"), 8192)
	if _, err = c.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append([]byte("response:"), payload...)) {
		t.Fatal("TCP response was truncated or changed")
	}
	if err := <-upDone; err != nil {
		t.Fatal(err)
	}
}

func connectedUDPPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := net.DialUDP("udp4", nil, a.LocalAddr().(*net.UDPAddr))
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	// Bind a to the known peer using a PacketConn adapter so both ends retain
	// datagram boundaries while presenting the net.Conn used by tsnet flows.
	return &fixedPeerUDP{UDPConn: a, peer: b.LocalAddr().(*net.UDPAddr)}, b
}

type fixedPeerUDP struct {
	*net.UDPConn
	peer *net.UDPAddr
}

func (c *fixedPeerUDP) Read(p []byte) (int, error)  { n, _, err := c.ReadFromUDP(p); return n, err }
func (c *fixedPeerUDP) Write(p []byte) (int, error) { return c.WriteToUDP(p, c.peer) }
func (c *fixedPeerUDP) RemoteAddr() net.Addr        { return c.peer }

func TestNetworkUDPProxyPreservesPacketBoundaries(t *testing.T) {
	a, client := connectedUDPPair(t)
	b, server := connectedUDPPair(t)
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); bridgeNetworkDatagrams(ctx, a, b, time.Second) }()
	defer func() { cancel(); <-done }()
	packets := [][]byte{[]byte("PING"), {}, bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 350), bytes.Repeat([]byte{1}, 65000)}
	for _, packet := range packets {
		client.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err := client.Write(packet); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 65535)
		server.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := server.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf[:n], packet) {
			t.Fatalf("datagram boundary/content changed: got %d, want %d", n, len(packet))
		}
		server.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err = server.Write(packet); err != nil {
			t.Fatal(err)
		}
		client.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err = client.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf[:n], packet) {
			t.Fatal("reverse datagram changed")
		}
	}
}

func TestNetworkUDPProxyExpiresIdleAndCancels(t *testing.T) {
	for _, cancelImmediately := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "cancel"}[cancelImmediately], func(t *testing.T) {
			a, client := connectedUDPPair(t)
			defer client.Close()
			b, server := connectedUDPPair(t)
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); bridgeNetworkDatagrams(ctx, a, b, 40*time.Millisecond) }()
			if cancelImmediately {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("UDP resources did not close")
			}
		})
	}
}

func TestNetworkForwardCreationFailureClosesEarlierListeners(t *testing.T) {
	var listeners []net.Listener
	_, err := startNetworkForwards(context.Background(), func(network, address string) (net.Listener, error) {
		if len(listeners) == 2 {
			return nil, errors.New("injected port failure")
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			listeners = append(listeners, ln)
		}
		return ln, err
	})
	if err == nil {
		t.Fatal("expected startup error")
	}
	for _, ln := range listeners {
		if c, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
			c.Close()
			t.Fatal("listener leaked after startup failure")
		}
	}
	for _, p := range networkForwardPorts {
		if p.port == 47990 {
			t.Fatal("admin port must never be exposed")
		}
	}
}

func TestNetworkStatusExcludesShareeAndDoesNotClaimOfflineNodeRunning(t *testing.T) {
	peers := make(map[key.NodePublic]*ipnstate.PeerStatus)
	peers[key.NewNode().Public()] = &ipnstate.PeerStatus{ID: "desktop-id", UserID: 7, HostName: "desktop", Online: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::2"), netip.MustParseAddr("100.64.0.2")}}
	peers[key.NewNode().Public()] = &ipnstate.PeerStatus{UserID: 7, HostName: "hidden", ShareeNode: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.3")}}
	peers[key.NewNode().Public()] = &ipnstate.PeerStatus{UserID: 8, HostName: "other-user", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.4")}}
	tags := views.SliceOf([]string{"tag:server"})
	peers[key.NewNode().Public()] = &ipnstate.PeerStatus{UserID: 7, HostName: "tagged", Tags: &tags, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.5")}}
	peers[key.NewNode().Public()] = &ipnstate.PeerStatus{UserID: 7, HostName: "shared", AltSharerUserID: 9, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.6")}}
	st := networkStatusFromBackend(&ipnstate.Status{BackendState: "NeedsLogin", AuthURL: "https://example.test/auth", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}, Peer: peers})
	if st.Running || st.AuthURL == "" || len(st.Peers) != 0 || st.Account.ID != "" {
		t.Fatalf("unexpected status: %+v", st)
	}
	st = networkStatusFromBackend(&ipnstate.Status{BackendState: "Running", AuthURL: "https://example.test/auth", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}, Self: &ipnstate.PeerStatus{UserID: 7}, Peer: peers, User: map[tailcfg.UserID]tailcfg.UserProfile{7: {ID: 7, LoginName: "chan@example.test", DisplayName: "Chan"}}})
	if !st.Running || st.AuthURL != "" || len(st.Peers) != 1 || st.Peers[0].IP != "100.64.0.2" || st.Peers[0].ID != "desktop-id" || st.Account.ID != "7" || st.Account.Login != "chan@example.test" || st.Account.Name != "Chan" {
		t.Fatalf("invalid account ownership filter: %+v", st)
	}
}

type fakeAccountBackend struct {
	status            *ipnstate.Status
	who               *apitype.WhoIsResponse
	statusErr, whoErr error
	remote            string
	lookups           int
}

func (f *fakeAccountBackend) Status(context.Context) (*ipnstate.Status, error) {
	return f.status, f.statusErr
}
func (f *fakeAccountBackend) WhoIs(_ context.Context, remote string) (*apitype.WhoIsResponse, error) {
	f.remote = remote
	f.lookups++
	return f.who, f.whoErr
}

func TestAccountControlPlaneUsesFreshWireGuardOwnerIdentity(t *testing.T) {
	tags := views.SliceOf([]string{"tag:server"})
	for _, tc := range []struct {
		name   string
		mutate func(*fakeAccountBackend)
		want   int
	}{
		{"same user", func(*fakeAccountBackend) {}, http.StatusNoContent},
		{"different tailnet user", func(b *fakeAccountBackend) { b.who.Node.User = 8; b.who.UserProfile.ID = 8 }, http.StatusForbidden},
		{"tagged remote", func(b *fakeAccountBackend) { b.who.Node.Tags = []string{"tag:server"} }, http.StatusForbidden},
		{"shared remote", func(b *fakeAccountBackend) { b.who.Node.Sharer = 9 }, http.StatusForbidden},
		{"sharee remote", func(b *fakeAccountBackend) { b.who.Node.Hostinfo = (&tailcfg.Hostinfo{ShareeNode: true}).View() }, http.StatusForbidden},
		{"unknown identity", func(b *fakeAccountBackend) { b.who = nil }, http.StatusForbidden},
		{"missing node", func(b *fakeAccountBackend) { b.who.Node = nil }, http.StatusForbidden},
		{"missing profile", func(b *fakeAccountBackend) { b.who.UserProfile = nil }, http.StatusForbidden},
		{"inconsistent profile", func(b *fakeAccountBackend) { b.who.UserProfile.ID = 8 }, http.StatusForbidden},
		{"lookup failure", func(b *fakeAccountBackend) { b.whoErr = errors.New("unknown remote") }, http.StatusForbidden},
		{"logged out", func(b *fakeAccountBackend) { b.status.BackendState = "NeedsLogin" }, http.StatusServiceUnavailable},
		{"unknown owner", func(b *fakeAccountBackend) { b.status.Self.UserID = 0 }, http.StatusServiceUnavailable},
		{"tagged local", func(b *fakeAccountBackend) { b.status.Self.Tags = &tags }, http.StatusServiceUnavailable},
		{"shared local", func(b *fakeAccountBackend) { b.status.Self.ShareeNode = true }, http.StatusServiceUnavailable},
		{"status failure", func(b *fakeAccountBackend) { b.statusErr = errors.New("offline") }, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeAccountBackend{status: &ipnstate.Status{BackendState: "Running", Self: &ipnstate.PeerStatus{UserID: 7}}, who: &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: "remote-stable", User: 7, Name: "remote.tailnet.test."}, UserProfile: &tailcfg.UserProfile{ID: 7}}}
			tc.mutate(backend)
			m := NewNetworkManager(t.TempDir(), nil)
			r := &networkRun{ctx: context.Background()}
			m.run = r
			called := false
			m.SetAccountHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				called = true
				peer, ok := NetworkAccountPeer(req.Context())
				if !ok || peer.ID != "remote-stable" || peer.IP != "100.64.0.2" {
					t.Errorf("missing verified peer: %+v", peer)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "http://100.64.0.1:48201/info", nil)
			req.RemoteAddr = "100.64.0.2:12345"
			req.Header.Set("X-Forwarded-For", "100.64.0.99")
			req.Header.Set("Tailscale-User-Login", "owner@example.test")
			rec := httptest.NewRecorder()
			m.networkAccountHandler(r, backend).ServeHTTP(rec, req)
			if rec.Code != tc.want || called != (tc.want == http.StatusNoContent) {
				t.Fatalf("got %d called=%v want %d", rec.Code, called, tc.want)
			}
			if backend.lookups != 0 && backend.remote != req.RemoteAddr {
				t.Fatal("trusted forwarded identity instead of socket address")
			}
			if tc.want == http.StatusNoContent {
				backend.who.Node.User = 8
				rec = httptest.NewRecorder()
				m.networkAccountHandler(r, backend).ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden || backend.lookups != 2 {
					t.Fatal("request reused stale authorization")
				}
				m.SetAccountHandler(nil)
				rec = httptest.NewRecorder()
				m.networkAccountHandler(r, backend).ServeHTTP(rec, req)
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatal("disabled account handler remained reachable")
				}
			}
		})
	}
}

func TestNetworkErrorRedactsURLsAndKeys(t *testing.T) {
	got := redactNetworkError(errors.New("Get https://example.test/auth/secret failed with tskey-auth-sensitive"))
	if strings.Contains(got, "secret") || strings.Contains(got, "tskey-") {
		t.Fatal("error leaked authentication information")
	}
}

func TestNetworkManagerStopBeforeStartAndCanceledStart(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(m.Start(ctx, "dd-test"), context.Canceled) {
		t.Fatal("canceled start must not initialize a node")
	}
	if m.Status(context.Background()).State != "Stopped" {
		t.Fatal("canceled start changed state")
	}
}

func TestNetworkClientUDPSeparatesSourceFlows(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, peer, err := echo.ReadFromUDP(buf)
			if err != nil {
				return
			}
			echo.WriteToUDP(buf[:n], peer)
		}
	}()
	front, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var mu sync.Mutex
	dials := 0
	dial := func(ctx context.Context, network, target string) (net.Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		serveNetworkClientUDP(ctx, front, echo.LocalAddr().String(), dial, &wg, time.Second)
	}()
	defer func() { cancel(); front.Close(); wg.Wait() }()
	var clients []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("udp4", front.LocalAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients = append(clients, c)
	}
	for i, c := range clients {
		for _, packet := range [][]byte{{byte(i), 1, 2, 3}, {}, bytes.Repeat([]byte{byte(i + 1)}, 1400)} {
			c.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := c.Write(packet); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 65535)
			n, err := c.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf[:n], packet) {
				t.Fatal("client flow mixed or altered")
			}
		}
	}
	mu.Lock()
	count := dials
	mu.Unlock()
	if count != 2 {
		t.Fatalf("got %d upstream flows, want 2", count)
	}
}

func TestNetworkClientTargetValidation(t *testing.T) {
	for _, input := range []string{"https://host/", "host:47989", "127.0.0.1", "[::1]", "bad name", "-bad", ""} {
		if _, err := normalizeNetworkClientRemote(input); err == nil {
			t.Errorf("accepted invalid target %q", input)
		}
	}
	for _, input := range []string{"100.64.0.2", "dd-pc.tail123.ts.net", "[fd7a:115c:a1e0::2]"} {
		if _, err := normalizeNetworkClientRemote(input); err != nil {
			t.Errorf("rejected target %q: %v", input, err)
		}
	}
}

func TestNetworkReceivingAuthorizationClosesActiveFlows(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	r := &networkRun{ctx: ctx, cancel: cancel, receive: make(chan networkReceiveRequest), receiveDone: make(chan struct{})}
	m.run = r
	var front net.Listener
	starts := 0
	up, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	go m.runReceiving(r, func(ctx context.Context) (*networkForwards, error) {
		starts++
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		front = ln
		ctx, stop := context.WithCancel(ctx)
		f := &networkForwards{cancel: stop, listeners: []net.Listener{ln}}
		f.wg.Add(1)
		go func() { defer f.wg.Done(); serveNetworkForward(ctx, ln, "tcp", up.Addr().String(), &f.wg) }()
		return f, nil
	})
	defer func() { cancel(); <-r.receiveDone }()
	if err := r.changeReceiving(false, true); err != nil {
		t.Fatal(err)
	}
	if starts != 0 || m.Status(ctx).Receiving {
		t.Fatal("login alone exposed local ports")
	}
	if err := r.changeReceiving(false, false); err != nil {
		t.Fatal(err)
	}
	m.SetReceiving(true)
	if starts != 0 {
		t.Fatal("receiving started before network readiness")
	}
	if err := r.changeReceiving(false, true); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || !m.Status(ctx).Receiving {
		t.Fatal("authorized receiver did not start")
	}
	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("before-stop")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("before-stop"))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	m.SetReceiving(false)
	if m.Status(ctx).Receiving {
		t.Fatal("receiver remained enabled")
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("active incoming connection survived disable")
	}
	if next, err := net.DialTimeout("tcp", front.Addr().String(), 100*time.Millisecond); err == nil {
		next.Close()
		t.Fatal("listener survived disable")
	}
	for i := 0; i < 10; i++ {
		if err := r.changeReceiving(false, true); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 {
		t.Fatal("status poll recreated disabled listener")
	}
	m.SetReceiving(true)
	if starts != 2 {
		t.Fatal("explicit re-enable failed")
	}
	if err := r.changeReceiving(false, false); err != nil {
		t.Fatal(err)
	}
	if m.Status(ctx).Receiving {
		t.Fatal("offline node retained listeners")
	}
}

func TestNetworkReceivingCanBeDisabledBeforeStart(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	if m.receiving || m.Status(context.Background()).Receiving {
		t.Fatal("new manager must not receive")
	}
	m.SetReceiving(true)
	m.SetReceiving(false)
	if m.receiving {
		t.Fatal("pre-start disable was not retained")
	}
}

func TestEphemeralDefaultUsesMemoryAndIsolatedDirectory(t *testing.T) {
	persistent := filepath.Join(t.TempDir(), "saved-node")
	if err := os.MkdirAll(persistent, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(persistent, "tailscaled.state")
	if err := os.WriteFile(sentinel, []byte("existing identity"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewNetworkManager(persistent, nil)
	if !m.IsTemporary() || !m.Status(context.Background()).Temporary {
		t.Fatal("default identity must be ephemeral")
	}
	server, tempDir, err := newNetworkServer(persistent, "test-dd", true)
	if err != nil {
		t.Fatal(err)
	}
	defer removeTemporaryNetworkDirectory(tempDir)
	store, ok := server.Store.(*mem.Store)
	if !ok || !server.Ephemeral || tempDir == "" || server.Dir == persistent {
		t.Fatal("ephemeral node reused disk identity")
	}
	if err := store.WriteState(ipn.StateKey("test-identity"), []byte("private node key")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "tailscaled.state")); !os.IsNotExist(err) {
		t.Fatal("node identity was written to disk")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "existing identity" {
		t.Fatal("existing persistent identity was modified")
	}
	if err := removeTemporaryNetworkDirectory(persistent); err == nil {
		t.Fatal("cleanup accepted a persistent state directory")
	}
	if err := removeTemporaryNetworkDirectory(tempDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatal("temporary metadata was not removed")
	}
}

func TestEphemeralModeCannotChangeActiveIdentity(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	r := &networkRun{done: make(chan struct{})}
	m.run = r
	if err := m.SetEphemeral(false); err == nil {
		t.Fatal("active identity mode changed")
	}
	if err := m.SetEphemeral(true); err != nil {
		t.Fatal(err)
	}
	close(r.done)
	if err := m.SetEphemeral(false); err != nil || m.IsTemporary() {
		t.Fatal("stopped internal configuration could not change")
	}
}

func fakeEphemeralRun(t *testing.T, m *NetworkManager, logout func(context.Context) error) *networkRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &networkRun{ctx: ctx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), receiveDone: make(chan struct{}), temporary: true, logout: logout}
	close(r.ready)
	close(r.receiveDone)
	m.run = r
	go func() { <-ctx.Done(); close(r.done) }()
	return r
}

func TestEphemeralStopRequestsLogoutBeforeDisconnect(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	var r *networkRun
	called := 0
	r = fakeEphemeralRun(t, m, func(ctx context.Context) error {
		called++
		if r.ctx.Err() != nil || ctx.Err() != nil {
			t.Error("network closed before server logout")
		}
		return nil
	})
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if called != 1 || r.ctx.Err() == nil || m.run != nil || m.Status(context.Background()).State != "Stopped" {
		t.Fatal("temporary shutdown lifecycle incomplete")
	}
}

func TestEphemeralLogoutFailureStillDisconnectsAndReportsUncertainty(t *testing.T) {
	for _, stop := range []bool{false, true} {
		m := NewNetworkManager(t.TempDir(), nil)
		r := fakeEphemeralRun(t, m, func(context.Context) error { return errors.New("cannot reach https://example.test/token") })
		var err error
		if stop {
			err = m.Stop()
		} else {
			err = m.Logout(context.Background())
		}
		if err == nil || !strings.Contains(err.Error(), "未确认立即移除") || strings.Contains(err.Error(), "token") {
			t.Fatalf("missing accurate cleanup status: %v", err)
		}
		if r.ctx.Err() == nil || m.run != nil {
			t.Fatal("failed logout left the account connected")
		}
	}
}

func TestEphemeralLogoutRequestHasBoundedDeadline(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	r := fakeEphemeralRun(t, m, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	defer r.cancel()
	start := time.Now()
	err := logoutNetworkRun(context.Background(), r, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("logout not bounded: %v", err)
	}
}
