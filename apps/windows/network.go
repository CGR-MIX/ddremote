package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

// NetworkPeer is an untagged peer owned by the same signed-in user.
// Online means connected to its coordination service, not that DD is running.
type NetworkPeer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Online bool   `json:"online"`
}

type NetworkAccount struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

type NetworkStatus struct {
	Running   bool           `json:"running"`
	Receiving bool           `json:"receiving"`
	State     string         `json:"state"`
	IP        string         `json:"ip"`
	IPv6      string         `json:"ipv6"`
	AuthURL   string         `json:"authUrl"`
	Error     string         `json:"error"`
	Peers     []NetworkPeer  `json:"peers"`
	Account   NetworkAccount `json:"account"`
	SelfID    string         `json:"selfID"`
	Temporary bool           `json:"temporary"`
}

// NetworkManager owns an independent userspace Tailscale node. It never
// changes the system Tailscale installation or exposes Sunshine's admin port.
type NetworkManager struct {
	opMu           sync.Mutex
	clientMu       sync.Mutex
	receiveMu      sync.Mutex
	mu             sync.RWMutex
	stateDir       string
	logf           func(string, ...any)
	run            *networkRun
	client         *networkClientBridge
	status         NetworkStatus
	receiving      bool
	accountHandler http.Handler
	temporary      bool
}

type networkRun struct {
	ctx              context.Context
	cancel           context.CancelFunc
	ready            chan struct{}
	done             chan struct{}
	server           *tsnet.Server
	client           *local.Client // published by closing ready
	initErr          error         // published by closing ready
	closeErr         error         // published by closing done
	hostname         string
	receive          chan networkReceiveRequest
	receiveDone      chan struct{}
	initialReceiving bool
	temporary        bool
	temporaryDir     string
	logout           func(context.Context) error // published by closing ready
}

type networkReceiveRequest struct {
	setEnabled bool
	value      bool
	done       chan error
}

func NewNetworkManager(stateDir string, logf func(string, ...any)) *NetworkManager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &NetworkManager{
		stateDir:  stateDir,
		logf:      logf,
		temporary: true,
		status:    NetworkStatus{State: "Stopped", Temporary: true, Peers: []NetworkPeer{}},
	}
}

// Ephemeral identities are the default: successful Logout removes the node
// through Tailscale instead of merely expiring a persistent device record.
func (m *NetworkManager) IsTemporary() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.temporary
}

// SetEphemeral is an internal configuration hook. Changing mode requires a
// stopped network, so an existing persistent identity can never be converted.
func (m *NetworkManager) SetEphemeral(enabled bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.run; r != nil && enabled != m.temporary {
		select {
		case <-r.done:
		default:
			return errors.New("请先退出账号，再更改网络身份模式")
		}
	}
	m.temporary = enabled
	m.status.Temporary = enabled
	return nil
}

// Start begins initialization in the background and returns without waiting
// for interactive login or coordination-server connectivity.
func (m *NetworkManager) Start(ctx context.Context, hostname string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "ddremote"
	}
	m.mu.Lock()
	if old := m.run; old != nil {
		select {
		case <-old.done:
			m.run = nil
		default:
			m.mu.Unlock()
			if old.hostname != hostname {
				return errors.New("请先停止安全网络，再更改设备名称")
			}
			return nil
		}
	}
	temporary := m.temporary
	m.mu.Unlock()
	server, temporaryDir, err := newNetworkServer(m.stateDir, hostname, temporary)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	r := &networkRun{
		ctx: runCtx, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), hostname: hostname,
		server: server, temporary: temporary, temporaryDir: temporaryDir,
	}
	m.mu.Lock()
	r.receive = make(chan networkReceiveRequest)
	r.receiveDone = make(chan struct{})
	r.initialReceiving = m.receiving
	m.run = r
	m.status = NetworkStatus{State: "Starting", Temporary: temporary, Peers: []NetworkPeer{}}
	m.mu.Unlock()
	go m.runNetwork(r)
	return nil
}

func newNetworkServer(stateDir, hostname string, temporary bool) (*tsnet.Server, string, error) {
	var dir string
	var err error
	if temporary {
		// The state Store below is entirely in memory. tsnet still requires a
		// directory for diagnostic metadata; isolate it from saved identities.
		dir, err = os.MkdirTemp("", "ddremote-tsnet-")
	} else {
		if strings.TrimSpace(stateDir) == "" {
			return nil, "", errors.New("安全网络状态目录不能为空")
		}
		dir, err = filepath.Abs(stateDir)
		if err == nil {
			err = os.MkdirAll(dir, 0700)
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("创建安全网络状态目录: %w", err)
	}
	temporaryDir := ""
	if temporary {
		temporaryDir = dir
	}
	if err = protectNetworkStateDirectory(dir); err != nil {
		if temporary {
			_ = removeTemporaryNetworkDirectory(dir)
		}
		return nil, "", fmt.Errorf("保护安全网络状态目录: %w", err)
	}
	server := &tsnet.Server{
		Dir: dir, Hostname: hostname, Ephemeral: temporary,
		// Authorization URLs are displayed only through local UI state.
		UserLogf: func(string, ...any) {}, Logf: func(string, ...any) {},
	}
	if temporary {
		server.Store = new(mem.Store)
	}
	return server, temporaryDir, nil
}

func removeTemporaryNetworkDirectory(dir string) error {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	base, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(abs), base) || !strings.HasPrefix(filepath.Base(abs), "ddremote-tsnet-") {
		return errors.New("拒绝清理非DD网络临时目录")
	}
	return os.RemoveAll(abs)
}

func protectNetworkStateDirectory(dir string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Windows ignores POSIX chmod's group/other bits. Apply a protected DACL
	// inherited by newly created state files: this user and SYSTEM only.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func (m *NetworkManager) runNetwork(r *networkRun) {
	var accountServer *http.Server
	go m.runReceiving(r, func(ctx context.Context) (*networkForwards, error) { return startNetworkForwards(ctx, r.server.Listen) })
	defer func() {
		r.cancel()
		if accountServer != nil {
			_ = accountServer.Close()
		}
		m.closeClientForRun(r)
		<-r.receiveDone
		// Close must only run after Start has returned, including failures.
		r.closeErr = r.server.Close()
		if r.temporaryDir != "" {
			r.closeErr = errors.Join(r.closeErr, removeTemporaryNetworkDirectory(r.temporaryDir))
		}
		m.mu.Lock()
		if m.run == r {
			m.status.Running = false
			m.status.Receiving = false
			m.status.AuthURL = ""
			m.status.IP, m.status.IPv6 = "", ""
			m.status.Peers = []NetworkPeer{}
			m.status.Account = NetworkAccount{}
			m.status.SelfID = ""
			if r.ctx.Err() != nil {
				m.status.State = "Stopped"
				m.status.Error = ""
			}
		}
		m.mu.Unlock()
		close(r.done)
	}()
	r.initErr = r.server.Start()
	if r.initErr == nil {
		r.client, r.initErr = r.server.LocalClient()
		if r.initErr == nil {
			r.logout = r.client.Logout
		}
	}
	close(r.ready)
	if r.initErr != nil {
		m.setNetworkError(r, "安全网络初始化失败", r.initErr)
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if r.ctx.Err() != nil {
			return
		}
		queryCtx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
		st, err := r.client.Status(queryCtx)
		cancel()
		if err != nil {
			if accountServer != nil {
				_ = accountServer.Close()
				accountServer = nil
			}
			_ = r.changeReceiving(false, false)
			m.setNetworkError(r, "读取安全网络状态失败", err)
		} else {
			next := networkStatusFromBackend(st)
			next.Temporary = r.temporary
			m.mu.RLock()
			hasAccountHandler := m.accountHandler != nil
			m.mu.RUnlock()
			if next.Running && next.Account.ID != "" && hasAccountHandler {
				if accountServer == nil {
					ln, listenErr := r.server.Listen("tcp", ":48201")
					if listenErr != nil {
						next.Error = "创建账号设备通道失败: " + redactNetworkError(listenErr)
					} else {
						accountServer = newNetworkAccountServer(r.ctx, m.networkAccountHandler(r, r.client))
						go func(server *http.Server) { _ = server.Serve(ln) }(accountServer)
					}
				}
			} else if accountServer != nil {
				_ = accountServer.Close()
				accountServer = nil
			}
			if err = r.changeReceiving(false, next.Running); err != nil && r.ctx.Err() == nil {
				next.Error = "创建串流通道失败: " + redactNetworkError(err)
			}
			m.mu.Lock()
			if m.run == r {
				next.Receiving = m.status.Receiving
				m.status = next
			}
			m.mu.Unlock()
		}
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SetReceiving explicitly authorizes forwarding to DD's own host. The default
// is false: signing into the tailnet never exposes an unrelated local server.
// On return from SetReceiving(false), all incoming listeners AND active flows
// are closed. Outgoing client bridges are deliberately unaffected.
func (m *NetworkManager) SetReceiving(enabled bool) {
	m.receiveMu.Lock()
	defer m.receiveMu.Unlock()
	m.mu.Lock()
	m.receiving = enabled
	r := m.run
	m.mu.Unlock()
	if r != nil {
		_ = r.changeReceiving(true, enabled)
	}
}

func (r *networkRun) changeReceiving(setEnabled, value bool) error {
	request := networkReceiveRequest{setEnabled: setEnabled, value: value, done: make(chan error, 1)}
	select {
	case r.receive <- request:
	case <-r.receiveDone:
		return net.ErrClosed
	}
	select {
	case err := <-request.done:
		return err
	case <-r.receiveDone:
		return net.ErrClosed
	}
}

// This goroutine is the sole owner of incoming proxy lifetime. A synchronous
// channel acknowledgement prevents a poll racing with SetReceiving(false)
// from recreating listeners after that method has returned.
func (m *NetworkManager) runReceiving(r *networkRun, start func(context.Context) (*networkForwards, error)) {
	var forwards *networkForwards
	enabled, online := r.initialReceiving, false
	defer func() {
		if forwards != nil {
			forwards.Close()
		}
		m.mu.Lock()
		if m.run == r {
			m.status.Receiving = false
		}
		m.mu.Unlock()
		close(r.receiveDone)
	}()
	for {
		select {
		case <-r.ctx.Done():
			return
		case request := <-r.receive:
			if request.setEnabled {
				enabled = request.value
			} else {
				online = request.value
			}
			var err error
			if enabled && online && r.ctx.Err() == nil {
				if forwards == nil {
					forwards, err = start(r.ctx)
				}
			} else if forwards != nil {
				forwards.Close()
				forwards = nil
			}
			m.mu.Lock()
			if m.run == r {
				m.status.Receiving = forwards != nil
				if err != nil {
					m.status.Error = "创建串流通道失败: " + redactNetworkError(err)
				}
			}
			m.mu.Unlock()
			request.done <- err
		}
	}
}

// Status returns the latest background snapshot, refreshed at most every two
// seconds. Authorization URLs are returned only to the caller, never logged.
func (m *NetworkManager) Status(ctx context.Context) NetworkStatus {
	m.mu.RLock()
	st := m.status
	st.Peers = append([]NetworkPeer{}, st.Peers...)
	m.mu.RUnlock()
	return st
}

// Login initiates an interactive login and waits briefly for its URL. The
// caller may continue reading Status if the coordination server is slow.
func (m *NetworkManager) Login(ctx context.Context) (string, error) {
	m.mu.RLock()
	r := m.run
	m.mu.RUnlock()
	if r == nil {
		return "", errors.New("请先启动安全网络")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case <-r.ready:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-r.done:
		return "", errors.New("安全网络已停止")
	}
	if r.initErr != nil {
		return "", fmt.Errorf("安全网络未就绪: %s", redactNetworkError(r.initErr))
	}
	if r.ctx.Err() != nil {
		return "", errors.New("安全网络已停止")
	}
	if st, err := r.client.Status(ctx); err == nil {
		if st.BackendState == "Running" {
			return "", nil
		}
		if st.AuthURL != "" {
			return st.AuthURL, nil
		}
	}
	if err := r.client.StartLoginInteractive(ctx); err != nil {
		return "", fmt.Errorf("无法打开网络登录: %s", redactNetworkError(err))
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		st, err := r.client.Status(ctx)
		if err == nil {
			if st.AuthURL != "" {
				m.mu.Lock()
				if m.run == r {
					m.status.AuthURL = st.AuthURL
				}
				m.mu.Unlock()
				return st.AuthURL, nil
			}
			if st.BackendState == "Running" {
				return "", nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("登录地址尚未就绪，请稍后重试: %w", ctx.Err())
		case <-r.ctx.Done():
			return "", errors.New("安全网络已停止")
		case <-ticker.C:
		}
	}
}

func (m *NetworkManager) Stop() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	r := m.run
	m.mu.RUnlock()
	if r == nil {
		return nil
	}
	m.SetReceiving(false)
	m.CloseClient()
	var logoutErr error
	if r.temporary {
		logoutErr = logoutNetworkRun(context.Background(), r, 4*time.Second)
	}
	r.cancel()
	<-r.done
	m.mu.Lock()
	if m.run == r {
		m.run = nil
		m.status = NetworkStatus{State: "Stopped", Temporary: m.temporary, Peers: []NetworkPeer{}}
	}
	m.mu.Unlock()
	if logoutErr != nil {
		return errors.Join(temporaryLogoutError(logoutErr), r.closeErr)
	}
	return r.closeErr
}

// Logout revokes this node's saved login through Tailscale's LocalAPI, then
// synchronously stops listeners and flows. A future login starts a new run.
func (m *NetworkManager) Logout(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	r := m.run
	m.mu.RUnlock()
	if r == nil {
		return errors.New("请先启动安全网络以注销已保存的账号")
	}
	m.SetReceiving(false)
	m.CloseClient()
	limit := 20 * time.Second
	if r.temporary {
		limit = 4 * time.Second
	}
	logoutErr := logoutNetworkRun(ctx, r, limit)
	if logoutErr != nil && !r.temporary {
		return fmt.Errorf("账号注销失败: %s", redactNetworkError(logoutErr))
	}
	r.cancel()
	<-r.done
	m.mu.Lock()
	if m.run == r {
		m.run = nil
		m.status = NetworkStatus{State: "Stopped", Temporary: m.temporary, Peers: []NetworkPeer{}}
	}
	m.mu.Unlock()
	if logoutErr != nil {
		return errors.Join(temporaryLogoutError(logoutErr), r.closeErr)
	}
	return r.closeErr
}

func logoutNetworkRun(ctx context.Context, r *networkRun, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	select {
	case <-r.ready:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return errors.New("网络已关闭，无法确认服务器注销")
	}
	if r.initErr != nil {
		return r.initErr
	}
	if r.logout == nil {
		return errors.New("网络身份尚未就绪")
	}
	return r.logout(ctx)
}

func temporaryLogoutError(err error) error {
	return fmt.Errorf("已断开，服务端离线后清理，未确认立即移除: %s", redactNetworkError(err))
}

func (m *NetworkManager) setNetworkError(r *networkRun, message string, err error) {
	m.mu.Lock()
	if m.run == r {
		m.status.Running = false
		m.status.Account = NetworkAccount{}
		m.status.SelfID = ""
		m.status.Peers = []NetworkPeer{}
		m.status.Error = message + ": " + redactNetworkError(err)
		if r.initErr != nil {
			m.status.State = "Error"
		}
	}
	m.mu.Unlock()
}

func redactNetworkError(err error) string {
	// Never echo token-bearing URLs embedded by an upstream transport error.
	words := strings.Fields(err.Error())
	for i, word := range words {
		if strings.Contains(word, "://") || strings.Contains(word, "tskey-") {
			words[i] = "[网络地址或凭据已隐藏]"
		}
	}
	return strings.Join(words, " ")
}

func networkStatusFromBackend(st *ipnstate.Status) NetworkStatus {
	if st == nil {
		return NetworkStatus{State: "Stopped", Peers: []NetworkPeer{}}
	}
	out := NetworkStatus{State: st.BackendState, AuthURL: st.AuthURL, Peers: []NetworkPeer{}}
	for _, ip := range st.TailscaleIPs {
		if ip.Is4() {
			out.IP = ip.String()
		} else if ip.Is6() {
			out.IPv6 = ip.String()
		}
	}
	out.Running = st.BackendState == "Running" && (out.IP != "" || out.IPv6 != "")
	if out.Running {
		out.AuthURL = ""
	}
	owner := networkOwner(st)
	if owner == 0 {
		return out
	}
	profile := st.User[owner]
	out.SelfID = string(st.Self.ID)
	out.Account = NetworkAccount{ID: strconv.FormatInt(int64(owner), 10), Login: profile.LoginName, Name: profile.DisplayName}
	for _, peer := range st.Peer {
		if peer == nil || peer.UserID != owner || peer.ShareeNode || peer.AltSharerUserID != 0 || (peer.Tags != nil && peer.Tags.Len() != 0) {
			continue
		}
		p := NetworkPeer{ID: string(peer.ID), Name: peer.HostName, Online: peer.Online}
		if p.Name == "" {
			p.Name = strings.TrimSuffix(peer.DNSName, ".")
		}
		for _, ip := range peer.TailscaleIPs {
			if p.IP == "" || ip.Is4() {
				p.IP = ip.String()
			}
		}
		if p.IP != "" {
			out.Peers = append(out.Peers, p)
		}
	}
	sort.Slice(out.Peers, func(i, j int) bool {
		if out.Peers[i].Name == out.Peers[j].Name {
			return out.Peers[i].IP < out.Peers[j].IP
		}
		return out.Peers[i].Name < out.Peers[j].Name
	})
	return out
}

// networkOwner fails closed for logged-out, shared or ACL-tagged identities.
// A tailnet can contain several users; tailnet membership is not ownership.
func networkOwner(st *ipnstate.Status) tailcfg.UserID {
	if st == nil || st.BackendState != "Running" || st.Self == nil || st.Self.UserID == 0 || st.Self.ShareeNode || st.Self.AltSharerUserID != 0 || (st.Self.Tags != nil && st.Self.Tags.Len() != 0) {
		return 0
	}
	return st.Self.UserID
}

// SetAccountHandler enables the account-only control plane on tsnet port
// 48201. The handler may change while running. A nil handler denies requests
// immediately; the next status poll also closes its listener. It never binds
// an OS/LAN socket and is independent of Sunshine receive authorization.
func (m *NetworkManager) SetAccountHandler(handler http.Handler) {
	m.mu.Lock()
	m.accountHandler = handler
	m.mu.Unlock()
}

type networkAccountBackend interface {
	Status(context.Context) (*ipnstate.Status, error)
	WhoIs(context.Context, string) (*apitype.WhoIsResponse, error)
}

type networkAccountPeerKey struct{}

// NetworkAccountPeer returns the WireGuard-authenticated peer identity. Only
// NetworkManager's account endpoint inserts this value; headers are ignored.
func NetworkAccountPeer(ctx context.Context) (NetworkPeer, bool) {
	peer, ok := ctx.Value(networkAccountPeerKey{}).(NetworkPeer)
	return peer, ok
}

func newNetworkAccountServer(ctx context.Context, handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 80 * time.Second,
		IdleTimeout: 20 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
}

func (m *NetworkManager) networkAccountHandler(r *networkRun, backend networkAccountBackend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		m.mu.RLock()
		handler, active := m.accountHandler, m.run == r && r.ctx.Err() == nil
		m.mu.RUnlock()
		if !active || handler == nil {
			http.Error(w, "account service unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		st, err := backend.Status(ctx)
		owner := networkOwner(st)
		if err != nil || owner == 0 {
			http.Error(w, "account identity unavailable", http.StatusServiceUnavailable)
			return
		}
		who, err := backend.WhoIs(ctx, req.RemoteAddr)
		if err != nil || who == nil || who.Node == nil || who.UserProfile == nil || who.Node.User != owner || who.UserProfile.ID != owner || who.Node.Sharer != 0 || len(who.Node.Tags) != 0 || (who.Node.Hostinfo.Valid() && who.Node.Hostinfo.ShareeNode()) {
			http.Error(w, "same account required", http.StatusForbidden)
			return
		}
		peer := NetworkPeer{ID: string(who.Node.StableID), Name: strings.TrimSuffix(who.Node.Name, "."), Online: true}
		if who.Node.Hostinfo.Valid() && who.Node.Hostinfo.Hostname() != "" {
			peer.Name = who.Node.Hostinfo.Hostname()
		}
		if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			peer.IP = host
		}
		// Recheck shutdown/handler revocation after authentication. Caller
		// authorization is fresh per HTTP request, including keep-alive reuse.
		m.mu.RLock()
		handler, active = m.accountHandler, m.run == r && r.ctx.Err() == nil
		m.mu.RUnlock()
		if !active || handler == nil {
			http.Error(w, "account service unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), networkAccountPeerKey{}, peer)))
	})
}

type networkForwardPort struct {
	network string
	port    int
}

var networkForwardPorts = []networkForwardPort{
	{"tcp", 47984}, {"tcp", 47989}, {"tcp", 48010}, {"tcp", 48200},
	{"udp", 47998}, {"udp", 47999}, {"udp", 48000},
}

type networkForwards struct {
	cancel    context.CancelFunc
	listeners []net.Listener
	wg        sync.WaitGroup
	once      sync.Once
}

func startNetworkForwards(ctx context.Context, listen func(string, string) (net.Listener, error)) (*networkForwards, error) {
	ctx, cancel := context.WithCancel(ctx)
	f := &networkForwards{cancel: cancel}
	for _, p := range networkForwardPorts {
		ln, err := listen(p.network, fmt.Sprintf(":%d", p.port))
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s %d: %w", p.network, p.port, err)
		}
		f.listeners = append(f.listeners, ln)
		f.wg.Add(1)
		go func(ln net.Listener, p networkForwardPort) {
			defer f.wg.Done()
			serveNetworkForward(ctx, ln, p.network, fmt.Sprintf("127.0.0.1:%d", p.port), &f.wg)
		}(ln, p)
	}
	return f, nil
}

func (f *networkForwards) Close() {
	f.once.Do(func() {
		f.cancel()
		for _, ln := range f.listeners {
			ln.Close()
		}
		f.wg.Wait()
	})
}

func serveNetworkForward(ctx context.Context, ln net.Listener, network, target string, wg *sync.WaitGroup) {
	for {
		remote, err := ln.Accept()
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			remote.Close()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer remote.Close()
			dialer := net.Dialer{Timeout: 5 * time.Second}
			upstream, err := dialer.DialContext(ctx, network, target)
			if err != nil {
				return
			}
			defer upstream.Close()
			if network == "udp" {
				bridgeNetworkDatagrams(ctx, remote, upstream, 90*time.Second)
			} else {
				bridgeNetworkStreams(ctx, remote, upstream)
			}
		}()
	}
}

func bridgeNetworkStreams(ctx context.Context, a, b net.Conn) {
	closeBoth := func() { a.Close(); b.Close() }
	stop := context.AfterFunc(ctx, closeBoth)
	defer stop()
	defer closeBoth()
	var wg sync.WaitGroup
	copyOne := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err == nil {
			if cw, ok := dst.(interface{ CloseWrite() error }); ok {
				cw.CloseWrite()
				return
			}
		}
		closeBoth()
	}
	wg.Add(2)
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}

func bridgeNetworkDatagrams(ctx context.Context, a, b net.Conn, idle time.Duration) {
	closeBoth := func() { a.Close(); b.Close() }
	stop := context.AfterFunc(ctx, closeBoth)
	defer stop()
	defer closeBoth()
	renew := func() {
		deadline := time.Now().Add(idle)
		a.SetReadDeadline(deadline)
		b.SetReadDeadline(deadline)
	}
	renew()
	var wg sync.WaitGroup
	copyOne := func(dst, src net.Conn) {
		defer wg.Done()
		defer closeBoth()
		buf := make([]byte, 65535)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			renew()
			dst.SetWriteDeadline(time.Now().Add(10 * time.Second))
			// One Read/Write per datagram is intentional: do not stream-copy or
			// combine GameStream RTP and ENet packets, including empty packets.
			written, err := dst.Write(buf[:n])
			if err != nil || written != n {
				return
			}
		}
	}
	wg.Add(2)
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}
