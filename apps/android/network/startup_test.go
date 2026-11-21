package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tailscale.com/logpolicy"
)

func TestNetworkLogsUsePrivateDirectory(t *testing.T) {
	// The backend must not depend on the shell or desktop environment which
	// hid this failure in the previous Windows-hosted network tests.
	t.Setenv("TS_LOGS_DIR", "")
	dir := t.TempDir()
	if err := prepareNetworkLogDirectory(dir); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "logs")
	if got := logpolicy.LogsDir(t.Logf); got != want {
		t.Fatalf("upstream chose %q instead of private logs %q", got, want)
	}
	f, err := os.CreateTemp(logpolicy.LogsDir(t.Logf), "upstream-check-")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := prepareNetworkLogDirectory(dir); err != nil {
		t.Fatalf("second login cannot reuse private logs: %v", err)
	}
}

func TestInvalidLogDirectoryFailsBeforeBackend(t *testing.T) {
	t.Setenv("TS_LOGS_DIR", "unchanged")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{dir, "relative-path"} {
		if err := prepareNetworkLogDirectory(input); err == nil {
			t.Fatalf("accepted invalid log directory %q", input)
		}
	}
	if os.Getenv("TS_LOGS_DIR") != "unchanged" {
		t.Fatal("failed initialization changed the process log directory")
	}
}

func TestRecoveredFailureHasSafeDiagnostics(t *testing.T) {
	secret := "private-token-do-not-log"
	result := guardedCallJSON(func(string, string) (any, error) { panic(secret) }, secret, secret)
	var reply map[string]string
	if err := json.Unmarshal([]byte(result), &reply); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, secret) || !strings.Contains(reply["error"], "network") {
		t.Fatalf("panic or request data leaked into reply: %s", result)
	}
	if !strings.Contains(reply["diagnostic"], "TestRecoveredFailureHasSafeDiagnostics") {
		t.Fatalf("missing symbolic stack: %s", result)
	}
	result = guardedCallJSON(func(string, string) (any, error) {
		panic("no safe place found to store log state")
	}, "start", "{}")
	if !strings.Contains(result, "网络日志目录不可写") || !strings.Contains(result, "start") {
		t.Fatalf("known startup failure is not explained: %s", result)
	}
}
