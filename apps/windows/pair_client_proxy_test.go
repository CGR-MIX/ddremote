package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPairProxyOnlyChangesFirstDeviceName(t *testing.T) {
	nonce := "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		path, query, tag, want string
		bad                    bool
	}{
		{"/pair", "uniqueid=AB%2fCD&uuid=aa&devicename=roth&updateState=1&phrase=getservercert&salt=00&clientcert=FFFF", nonce, "uniqueid=AB%2fCD&uuid=aa&devicename=DD-" + nonce + "&updateState=1&phrase=getservercert&salt=00&clientcert=FFFF", false},
		{"/pair", "phrase=getservercert&uniqueid=abc&salt=ff", nonce, "phrase=getservercert&uniqueid=abc&salt=ff&devicename=DD-" + nonce, false},
		{"/pair", "phrase=getservercert&devicename=a&devicename=b&uniqueid=x", nonce, "phrase=getservercert&devicename=DD-" + nonce + "&uniqueid=x", false},
		{"/pair", "devicename=roth&clientchallenge=ABCD&uniqueid=x", nonce, "devicename=roth&clientchallenge=ABCD&uniqueid=x", false},
		{"/pair", "devicename=roth&phrase=pairchallenge&uniqueid=x", nonce, "devicename=roth&phrase=pairchallenge&uniqueid=x", false},
		{"/serverinfo", "devicename=x&phrase=getservercert", nonce, "devicename=x&phrase=getservercert", false},
		{"/pair", "phrase=getservercert&devicename=roth", "", "phrase=getservercert&devicename=roth", false},
		{"/pair", "phrase=getservercert&phrase=pairchallenge", nonce, "", true},
		{"/pair", "phrase=getservercert&devicename=%zz", nonce, "", true},
	} {
		got, err := taggedNetworkPairQuery(tc.path, tc.query, tc.tag)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("%s: got %q err=%v want %q bad=%v", tc.query, got, err, tc.want, tc.bad)
		}
	}
}

func TestPairHTTPProxyForwardsOnlyToSelectedNode(t *testing.T) {
	nonce := "0123456789abcdef0123456789abcdef"
	observed := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		observed <- req.URL.RawQuery
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, "<root status_code=\"200\">ok</root>")
	}))
	defer upstream.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	selectedTarget := "100.64.0.2:47989"
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != selectedTarget || network != "tcp" {
			t.Errorf("unselected destination %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(upstream.URL, "http://"))
	}
	server, transport := newNetworkPairProxy(ctx, selectedTarget, dial, func() string { return nonce })
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ln) }()
	defer func() { server.Close(); transport.CloseIdleConnections(); <-done }()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/pair?uniqueid=ORIGINAL&phrase=getservercert&devicename=roth")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || !strings.Contains(string(body), "status_code") {
		t.Fatalf("invalid upstream reply %s %v", body, err)
	}
	if got := <-observed; got != "uniqueid=ORIGINAL&phrase=getservercert&devicename=DD-"+nonce {
		t.Fatalf("incorrect pair query %s", got)
	}
	// Subsequent pairing phases have no phrase parameter and must pass through.
	resp, err = client.Get("http://" + ln.Addr().String() + "/pair?uniqueid=ORIGINAL&clientchallenge=ff&devicename=roth")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := <-observed; got != "uniqueid=ORIGINAL&clientchallenge=ff&devicename=roth" {
		t.Fatalf("later pairing phase changed %s", got)
	}
}

func TestPairTagRequiresPreparedBridgeAndExactNonce(t *testing.T) {
	m := NewNetworkManager(t.TempDir(), nil)
	if m.SetClientPairTag("0123456789abcdef0123456789abcdef") == nil {
		t.Fatal("accepted tag without selected remote")
	}
	m.client = &networkClientBridge{}
	for _, bad := range []string{"short", strings.Repeat("z", 32), "0123456789abcdef0123456789abcdef&x=1"} {
		if m.SetClientPairTag(bad) == nil {
			t.Fatalf("accepted invalid nonce %q", bad)
		}
	}
	if err := m.SetClientPairTag("0123456789ABCDEF0123456789ABCDEF"); err != nil {
		t.Fatal(err)
	}
	if m.client.currentPairTag() != "0123456789abcdef0123456789abcdef" {
		t.Fatal("tag was not set")
	}
	if err := m.SetClientPairTag(""); err != nil || m.client.currentPairTag() != "" {
		t.Fatal("tag was not cleared")
	}
}
