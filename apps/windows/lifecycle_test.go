package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func writePairingFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sunshine", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"root":{"uniqueid":"preserve-host","named_devices":[{"cert":"old-current-client"}],"devices":[{"certs":["old-legacy-client"]}]},"unrelated":{"setting":7}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPairingsRevoked(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	root := state["root"].(map[string]any)
	if root["uniqueid"] != "preserve-host" || state["unrelated"] == nil {
		t.Fatal("unrelated host state changed")
	}
	if root["devices"] != nil || root["named_devices"] != nil || strings.Contains(string(data), "old-") {
		t.Fatal("incoming desktop authorization survived logout")
	}
}

func TestLogoutCancelsActionsClearsIdentityAndRevokesGrants(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	fakeEphemeralRun(t, a.network, func(context.Context) error { return nil })
	statePath := writePairingFixture(t, a.nativeDir)
	oldToken := a.secrets.Token
	profile, err := a.ensureClientProfile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "test-key.pem"), []byte("test credential"), 0600); err != nil {
		t.Fatal(err)
	}
	a.account.cache = accountCache{Owner: "old-owner", Devices: map[string]AccountDevice{"old": {ID: "old"}}}
	session := a.sessionCtx
	a.actionMu.RLock()
	actionDone := make(chan struct{})
	go func() {
		<-session.Done()
		a.actionMu.RUnlock()
		close(actionDone)
	}()
	logoutDone := make(chan error, 1)
	go func() { _, err := a.logoutAccount(); logoutDone <- err }()
	select {
	case err := <-logoutDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logout waited for the old action instead of canceling it")
	}
	<-actionDone
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("outgoing session credential directory was retained")
	}
	if a.account.cache.Owner != "" || len(a.account.cache.Devices) != 0 {
		t.Fatal("old account devices retained")
	}
	if a.sessionCtx.Err() != nil || a.exiting {
		t.Fatal("fresh login is blocked after logout")
	}
	if response := transferRequest(a.transfer, "GET", "/api/files", "Bearer "+oldToken, nil); response.Code != 401 {
		t.Fatal("old file link still authorizes access")
	}
	assertPairingsRevoked(t, statePath)
	stored, err := loadSecrets(a.dir)
	if err != nil || stored.Token == oldToken || stored.Token != a.secrets.Token {
		t.Fatal("revocation was not saved")
	}
}

func TestNewProcessRevokesPreviousSessionAfterCrash(t *testing.T) {
	dir := t.TempDir()
	prior, err := loadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	native, err := chooseNativeDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	statePath := writePairingFixture(t, native)
	profile := filepath.Join(native, "account-session-crashed")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	if a.secrets.Token == prior.Token {
		t.Fatal("previous process file link survived restart")
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("crashed session identity retained")
	}
	assertPairingsRevoked(t, statePath)
}

func TestUnreadableAuthorizationStatePreventsReceiving(t *testing.T) {
	dir := t.TempDir()
	path := writePairingFixture(t, dir)
	if err := os.WriteFile(path, []byte("invalid state"), 0600); err != nil {
		t.Fatal(err)
	}
	host := NewHost(dir, &Secrets{}, func(string, ...any) {})
	if err := host.ForgetClients(); err == nil {
		t.Fatal("invalid authorization state accepted")
	}
	if err := host.Start("test"); err == nil || !strings.Contains(err.Error(), "授权清理未完成") {
		t.Fatal("host can restart after failed revocation")
	}
}

func TestFailedProfileCleanupCannotReuseOldAccountIdentity(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.shutdown()
	old, err := a.ensureClientProfile()
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(old, "locked-key"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { windows.CloseHandle(lock); os.RemoveAll(old) }()
	if err := a.clearClientProfile(); err == nil {
		t.Fatal("locked credential deletion reported success")
	}
	fresh, err := a.ensureClientProfile()
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("new account reused previous account private key directory")
	}
}
