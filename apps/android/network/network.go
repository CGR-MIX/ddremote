package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type device struct {
	Protocol   int    `json:"protocol"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Ready      bool   `json:"ready"`
	Version    string `json:"version"`
	IP         string `json:"ip"`
	NodeID     string `json:"nodeId"`
	StreamCert string `json:"streamCert"`
}

type status struct {
	State   string   `json:"state"`
	Running bool     `json:"running"`
	AuthURL string   `json:"authUrl"`
	Account string   `json:"account"`
	Devices []device `json:"devices"`
}

type manager struct {
	mu            sync.Mutex
	cancelMu      sync.Mutex // Never held while waiting for mu or performing network I/O.
	sessionCtx    context.Context
	sessionCancel context.CancelFunc
	closeDone     chan struct{}
	closeErr      error
	epoch         uint64
	minimumToken  int64
	sessionToken  int64
	server        *tsnet.Server
	local         accountBackend
	bridge        *clientBridge
	dial          dialFunc // production tsnet dialer; allows protocol tests without real login.
	closeServer   func() error
}

type accountBackend interface {
	Status(context.Context) (*ipnstate.Status, error)
	StartLoginInteractive(context.Context) error
	Logout(context.Context) error
}

var network manager

func main() {}

func callJSON(method, payload string) (result string) {
	return guardedCallJSON(network.call, method, payload)
}

func (m *manager) call(method, payload string) (value any, returnErr error) {
	if method == "networkChanged" {
		if err := updateInterfaces(payload); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	}
	var envelope struct {
		SessionToken int64 `json:"sessionToken"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return nil, err
	}
	if envelope.SessionToken <= 0 {
		return nil, errors.New("账号会话无效")
	}
	if method == "logout" {
		m.cancelMu.Lock()
		if envelope.SessionToken > m.minimumToken {
			m.minimumToken = envelope.SessionToken
		}
		m.cancelMu.Unlock()
		return m.logout(12 * time.Second)
	}
	timeout := 20 * time.Second
	if method == "request" {
		timeout = 75 * time.Second
	}
	m.cancelMu.Lock()
	closing := m.closeDone != nil
	parent := m.sessionCtx
	epoch := m.epoch
	m.cancelMu.Unlock()
	if closing {
		return nil, errors.New("账号正在退出，请稍后重新登录")
	}
	if parent == nil {
		if method != "start" {
			return nil, errors.New("安全网络尚未初始化")
		}
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Logout cancels this context before waiting for mu, so long HTTP requests
	// and concurrent discovery stop instead of delaying logout for 75 seconds.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelMu.Lock()
	stale := epoch != m.epoch || m.closeDone != nil || envelope.SessionToken <= m.minimumToken || (method != "start" && envelope.SessionToken != m.sessionToken)
	m.cancelMu.Unlock()
	if stale {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if method != "start" && ctx.Err() != nil {
			value = nil
			returnErr = ctx.Err()
		}
	}()
	if method == "start" {
		if m.server != nil {
			if envelope.SessionToken != m.sessionToken {
				return nil, errors.New("账号会话已结束")
			}
			return map[string]bool{"ok": true}, nil
		}
		if err := updateInterfaces(payload); err != nil {
			return nil, err
		}
		installInterfaceHook()
		var input struct {
			Dir      string `json:"dir"`
			Hostname string `json:"hostname"`
			CABundle string `json:"caBundle"`
		}
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(input.Dir) {
			return nil, errors.New("网络身份目录无效")
		}
		if err := os.MkdirAll(input.Dir, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(input.Dir, 0700); err != nil {
			return nil, err
		}
		if err := prepareNetworkLogDirectory(input.Dir); err != nil {
			return nil, fmt.Errorf("准备网络日志目录: %w", err)
		}
		if input.CABundle != "" {
			if !filepath.IsAbs(input.CABundle) {
				return nil, errors.New("系统信任证书路径无效")
			}
			if err := os.Setenv("SSL_CERT_FILE", input.CABundle); err != nil {
				return nil, err
			}
		}
		s := &tsnet.Server{Dir: input.Dir, Hostname: input.Hostname, Ephemeral: true, Store: new(mem.Store), Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
		if err := s.Start(); err != nil {
			s.Close()
			return nil, fmt.Errorf("初始化安全网络: %w", err)
		}
		lc, err := s.LocalClient()
		if err != nil {
			s.Close()
			return nil, err
		}
		m.cancelMu.Lock()
		if m.closeDone != nil || m.epoch != epoch || envelope.SessionToken <= m.minimumToken {
			m.cancelMu.Unlock()
			s.Close()
			return nil, context.Canceled
		}
		m.sessionCtx, m.sessionCancel = context.WithCancel(context.Background())
		m.sessionToken = envelope.SessionToken
		m.cancelMu.Unlock()
		m.server, m.local, m.dial, m.closeServer = s, lc, s.Dial, s.Close
		activeMonitor.Store(s.Sys().NetMon.Get())
		return map[string]bool{"ok": true}, nil
	}
	if m.local == nil {
		return nil, errors.New("安全网络尚未初始化")
	}
	switch method {
	case "login":
		if err := m.local.StartLoginInteractive(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "status":
		return m.status(ctx, false)
	case "discover":
		return m.status(ctx, true)
	case "prepare":
		var input struct {
			IP string `json:"ip"`
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			return nil, err
		}
		st, err := m.local.Status(ctx)
		if err != nil {
			return nil, err
		}
		peer := ownedPeer(st, input.IP)
		if peer == nil || input.ID == "" || input.ID != string(peer.ID) {
			return nil, errors.New("该电脑不属于当前登录账号")
		}
		dev, err := m.probe(ctx, input.IP)
		if err != nil || dev.ID != input.ID || dev.ID != string(peer.ID) || !dev.Ready {
			return nil, errors.New("电脑暂时不可连接，请在电脑开启桌面共享")
		}
		if m.bridge == nil || m.bridge.remote != input.IP {
			m.closeBridge()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			m.bridge, err = startClientBridge(parent, input.IP, m.dial)
			if err != nil {
				return nil, err
			}
		}
		return map[string]string{"host": clientHost, "streamCert": dev.StreamCert}, nil
	case "request":
		var input struct {
			IP   string `json:"ip"`
			Path string `json:"path"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			return nil, err
		}
		st, err := m.local.Status(ctx)
		if err != nil {
			return nil, err
		}
		if !ownedIP(st, input.IP) {
			return nil, errors.New("该电脑不属于当前登录账号")
		}
		// Only the DD account protocol is reachable through this entry point.
		if !strings.HasPrefix(input.Path, "/v1/") || strings.ContainsAny(input.Path, "?#\\") {
			return nil, errors.New("请求路径无效")
		}
		method := "GET"
		if input.Body != "" {
			method = "POST"
		}
		raw, err := m.request(ctx, input.IP, method, input.Path, input.Body)
		if err != nil {
			return nil, err
		}
		return map[string]string{"body": string(raw)}, nil
	case "closeClient":
		m.closeBridge()
		return map[string]bool{"ok": true}, nil
	default:
		return nil, errors.New("未知网络操作")
	}
}

func (m *manager) closeBridge() {
	if m.bridge != nil {
		m.bridge.Close()
		m.bridge = nil
	}
}

// Cancellation is independent from the operation lock. The public call has a
// bounded wait; new starts remain denied until background cleanup really ends.
func (m *manager) logout(timeout time.Duration) (any, error) {
	m.cancelMu.Lock()
	done := m.closeDone
	if done == nil {
		m.epoch++
		if m.sessionToken > m.minimumToken {
			m.minimumToken = m.sessionToken
		}
		done = make(chan struct{})
		m.closeDone = done
		if m.sessionCancel != nil {
			m.sessionCancel()
		}
		m.sessionCtx, m.sessionCancel = nil, nil
		go func() {
			m.mu.Lock()
			m.closeBridge()
			var logoutErr, closeErr error
			if m.local != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				logoutErr = m.local.Logout(ctx)
				cancel()
			}
			activeMonitor.Store(nil)
			if m.closeServer != nil {
				closeErr = m.closeServer()
			}
			m.server, m.local, m.dial, m.closeServer = nil, nil, nil, nil
			m.mu.Unlock()
			m.cancelMu.Lock()
			m.closeErr = nil
			if logoutErr != nil {
				m.closeErr = errors.New("已断开连接；未确认云端移除，将待离线清理")
			} else if closeErr != nil {
				m.closeErr = errors.New("账号已注销，本地网络关闭时遇到错误")
			}
			close(done)
			m.closeDone = nil
			m.cancelMu.Unlock()
		}()
	}
	m.cancelMu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		m.cancelMu.Lock()
		err := m.closeErr
		m.cancelMu.Unlock()
		if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case <-timer.C:
		return nil, errors.New("已取消连接；未确认云端移除，将待离线清理。本地网络正在关闭")
	}
}

func owner(st *ipnstate.Status) tailcfg.UserID {
	if st == nil || st.BackendState != "Running" || st.Self == nil || st.Self.UserID == 0 || st.Self.ShareeNode || st.Self.AltSharerUserID != 0 || st.Self.Tags != nil && st.Self.Tags.Len() > 0 {
		return 0
	}
	return st.Self.UserID
}

func ownedPeers(st *ipnstate.Status) []*ipnstate.PeerStatus {
	id := owner(st)
	if id == 0 {
		return nil
	}
	var peers []*ipnstate.PeerStatus
	for _, peer := range st.Peer {
		if peer != nil && peer.UserID == id && !peer.ShareeNode && peer.AltSharerUserID == 0 && (peer.Tags == nil || peer.Tags.Len() == 0) {
			peers = append(peers, peer)
		}
	}
	return peers
}

func ownedIP(st *ipnstate.Status, address string) bool {
	return ownedPeer(st, address) != nil
}

func ownedPeer(st *ipnstate.Status, address string) *ipnstate.PeerStatus {
	for _, peer := range ownedPeers(st) {
		for _, ip := range peer.TailscaleIPs {
			if ip.String() == address {
				return peer
			}
		}
	}
	return nil
}

func (m *manager) status(ctx context.Context, discover bool) (*status, error) {
	st, err := m.local.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := &status{State: st.BackendState, AuthURL: st.AuthURL, Devices: []device{}}
	out.Running = owner(st) != 0
	if !out.Running {
		return out, nil
	}
	out.AuthURL = ""
	profile := st.User[owner(st)]
	out.Account = profile.LoginName
	if out.Account == "" {
		out.Account = profile.DisplayName
	}
	if !discover {
		return out, nil
	}
	var wg sync.WaitGroup
	var results sync.Mutex
	limit := make(chan struct{}, 8)
	for _, peer := range ownedPeers(st) {
		if !peer.Online || len(peer.TailscaleIPs) == 0 {
			continue
		}
		ip := peer.TailscaleIPs[0]
		for _, candidate := range peer.TailscaleIPs {
			if candidate.Is4() {
				ip = candidate
				break
			}
		}
		wg.Add(1)
		go func(peer *ipnstate.PeerStatus, address string) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-limit }()
			probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			dev, err := m.probe(probeCtx, address)
			if err != nil || peer.ID == "" || dev.ID != string(peer.ID) {
				return
			}
			dev.IP, dev.NodeID = address, string(peer.ID)
			results.Lock()
			out.Devices = append(out.Devices, *dev)
			results.Unlock()
		}(peer, ip.String())
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].Name < out.Devices[j].Name })
	return out, nil
}

func (m *manager) probe(ctx context.Context, ip string) (*device, error) {
	data, err := m.request(ctx, ip, "GET", "/v1/device", "")
	if err != nil {
		return nil, err
	}
	var dev device
	if err := json.Unmarshal(data, &dev); err != nil {
		return nil, err
	}
	if dev.Protocol != 1 || dev.ID == "" || dev.Platform != "windows" {
		return nil, errors.New("不兼容的 DD 设备")
	}
	return &dev, nil
}

func (m *manager) request(ctx context.Context, ip, method, path, body string) ([]byte, error) {
	transport := &http.Transport{DialContext: m.dial, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("拒绝跨设备重定向") }}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+net.JoinHostPort(ip, "48201")+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("电脑返回内容超出限制")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("电脑返回错误 (%d)", res.StatusCode)
	}
	return data, nil
}
