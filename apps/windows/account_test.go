package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountHandlerRejectsMissingIdentity(t *testing.T) {
	m := &AccountManager{}
	r := httptest.NewRequest("GET", "http://device/v1/device", nil)
	r.Header.Set("X-User-ID", "123")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing identity accepted: %d", w.Code)
	}
	r = r.WithContext(context.WithValue(r.Context(), networkAccountPeerKey{}, NetworkPeer{ID: "test"}))
	r.Header.Set("Origin", "https://example.invalid")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("browser request accepted: %d", w.Code)
	}
}

func TestAccountPairValidationBeforeHost(t *testing.T) {
	m := &AccountManager{}
	for _, body := range []string{`{}`, `{"nonce":"abc","pin":"1234","name":"test"}`, `{"nonce":"12345678901234567890123456789012","pin":"abcd","name":"test"}`} {
		r := httptest.NewRequest("POST", "http://device/v1/pair", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), networkAccountPeerKey{}, NetworkPeer{ID: "test"}))
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("invalid pairing accepted: %d", w.Code)
		}
	}
}

func TestQtPEMDecode(t *testing.T) {
	for _, v := range []string{`@ByteArray(-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----\n)`, `"@ByteArray(-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----\n)"`} {
		want := "-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----\n"
		if got := decodeQtValue(v); got != want {
			t.Fatalf("incorrect PEM decode: %q", got)
		}
	}
}

func TestLiveAccountAutoPair(t *testing.T) {
	if os.Getenv("DD_TEST_ACCOUNT") != "1" {
		t.Skip("opt-in live Sunshine/Moonlight pairing")
	}
	base := os.Getenv("DD_TEST_ACCOUNT_DIR")
	if base == "" {
		base = t.TempDir()
	}
	a, err := newApp(base)
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	if err = a.host.Start("DD account integration"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	for {
		st, _ := a.host.Status(ctx)
		if st.Ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("host start timeout")
		case <-time.After(250 * time.Millisecond):
		}
	}
	a.network.mu.Lock()
	a.network.status = NetworkStatus{Running: true, Receiving: true, SelfID: "test-host", Account: NetworkAccount{ID: "test-owner"}}
	a.network.mu.Unlock()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}
	bridge, err := startNetworkClientBridge(ctx, "test-host", dial)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	nonce := "0123456789abcdef0123456789abcdef"
	bridge.pairTag = nonce
	exe, err := extractPayload(a.nativeDir, "moonlight", "Moonlight.exe")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(filepath.Join(a.nativeDir, "sunshine", "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := a.ensureClientProfile()
	if err != nil {
		t.Fatal(err)
	}
	if err = runAccountPair(ctx, exe, profile, networkClientHost, string(cert), "4178", func(ctx context.Context) error {
		r := httptest.NewRequest("POST", "http://device/v1/pair", strings.NewReader(fmt.Sprintf(`{"nonce":%q,"pin":"4178","name":"DD integration"}`, nonce)))
		r = r.WithContext(context.WithValue(ctx, networkAccountPeerKey{}, NetworkPeer{ID: "test-client"}))
		w := httptest.NewRecorder()
		a.account.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			return fmt.Errorf("account pair %d: %s", w.Code, w.Body.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !moonlightPaired(ctx, profile, networkClientHost, string(cert)) {
		t.Fatal("repeat mutual TLS check failed")
	}
	report := map[string]any{"time": time.Now().Format(time.RFC3339), "autoPair": true, "persistedIdentity": true, "mutualTLS": true, "repeatConnectionIdentity": true, "transport": "local production TCP/UDP bridge; no user tailnet login"}
	if os.Getenv("DD_TEST_STREAM") == "1" {
		streamLog, err := os.Create(filepath.Join(base, "stream-client.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := hiddenCommand(exe, "stream", networkClientHost, "Desktop", "--720", "--fps", "30", "--bitrate", "5000", "--packet-size", "1024", "--display-mode", "windowed", "--video-codec", "H.264")
		cmd.Dir = profile
		cmd.Stderr = streamLog
		cmd.Stdout = streamLog
		if err = cmd.Start(); err != nil {
			streamLog.Close()
			t.Fatal(err)
		}
		a.clientMu.Lock()
		a.watchClient(cmd)
		a.clientMu.Unlock()
		select {
		case <-a.clientDone:
		case <-time.After(25 * time.Second):
		}
		a.closeClientProcess()
		streamLog.Close()
		data, err := os.ReadFile(filepath.Join(base, "stream-client.log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Log("actual stream process log saved for validation")
		report["streamLogBytes"] = len(data)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(base, "account-pair-result.json"), data, 0600)
	t.Log("actual Sunshine + portable Moonlight nonce-bound automatic pair and repeat mTLS passed")
}
