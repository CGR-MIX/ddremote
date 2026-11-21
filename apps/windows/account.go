package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type AccountDevice struct {
	Protocol   int    `json:"protocol"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Online     bool   `json:"online"`
	Ready      bool   `json:"ready"`
	Platform   string `json:"platform"`
	Version    string `json:"version"`
	StreamCert string `json:"streamCert,omitempty"`
	Error      string `json:"error,omitempty"`
}

type accountCache struct {
	Owner   string                   `json:"owner"`
	Devices map[string]AccountDevice `json:"devices"`
}

type AccountManager struct {
	app         *App
	mu          sync.Mutex
	discoveryMu sync.Mutex
	cache       accountCache
	pairGate    chan struct{}
	usedNonces  map[string]time.Time
}

func newAccountManager(a *App) *AccountManager {
	m := &AccountManager{app: a, pairGate: make(chan struct{}, 1), usedNonces: map[string]time.Time{}}
	_ = os.Remove(filepath.Join(a.dir, "account-devices.json"))
	return m
}

func (m *AccountManager) Clear() {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	m.mu.Lock()
	m.cache = accountCache{}
	m.mu.Unlock()
}

func accountJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func accountError(w http.ResponseWriter, status int, message string) {
	accountJSON(w, status, map[string]string{"error": message})
}

// Only NetworkManager's identity-checked tsnet listener may install this handler.
func (m *AccountManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := NetworkAccountPeer(r.Context()); !ok {
		accountError(w, 403, "需要同账号身份")
		return
	}
	if r.Header.Get("Origin") != "" {
		accountError(w, 403, "不接受浏览器跨域请求")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/device":
		status := m.app.network.Status(r.Context())
		host, _ := m.app.host.Status(r.Context())
		m.app.mu.Lock()
		name := m.app.settings.DeviceName
		m.app.mu.Unlock()
		cert, _ := os.ReadFile(filepath.Join(m.app.nativeDir, "sunshine", "cert.pem"))
		block, _ := pem.Decode(cert)
		certOK := false
		if block != nil {
			_, err := x509.ParseCertificate(block.Bytes)
			certOK = err == nil
		}
		accountJSON(w, 200, AccountDevice{Protocol: 1, ID: status.SelfID, Name: name, Online: true, Ready: host.Ready && status.Receiving && certOK, Platform: "windows", Version: Version, StreamCert: string(cert)})
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && r.URL.Path == "/v1/super-screen":
		m.superScreen(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && r.URL.Path == "/v1/privacy":
		m.privacy(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/pair":
		m.pair(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/companion":
		status := m.app.network.Status(r.Context())
		if !status.Receiving || !m.app.transfer.Status().Running {
			accountError(w, 409, "本机尚未开启共享")
			return
		}
		host := status.IP
		if host == "" {
			host = status.IPv6
		}
		m.app.mu.Lock()
		token := m.app.secrets.Token
		m.app.mu.Unlock()
		u := url.URL{Scheme: "ddremote", Host: "connect", RawQuery: url.Values{"host": {host}, "port": {"48200"}, "token": {token}, "fingerprint": {m.app.transfer.fingerprint}}.Encode()}
		accountJSON(w, 200, map[string]string{"url": u.String(), "link": u.String()})
	default:
		accountError(w, 404, "接口不存在")
	}
}

var accountNoncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (m *AccountManager) pair(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Nonce string `json:"nonce"`
		Pin   string `json:"pin"`
		Name  string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || !accountNoncePattern.MatchString(p.Nonce) || !pinPattern.MatchString(p.Pin) || len(p.Name) > 128 || strings.TrimSpace(p.Name) == "" || strings.ContainsAny(p.Name, "\r\n\x00") {
		accountError(w, 400, "配对参数无效")
		return
	}
	if !m.app.network.Status(r.Context()).Receiving {
		accountError(w, 409, "被控电脑尚未开启共享")
		return
	}
	select {
	case m.pairGate <- struct{}{}:
		defer func() { <-m.pairGate }()
	default:
		accountError(w, 409, "正在处理另一个配对，请稍后重试")
		return
	}
	m.mu.Lock()
	now := time.Now()
	for nonce, expiry := range m.usedNonces {
		if now.After(expiry) {
			delete(m.usedNonces, nonce)
		}
	}
	_, used := m.usedNonces[p.Nonce]
	if !used {
		m.usedNonces[p.Nonce] = now.Add(5 * time.Minute)
	}
	m.mu.Unlock()
	if used {
		accountError(w, 409, "配对请求已使用，请重新连接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	err := m.awaitPair(ctx, p.Nonce, p.Pin, p.Name)
	if err != nil {
		accountError(w, 409, err.Error())
		return
	}
	accountJSON(w, 200, map[string]bool{"paired": true})
}

func (m *AccountManager) awaitPair(ctx context.Context, nonce, pin, name string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !m.app.network.Status(ctx).Receiving {
			return errors.New("被控电脑已停止共享")
		}
		status, pending := m.app.host.Status(ctx)
		if !status.Running {
			return errors.New("被控电脑已停止共享")
		}
		for _, p := range pending {
			if p.Name == "DD-"+nonce {
				if !m.app.network.Status(ctx).Receiving {
					return errors.New("被控电脑已停止共享")
				}
				return m.app.host.Pair(ctx, p.ID, pin, name)
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("自动配对超时，请重试连接")
		case <-ticker.C:
		}
	}
}

func accountHTTPClient(dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: dial, Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: timeout}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("账号服务不允许跳转") }}
}

func accountRequest(ctx context.Context, client *http.Client, peer, method, path string, body, out any) error {
	var input io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return e
		}
		input = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://"+net.JoinHostPort(peer, "48201")+path, input)
	if e != nil {
		return e
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, e := client.Do(req)
	if e != nil {
		return errors.New("无法连接该电脑的 DD账号服务")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&msg)
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		return fmt.Errorf("账号服务返回 %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(out)
	}
	return nil
}

func (m *AccountManager) Devices(ctx context.Context) (any, error) {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	st := m.app.network.Status(ctx)
	devices := []AccountDevice{}
	if !st.Running || st.Account.ID == "" {
		return map[string]any{"account": st.Account, "state": st.State, "devices": devices}, nil
	}
	m.mu.Lock()
	if m.cache.Owner != st.Account.ID || m.cache.Devices == nil {
		m.cache = accountCache{Owner: st.Account.ID, Devices: map[string]AccountDevice{}}
	}
	cached := make(map[string]AccountDevice, len(m.cache.Devices))
	for id, d := range m.cache.Devices {
		cached[id] = d
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	client := accountHTTPClient(m.app.network.DialContext, 2500*time.Millisecond)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	sem := make(chan struct{}, 8)
	for _, peer := range st.Peers {
		if peer.ID == "" {
			continue
		}
		wg.Add(1)
		go func(peer NetworkPeer) {
			defer wg.Done()
			d, known := cached[peer.ID]
			d.ID = peer.ID
			d.IP = peer.IP
			d.Online = peer.Online
			d.Ready = false
			d.StreamCert = ""
			d.Error = ""
			if peer.Online {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				var remote AccountDevice
				if e := accountRequest(ctx, client, peer.IP, "GET", "/v1/device", nil, &remote); e == nil && validAccountDevice(remote, peer.ID) {
					d = remote
					d.IP = peer.IP
					d.Online = true
					known = true
				} else if known {
					d.Error = "DD远程暂时不可达"
				}
			}
			if known {
				resultMu.Lock()
				devices = append(devices, d)
				resultMu.Unlock()
			}
		}(peer)
	}
	wg.Wait()
	// Discard a result if logout/account switching happened during discovery.
	current := m.app.network.Status(ctx)
	if current.Account.ID != st.Account.ID || !current.Running {
		return map[string]any{"account": current.Account, "state": current.State, "devices": []AccountDevice{}}, nil
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Ready != devices[j].Ready {
			return devices[i].Ready
		}
		return devices[i].Name < devices[j].Name
	})
	m.mu.Lock()
	for _, d := range devices {
		d.StreamCert = ""
		d.Ready = false
		d.Online = false
		d.IP = ""
		d.Error = ""
		m.cache.Devices[d.ID] = d
	}
	m.mu.Unlock()
	return map[string]any{"account": st.Account, "state": st.State, "devices": devices}, nil
}

func validAccountDevice(d AccountDevice, id string) bool {
	return d.Protocol == 1 && d.ID == id && d.Name != "" && len(d.Name) <= 128 && d.Platform == "windows"
}

func (m *AccountManager) Connect(ctx context.Context, id string) (any, error) {
	a := m.app
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.clientRunning {
		return nil, errors.New("远程窗口已经打开，请先结束当前控制")
	}
	st := a.network.Status(ctx)
	var peer NetworkPeer
	for _, p := range st.Peers {
		if p.ID == id {
			peer = p
			break
		}
	}
	if !st.Running || st.Account.ID == "" || peer.ID == "" || !peer.Online {
		return nil, errors.New("该账号设备当前离线，请刷新设备列表")
	}
	client := accountHTTPClient(a.network.DialContext, 70*time.Second)
	var device AccountDevice
	checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	e := accountRequest(checkCtx, client, peer.IP, "GET", "/v1/device", nil, &device)
	cancel()
	if e != nil {
		return nil, e
	}
	if !validAccountDevice(device, peer.ID) || !device.Ready {
		return nil, errors.New("该电脑尚未开启 DD远程共享")
	}
	exe, e := extractPayload(a.nativeDir, "moonlight", "Moonlight.exe")
	if e != nil {
		return nil, e
	}
	host, e := a.network.PrepareClient(ctx, peer.IP)
	if e != nil {
		return nil, e
	}
	started := false
	defer func() {
		if !started {
			a.network.CloseClient()
		}
	}()
	profile, e := a.ensureClientProfile()
	if e != nil {
		return nil, e
	}
	if !moonlightPaired(ctx, profile, host, device.StreamCert) {
		nonceBytes := make([]byte, 16)
		if _, e = rand.Read(nonceBytes); e != nil {
			return nil, e
		}
		nonce := hex.EncodeToString(nonceBytes)
		pinInt, e := rand.Int(rand.Reader, big.NewInt(10000))
		if e != nil {
			return nil, e
		}
		pin := fmt.Sprintf("%04d", pinInt.Int64())
		if e = a.network.SetClientPairTag(nonce); e != nil {
			return nil, e
		}
		a.logf("正在为同账号设备完成首次配对")
		pairCtx, pairCancel := context.WithTimeout(ctx, 75*time.Second)
		e = runAccountPair(pairCtx, exe, profile, host, device.StreamCert, pin, func(ctx context.Context) error {
			var result struct {
				Paired bool `json:"paired"`
			}
			err := accountRequest(ctx, client, peer.IP, "POST", "/v1/pair", map[string]string{"nonce": nonce, "pin": pin, "name": "DD Windows"}, &result)
			if err == nil && !result.Paired {
				err = errors.New("远端未确认配对结果")
			}
			return err
		})
		pairCancel()
		_ = a.network.SetClientPairTag("")
		if e != nil {
			return nil, e
		}
	}
	cmd := hiddenCommand(exe, "stream", host, "Desktop", "--absolute-mouse", "--display-mode", "windowed", "--packet-size", "1024")
	if ctx.Err() != nil {
		return nil, errors.New("连接已取消")
	}
	cmd.Dir = profile
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	a.watchClient(cmd)
	started = true
	a.logf("已打开同账号远程桌面")
	return map[string]string{"message": "已打开远程桌面"}, nil
}
