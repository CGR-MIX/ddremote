package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func moonlightSettingsPath(dir string) string {
	return filepath.Join(dir, "Moonlight Game Streaming Project", "Moonlight.ini")
}

// Moonlight portable mode persists PEM credentials as QSettings QByteArray.
// This reads only DD's private portable settings, never another installation.
func moonlightIdentity(dir string) (tls.Certificate, string, []byte, error) {
	data, e := os.ReadFile(moonlightSettingsPath(dir))
	if e != nil {
		return tls.Certificate{}, "", nil, e
	}
	values := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[General]" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = decodeQtValue(v)
		}
	}
	cert, e := tls.X509KeyPair([]byte(values["certificate"]), []byte(values["key"]))
	if e != nil {
		return tls.Certificate{}, "", nil, e
	}
	if values["uniqueid"] == "" {
		return tls.Certificate{}, "", nil, errors.New("串流身份尚未创建")
	}
	return cert, values["uniqueid"], data, nil
}

func decodeQtValue(v string) string {
	if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
		v = v[1 : len(v)-1]
	}
	if strings.HasPrefix(v, "@ByteArray(") && strings.HasSuffix(v, ")") {
		v = v[len("@ByteArray(") : len(v)-1]
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 == len(v) {
			b.WriteByte(v[i])
			continue
		}
		i++
		switch v[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

func moonlightPaired(ctx context.Context, dir, host, serverPEM string) bool {
	identity, uid, settings, e := moonlightIdentity(dir)
	if e != nil {
		return false
	}
	block, _ := pem.Decode([]byte(serverPEM))
	if block == nil {
		return false
	}
	expected, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return false
	}
	// Require the actual host pin in the persisted host list before starting a new process.
	pinned := false
	for _, line := range strings.Split(string(settings), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.HasSuffix(key, "\\srvcert") {
			b, _ := pem.Decode([]byte(decodeQtValue(value)))
			if b != nil && sha256.Sum256(b.Bytes) == sha256.Sum256(expected.Raw) {
				pinned = true
				break
			}
		}
	}
	if !pinned {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{identity}, InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || sha256.Sum256(cs.PeerCertificates[0].Raw) != sha256.Sum256(expected.Raw) {
			return errors.New("串流主机证书与账号设备不符")
		}
		return nil
	}}}
	defer tr.CloseIdleConnections()
	req, e := http.NewRequestWithContext(cctx, "GET", "https://"+net.JoinHostPort(host, "47984")+"/serverinfo?uniqueid="+url.QueryEscape(uid), nil)
	if e != nil {
		return false
	}
	res, e := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("unexpected redirect") }}).Do(req)
	if e != nil {
		return false
	}
	defer res.Body.Close()
	var status struct {
		Pair int `xml:"PairStatus"`
		Code int `xml:"status_code,attr"`
	}
	return res.StatusCode == 200 && xml.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&status) == nil && status.Pair == 1 && status.Code == 200
}

func runAccountPair(ctx context.Context, exe, profile, host, serverPEM, pin string, approve func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := hiddenCommand(exe, "pair", host, "--pin", pin)
	cmd.Dir = profile
	if e := cmd.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		select {
		case <-done:
			return
		default:
		}
		closeOwnedWindows(uint32(cmd.Process.Pid))
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	// Approval waits for the unpredictable DD nonce injected by the client proxy.
	// The PIN alone is never used to choose a pending request.
	result := make(chan error, 1)
	go func() { result <- approve(ctx) }()
	select {
	case e := <-result:
		if e != nil {
			return e
		}
	case e := <-done:
		// Preserve the completion signal for deferred cleanup.
		done <- e
		return errors.New("串流配对进程已退出，请重试")
	case <-ctx.Done():
		return errors.New("自动配对超时，请重试")
	}
	// Qt writes settings on its own worker thread. Verify both the persisted pin
	// and a real mutual-TLS serverinfo response before closing its pairing UI.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if moonlightPaired(ctx, profile, host, serverPEM) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("配对已应答，但串流身份未保存，请重试")
		case e := <-done:
			done <- e
			return errors.New("串流配对窗口已关闭")
		case <-ticker.C:
		}
	}
}

func closeOwnedWindows(pid uint32) {
	callback := windows.NewCallback(func(hwnd, unused uintptr) uintptr {
		var owner uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner == pid {
			user32.NewProc("PostMessageW").Call(hwnd, 0x0010, 0, 0)
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(callback, 0)
}
