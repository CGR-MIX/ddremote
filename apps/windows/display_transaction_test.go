package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

const testDisplaySession = "0123456789abcdef0123456789abcdef"

type memoryDisplayJournal struct {
	events   *[]string
	failSave bool
	present  bool
}

func (s *memoryDisplayJournal) Save(displayJournal) error {
	*s.events = append(*s.events, "save")
	if s.failSave {
		return errors.New("disk full")
	}
	s.present = true
	return nil
}
func (s *memoryDisplayJournal) Remove() error {
	*s.events = append(*s.events, "remove")
	s.present = false
	return nil
}

type testVirtualDevice struct{ events *[]string }

func (d testVirtualDevice) Close() error { *d.events = append(*d.events, "device-close"); return nil }
func sampleDisplaySnapshot() DisplaySnapshot {
	return DisplaySnapshot{Paths: []displayPath{{}}, Modes: []displayMode{{Kind: 1}}}
}
func TestDisplayJournalDurableBeforeDeviceAndRestoresBeforeRemoval(t *testing.T) {
	events := []string{}
	store := &memoryDisplayJournal{events: &events}
	tx, err := beginDisplayTransaction(sampleDisplaySnapshot(), testDisplaySession, store, func(DisplaySnapshot) error { events = append(events, "restore"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, "device-create")
	tx.device = testVirtualDevice{&events}
	if err = tx.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"save", "device-create", "restore", "device-close", "remove"}) {
		t.Fatal(events)
	}
}
func TestDisplayRestoreFailureRetainsJournalAndRetriesAfterDeviceRemoved(t *testing.T) {
	events := []string{}
	store := &memoryDisplayJournal{events: &events}
	fail := true
	tx, err := beginDisplayTransaction(sampleDisplaySnapshot(), testDisplaySession, store, func(DisplaySnapshot) error {
		events = append(events, "restore")
		if fail {
			return errors.New("screen disconnected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tx.device = testVirtualDevice{&events}
	if tx.Close() == nil || !store.present {
		t.Fatal("lost recovery data")
	}
	if !reflect.DeepEqual(events, []string{"save", "restore", "device-close", "restore"}) {
		t.Fatal(events)
	}
	fail = false
	if tx.Close() != nil || store.present {
		t.Fatal("recovery failed")
	}
}
func TestDisplayJournalWriteFailurePreventsTransaction(t *testing.T) {
	events := []string{}
	store := &memoryDisplayJournal{events: &events, failSave: true}
	tx, err := beginDisplayTransaction(sampleDisplaySnapshot(), testDisplaySession, store, func(DisplaySnapshot) error { t.Fatal("restore called before save"); return nil })
	if tx != nil || err == nil {
		t.Fatal("started with no recovery data")
	}
}
func TestDisplayJournalRoundTripAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	store := fileDisplayJournal{dir}
	original := displayJournal{Version: 1, Session: testDisplaySession, Original: sampleDisplaySnapshot()}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	if store.Save(original) == nil {
		t.Fatal("overwrote recovery")
	}
	actual, err := store.Load()
	if err != nil || !reflect.DeepEqual(actual, original) {
		t.Fatal(actual, err)
	}
	if err = store.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, displayJournalName), []byte(`{"version":1,"session":"bad"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil {
		t.Fatal("invalid journal accepted")
	}
}
func TestVirtualTargetOwnershipAndAmbiguity(t *testing.T) {
	dd := displayLUID{Low: 2}
	uu := displayLUID{Low: 3}
	path := func(adapter displayLUID, id uint32) displayPath {
		return displayPath{Target: displayTarget{Adapter: adapter, ID: id, Technology: 17, Available: 1}}
	}
	lookup := func(id displayLUID) (string, error) {
		if id == dd {
			return "DD-Interface", nil
		}
		return "UU-Interface", nil
	}
	snap := DisplaySnapshot{Paths: []displayPath{path(uu, 1), path(dd, 2)}}
	chosen, err := ownedVirtualPath(snap, []string{"dd-interface"}, lookup)
	if err != nil || chosen.Target.Adapter != dd {
		t.Fatal(chosen, err)
	}
	if _, err = ownedVirtualPath(snap, []string{"missing"}, lookup); err == nil {
		t.Fatal("selected somebody else's device")
	}
	snap.Paths = append(snap.Paths, path(dd, 3))
	if _, err = ownedVirtualPath(snap, []string{"DD-Interface"}, lookup); err == nil {
		t.Fatal("guessed between targets")
	}
}
func TestVirtualOnlyModeRejectsUnsupportedAndChecksAppliedState(t *testing.T) {
	target := displayPath{Source: displaySource{ID: 4, Adapter: displayLUID{Low: 2}}, Target: displayTarget{ID: 1, Adapter: displayLUID{Low: 2}, Available: 1, Technology: 17}}
	layout, err := virtualOnlyLayout(target, 1920, 1080, 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyVirtualOnly(layout, target, 1920, 1080, 60); err != nil {
		t.Fatal(err)
	}
	if _, err = virtualOnlyLayout(target, 1080, 1920, 60); err == nil {
		t.Fatal("unsupported portrait")
	}
	layout.Paths = append(layout.Paths, target)
	if verifyVirtualOnly(layout, target, 1920, 1080, 60) == nil {
		t.Fatal("physical screen still active")
	}
	if unsafe.Sizeof(softwareDeviceCreateInfo{}) != 72 {
		t.Fatal("SW_DEVICE_CREATE_INFO layout mismatch")
	}
	if err = swdevice.NewProc("SwDeviceCreate").Find(); err != nil {
		t.Fatal(err)
	}
}
func TestSuperScreenLeaseOwnershipExpiryAndCleanupFailure(t *testing.T) {
	now := time.Now()
	surface := &fakePrivacySurface{done: make(chan struct{})}
	m := &SuperScreenManager{now: func() time.Time { return now }, supported: func() error { return nil }, start: func(context.Context, uint32, uint32, uint32) (privacySurface, error) { return surface, nil }}
	lease, err := m.Enable(context.Background(), "phone", 1920, 1080, 60)
	if err != nil {
		t.Fatal(err)
	}
	if m.Renew("other", lease) == nil || m.Disable("other", lease) == nil || m.Disable("phone", "wrong") == nil {
		t.Fatal("foreign lease accepted")
	}
	now = now.Add(10 * time.Second)
	if m.Renew("phone", lease) != nil {
		t.Fatal("renew failed")
	}
	now = now.Add(16 * time.Second)
	if m.Status().Active || surface.closed != 1 || m.Renew("phone", lease) == nil {
		t.Fatal("expired device retained/recreated")
	}
}

func TestSuperScreenAccountAuthAndLogoutCleanup(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	fakeEphemeralRun(t, a.network, func(context.Context) error { return nil })
	if err = os.MkdirAll(filepath.Join(a.host.dir, "sunshine"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.host.dir, "sunshine", "sunshine.conf"), []byte("bind_address = 127.0.0.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	surface := &fakePrivacySurface{done: make(chan struct{})}
	a.superScreen.start = func(context.Context, uint32, uint32, uint32) (privacySurface, error) { return surface, nil }
	a.superScreen.supported = func() error { return nil }
	a.network.mu.Lock()
	a.network.status.Receiving = true
	a.network.receiving = true
	a.network.mu.Unlock()
	request := func(owner, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://device/v1/super-screen", strings.NewReader(body))
		if owner != "" {
			r = r.WithContext(context.WithValue(r.Context(), networkAccountPeerKey{}, NetworkPeer{ID: owner}))
		}
		w := httptest.NewRecorder()
		a.account.ServeHTTP(w, r)
		return w
	}
	if w := request("", `{"action":"enable","width":1920,"height":1080,"fps":60}`); w.Code != 403 {
		t.Fatal("unauthenticated request accepted")
	}
	if w := request("owner", `{"action":"enable","width":1920,"height":1080,"fps":60,"unexpected":true}`); w.Code != 400 {
		t.Fatal("unknown fields accepted")
	}
	w := request("owner", `{"action":"enable","width":1920,"height":1080,"fps":60}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var reply struct{ Lease string }
	if err = json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.Lease == "" {
		t.Fatal("missing lease", err)
	}
	if w = request("other", `{"action":"disable","lease":"`+reply.Lease+`"}`); w.Code != 409 || surface.closed != 0 {
		t.Fatal("foreign peer stopped display")
	}
	if _, err = a.logoutAccount(); err != nil {
		t.Fatal(err)
	}
	if surface.closed != 1 || a.superScreen.Status().Active {
		t.Fatal("logout retained virtual screen")
	}
	if w = request("owner", `{"action":"renew","lease":"`+reply.Lease+`"}`); w.Code != 409 {
		t.Fatal("old lease reactivated")
	}
}

type failedScreenRestore struct{ fakePrivacySurface }

func (s *failedScreenRestore) Close() error {
	s.fakePrivacySurface.Close()
	return errors.New("restore failed")
}
func TestSuperScreenReportsRecoveryFailureOnLateDisable(t *testing.T) {
	surface := &failedScreenRestore{fakePrivacySurface{done: make(chan struct{})}}
	m := &SuperScreenManager{surface: surface, owner: "phone", lease: "lease", expires: time.Now().Add(time.Minute), now: time.Now, supported: func() error { return nil }}
	if m.Disable("phone", "lease") == nil || m.Status().RecoveryError == "" {
		t.Fatal("restore failure hidden")
	}
	if m.Disable("phone", "lease") == nil || m.Stop() == nil {
		t.Fatal("reported false restoration success")
	}
}

func TestSuperScreenManualRecoveryClearsOnlyAfterSuccessfulRestore(t *testing.T) {
	fail := true
	m := &SuperScreenManager{now: time.Now, supported: func() error { return nil }, recoveryError: "old failed restore", recover: func() error {
		if fail {
			return errors.New("display unavailable")
		}
		return nil
	}}
	if m.Restore() == nil || m.Status().RecoveryError == "" {
		t.Fatal("failed retry lost warning")
	}
	fail = false
	if m.Restore() != nil || m.Status().RecoveryError != "" {
		t.Fatal("successful retry not acknowledged")
	}
}
