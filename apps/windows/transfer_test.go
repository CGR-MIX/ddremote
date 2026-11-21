package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testTransfer(t *testing.T) *TransferServer {
	t.Helper()
	dir := t.TempDir()
	s, e := loadSecrets(dir)
	if e != nil {
		t.Fatal(e)
	}
	server, e := NewTransfer(filepath.Join(dir, "received"), s, func(string, ...any) {})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(server.Close)
	var clipboard string
	server.readClipboard = func() (string, error) { return clipboard, nil }
	server.writeClipboard = func(s string) error { clipboard = s; return nil }
	return server
}
func transferRequest(s *TransferServer, method, path, auth string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}
func TestTransferAuthAndRevoke(t *testing.T) {
	s := testTransfer(t)
	for _, auth := range []string{"", "Bearer wrong", s.token} {
		r := transferRequest(s, "GET", "/api/files", auth, nil)
		if r.Code != 401 {
			t.Fatalf("unauthorized %q: %d", auth, r.Code)
		}
	}
	old := s.token
	if r := transferRequest(s, "GET", "/api/files", "Bearer "+old, nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s.SetToken(randomSecret())
	if r := transferRequest(s, "GET", "/api/files", "Bearer "+old, nil); r.Code != 401 {
		t.Fatal("revoked token accepted")
	}
}
func TestTransferBinaryRoundTripAndCollision(t *testing.T) {
	s := testTransfer(t)
	auth := "Bearer " + s.token
	data := bytes.Repeat([]byte{0, 255, 17, 32, 128}, 50000)
	for i := 0; i < 2; i++ {
		r := transferRequest(s, "POST", "/api/files/upload?name="+url.QueryEscape("文件.bin"), auth, bytes.NewReader(data))
		if r.Code != 201 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	files, e := s.files()
	if e != nil || len(files) != 2 {
		t.Fatalf("files=%v err=%v", files, e)
	}
	for _, file := range files {
		r := transferRequest(s, "GET", "/api/files/download?name="+url.QueryEscape(file.Name), auth, nil)
		if r.Code != 200 || !bytes.Equal(data, r.Body.Bytes()) {
			t.Fatal("file mismatch", r.Code, file.Name)
		}
	}
	if _, e := os.Stat(filepath.Join(s.folder, "文件 (1).bin")); e != nil {
		t.Fatal(e)
	}
}
func TestTransferRejectsTraversal(t *testing.T) {
	s := testTransfer(t)
	auth := "Bearer " + s.token
	for _, name := range []string{"../secret", "..\\secret", "C:\\secret", "file:stream", "file/child", ".dd-upload-secret", "bad\nname", "trailing. ", "*"} {
		for _, route := range []struct{ method, path string }{{"POST", "/api/files/upload"}, {"GET", "/api/files/download"}} {
			r := transferRequest(s, route.method, route.path+"?name="+url.QueryEscape(name), auth, strings.NewReader("secret"))
			if r.Code != 400 {
				t.Fatalf("%q accepted: %d", name, r.Code)
			}
		}
	}
}
func TestTransferClipboardAndLimits(t *testing.T) {
	s := testTransfer(t)
	auth := "Bearer " + s.token
	body := `{"text":"你好 DD远程\n🔗"}`
	if r := transferRequest(s, "POST", "/api/clipboard", auth, strings.NewReader(body)); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r := transferRequest(s, "GET", "/api/clipboard", auth, nil)
	var got map[string]string
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if got["text"] != "你好 DD远程\n🔗" {
		t.Fatal(got)
	}
	req := httptest.NewRequest("POST", "/api/files/upload?name=large.bin", nil)
	req.ContentLength = maxFileSize + 1
	req.Header.Set("Authorization", auth)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
}
func TestSecretsProtectedAndStable(t *testing.T) {
	dir := t.TempDir()
	s, e := loadSecrets(dir)
	if e != nil {
		t.Fatal(e)
	}
	disk, e := os.ReadFile(filepath.Join(dir, "credentials.dpapi"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(disk, []byte(s.Token)) || bytes.Contains(disk, s.KeyPEM) {
		t.Fatal("secrets stored in plaintext")
	}
	again, e := loadSecrets(dir)
	if e != nil || again.Token != s.Token {
		t.Fatal("identity did not persist", e)
	}
	cert, fp, e := s.certificate()
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(cert.Certificate[0])
	if fp != hex.EncodeToString(hash[:]) {
		t.Fatal("fingerprint mismatch")
	}
}
func TestTransferTLSIdentity(t *testing.T) {
	s := testTransfer(t)
	server := httptest.NewUnstartedServer(s)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
		hash := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if hex.EncodeToString(hash[:]) != s.fingerprint {
			return fmt.Errorf("wrong fingerprint")
		}
		return nil
	}}}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("GET", server.URL+"/api/files", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}
