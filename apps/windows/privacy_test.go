package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakePrivacySurface struct {
	done            chan struct{}
	closed, renewed int
	fail            bool
}

func (s *fakePrivacySurface) Renew() error {
	if s.fail {
		return errors.New("gone")
	}
	s.renewed++
	return nil
}
func (s *fakePrivacySurface) Close() error {
	if s.closed == 0 {
		close(s.done)
	}
	s.closed++
	return nil
}
func (s *fakePrivacySurface) Done() <-chan struct{} { return s.done }
func privacyFixture() (*PrivacyManager, *fakePrivacySurface, *time.Time) {
	now := time.Now()
	surface := &fakePrivacySurface{done: make(chan struct{})}
	m := &PrivacyManager{start: func(context.Context) (privacySurface, error) { return surface, nil }, supported: func() error { return nil }, now: func() time.Time { return now }}
	return m, surface, &now
}
func TestPrivacyLeaseIdentityExpiryAndNoRecreation(t *testing.T) {
	m, s, now := privacyFixture()
	lease, err := m.Enable(context.Background(), "peer-1")
	if err != nil || lease == "" {
		t.Fatal(err)
	}
	if _, err = m.Enable(context.Background(), "peer-2"); err == nil {
		t.Fatal("parallel masking session accepted")
	}
	if m.Renew("peer-2", lease) == nil || m.Disable("peer-2", lease) == nil || m.Renew("peer-1", "wrong") == nil {
		t.Fatal("foreign identity/lease accepted")
	}
	*now = now.Add(10 * time.Second)
	if err = m.Renew("peer-1", lease); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(14 * time.Second)
	if !m.Status().Active {
		t.Fatal("renewal lost")
	}
	*now = now.Add(2 * time.Second)
	if m.Status().Active || s.closed != 1 {
		t.Fatal("expired mask not released")
	}
	if m.Renew("peer-1", lease) == nil || m.Status().Active {
		t.Fatal("late heartbeat recreated mask")
	}
	m.Stop()
	if s.closed != 1 {
		t.Fatal("duplicate shutdown")
	}
}
func TestPrivacyLocalEscapeAndHelperFailure(t *testing.T) {
	m, s, _ := privacyFixture()
	lease, _ := m.Enable(context.Background(), "peer")
	close(s.done)
	s.closed = 1
	if m.Renew("peer", lease) == nil || m.Status().Active {
		t.Fatal("local escape undone by heartbeat")
	}
	m, s, _ = privacyFixture()
	lease, _ = m.Enable(context.Background(), "peer")
	s.fail = true
	if m.Renew("peer", lease) == nil || s.closed != 1 {
		t.Fatal("dead helper retained")
	}
}
func TestPrivacyCancellationClosesNewSurface(t *testing.T) {
	m, s, _ := privacyFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Enable(ctx, "peer"); err == nil || s.closed != 1 || m.Status().Active {
		t.Fatal("cancelled enable retained window")
	}
}
func TestPrivacyUnsupportedNeverStarts(t *testing.T) {
	m, s, _ := privacyFixture()
	m.supported = func() error { return errors.New("unsupported") }
	if _, err := m.Enable(context.Background(), "peer"); err == nil || s.closed != 0 || m.Status().Available {
		t.Fatal("unsupported masking accepted")
	}
}
func TestPrivacyAccountEndpointRejectsMissingIdentityAndMalformedBody(t *testing.T) {
	m, _, _ := privacyFixture()
	account := &AccountManager{app: &App{privacy: m}}
	for _, body := range []string{`{"action":"enable"}`, `{"action":"disable","lease":"x"}`} {
		req := httptest.NewRequest("POST", "http://device/v1/privacy", strings.NewReader(body))
		w := httptest.NewRecorder()
		account.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("unauthenticated %d", w.Code)
		}
	}
	for _, body := range []string{`{"action":"enable","unknown":true}`, `{"action":"enable"}{}`, strings.Repeat("x", 1500)} {
		req := httptest.NewRequest("POST", "http://device/v1/privacy", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), networkAccountPeerKey{}, NetworkPeer{ID: "peer"}))
		w := httptest.NewRecorder()
		account.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("malformed input accepted %d", w.Code)
		}
	}
}

func TestPrivacyHTTPUsesAuthenticatedOwnerAndStopsOnLogout(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	fakeEphemeralRun(t, a.network, func(context.Context) error { return nil })
	surface := &fakePrivacySurface{done: make(chan struct{})}
	a.privacy.start = func(context.Context) (privacySurface, error) { return surface, nil }
	a.privacy.supported = func() error { return nil }
	a.network.mu.Lock()
	a.network.status.Receiving = true
	a.network.receiving = true
	a.network.mu.Unlock()
	request := func(owner, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://device/v1/privacy", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), networkAccountPeerKey{}, NetworkPeer{ID: owner}))
		w := httptest.NewRecorder()
		a.account.ServeHTTP(w, req)
		return w
	}
	w := request("owner", `{"action":"enable"}`)
	if w.Code != 200 {
		t.Fatalf("enable failed %d %s", w.Code, w.Body.String())
	}
	var reply struct {
		Lease string `json:"lease"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.Lease == "" {
		t.Fatal("lease missing")
	}
	w = request("other", `{"action":"disable","lease":"`+reply.Lease+`"}`)
	if w.Code != 409 || surface.closed != 0 {
		t.Fatal("other peer disabled mask")
	}
	w = request("owner", `{"action":"renew","lease":"`+reply.Lease+`"}`)
	if w.Code != 200 || surface.renewed != 1 {
		t.Fatal("owner renewal failed")
	}
	if _, err = a.logoutAccount(); err != nil {
		t.Fatal(err)
	}
	if surface.closed != 1 || a.privacy.Status().Active {
		t.Fatal("logout retained mask")
	}
	w = request("owner", `{"action":"renew","lease":"`+reply.Lease+`"}`)
	if w.Code != 409 {
		t.Fatal("stale logout lease accepted")
	}
}
