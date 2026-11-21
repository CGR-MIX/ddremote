package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/windows"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	webview "github.com/jchv/go-webview2"
	qrcode "github.com/skip2/go-qrcode"
)

const Version = "0.3.3-super-screen-lab"

//go:embed web/index.html
var uiFiles embed.FS

type Settings struct {
	DeviceName     string `json:"deviceName"`
	AccountEnabled bool   `json:"accountEnabled"`
}
type App struct {
	mu            sync.Mutex
	actionMu      sync.RWMutex
	lifecycleMu   sync.Mutex
	shutdownOnce  sync.Once
	exiting       bool
	epoch         uint64
	sessionCtx    context.Context
	sessionCancel context.CancelFunc
	ctx           context.Context
	cancel        context.CancelFunc
	dir           string
	nativeDir     string
	settings      Settings
	secrets       *Secrets
	host          *HostManager
	network       *NetworkManager
	transfer      *TransferServer
	account       *AccountManager
	privacy       *PrivacyManager
	superScreen   *SuperScreenManager
	logFile       *os.File
	logs          []string
	hostOp        sync.Mutex
	clientMu      sync.Mutex
	clientRunning bool
	clientProcess *os.Process
	clientDone    chan struct{}
	clientProfile string
	instance      windows.Handle
}
type Request struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}
type Reply struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func newApp(dir string) (*App, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, e
	}
	if e = protectNetworkStateDirectory(abs); e != nil {
		return nil, e
	}
	lockPath, _ := windows.UTF16PtrFromString(filepath.Join(abs, "session.lock"))
	instance, e := windows.CreateFile(lockPath, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, errors.New("这台电脑上的 DD远程已经运行，或数据目录不可写")
	}
	ok := false
	defer func() {
		if !ok {
			windows.CloseHandle(instance)
		}
	}()
	secrets, e := loadSecrets(abs)
	if e != nil {
		return nil, e
	}
	// A process restart starts a new account session, including after a crash.
	secrets.Token = randomSecret()
	if e = saveSecrets(abs, secrets); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	nativeDir, e := chooseNativeDirectory(abs)
	if e != nil {
		cancel()
		return nil, e
	}
	a := &App{ctx: ctx, cancel: cancel, dir: abs, nativeDir: nativeDir, secrets: secrets, logs: []string{}, instance: instance}
	a.sessionCtx, a.sessionCancel = context.WithCancel(ctx)
	host, _ := os.Hostname()
	a.settings.DeviceName = "DD · " + host
	if b, e := os.ReadFile(filepath.Join(abs, "settings.json")); e == nil {
		_ = json.Unmarshal(b, &a.settings)
	}
	a.logFile, e = os.OpenFile(filepath.Join(abs, "ddremote.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		cancel()
		return nil, e
	}
	a.host = NewHost(nativeDir, secrets, a.logf)
	if e = a.host.ForgetClients(); e != nil {
		a.logFile.Close()
		cancel()
		return nil, e
	}
	a.network = NewNetworkManager(filepath.Join(abs, "network"), a.logf)
	a.transfer, e = NewTransfer(filepath.Join(abs, "接收文件"), secrets, a.logf)
	if e != nil {
		a.logFile.Close()
		cancel()
		return nil, e
	}
	a.host.onExit = func() {
		if a.privacy != nil {
			a.privacy.Stop()
			if err := a.superScreen.Stop(); err != nil {
				a.logf("超级屏恢复: %v", err)
			}
		}
		a.network.SetReceiving(false)
		a.transfer.Stop()
	}
	a.account = newAccountManager(a)
	a.network.SetAccountHandler(a.account)
	if err := a.clearStaleClientProfiles(); err != nil {
		a.transfer.Close()
		a.logFile.Close()
		cancel()
		return nil, fmt.Errorf("上次登录的本地凭据清理失败: %w", err)
	}
	a.privacy = newPrivacyManager(a.ctx)
	a.superScreen = newSuperScreenManager(a.ctx, filepath.Join(a.nativeDir, "display-recovery"))
	a.logf("DD远程 %s 已启动", Version)
	ok = true
	return a, nil
}
func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, line)
	if len(a.logs) > 60 {
		a.logs = a.logs[len(a.logs)-60:]
	}
	if a.logFile != nil {
		fmt.Fprintln(a.logFile, line)
	}
}
func (a *App) setAccountEnabled(enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.AccountEnabled = enabled
	b, err := json.MarshalIndent(a.settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.dir, "settings.json"), b, 0600)
}
func (a *App) shutdown() {
	a.shutdownOnce.Do(func() {
		a.lifecycleMu.Lock()
		defer a.lifecycleMu.Unlock()
		a.mu.Lock()
		a.exiting = true
		a.epoch++
		a.sessionCancel()
		a.mu.Unlock()
		a.actionMu.Lock()
		defer a.actionMu.Unlock()
		a.privacy.Stop()
		if err := a.superScreen.Stop(); err != nil {
			a.logf("超级屏恢复: %v", err)
		}
		a.network.Stop()
		a.cancel()
		clientErr := a.closeClientProcess()
		profileErr := a.clearClientProfile()
		hostErr := a.host.Stop()
		a.transfer.Close()
		if err := errors.Join(clientErr, profileErr, hostErr, a.revokeSessionAccess()); err != nil {
			a.logf("本地会话清理未全部完成: %v", err)
		}
		a.logf("DD远程已关闭")
		a.mu.Lock()
		a.logFile.Close()
		a.logFile = nil
		a.mu.Unlock()
		windows.CloseHandle(a.instance)
	})
}

func (a *App) logoutAccount() (any, error) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.mu.Lock()
	a.exiting = true
	a.epoch++
	a.sessionCancel()
	a.mu.Unlock()
	a.actionMu.Lock()
	defer a.actionMu.Unlock()
	defer func() {
		a.mu.Lock()
		a.sessionCtx, a.sessionCancel = context.WithCancel(a.ctx)
		a.exiting = false
		a.mu.Unlock()
	}()
	a.privacy.Stop()
	if err := a.superScreen.Stop(); err != nil {
		a.logf("超级屏恢复: %v", err)
	}
	networkErr := a.network.Logout(a.ctx)
	clientErr := a.closeClientProcess()
	a.hostOp.Lock()
	hostErr := a.host.Stop()
	transferErr := a.transfer.Stop()
	accessErr := a.revokeSessionAccess()
	a.hostOp.Unlock()
	profileErr := a.clearClientProfile()
	a.account.Clear()
	settingsErr := a.setAccountEnabled(false)
	if err := errors.Join(clientErr, hostErr, transferErr, accessErr, profileErr, settingsErr); err != nil {
		return nil, errors.Join(networkErr, fmt.Errorf("账号连接已断开；部分本地清理未完成: %w", err))
	}
	if networkErr != nil {
		return nil, networkErr
	}
	return map[string]string{"message": "已退出账号并移除此设备"}, nil
}

func (a *App) revokeSessionAccess() error {
	pairErr := a.host.ForgetClients()
	a.mu.Lock()
	a.secrets.Token = randomSecret()
	a.transfer.SetToken(a.secrets.Token)
	tokenErr := saveSecrets(a.dir, a.secrets)
	a.mu.Unlock()
	return errors.Join(pairErr, tokenErr)
}
func (a *App) status() any {
	host, pairings := a.host.Status(a.ctx)
	hostname, _ := os.Hostname()
	a.mu.Lock()
	name := a.settings.DeviceName
	logs := append([]string{}, a.logs...)
	a.mu.Unlock()
	return map[string]any{"version": Version, "deviceName": name, "hostname": hostname, "addresses": localAddresses(), "host": host, "pairings": pairings, "network": a.network.Status(a.ctx), "transfer": a.transfer.Status(), "logTail": logs, "privacy": a.privacy.Status(), "superScreen": a.superScreen.Status(), "caps": map[string]bool{"streaming": true, "sunshine": true, "tailscale": true, "files": true, "clipboard": true, "wol": true, "client": true, "gamepad": false}}
}
func (a *App) handle(req Request) (any, error) {
	var p struct {
		ID         string `json:"id"`
		Pin        string `json:"pin"`
		Name       string `json:"name"`
		Host       string `json:"host"`
		Text       string `json:"text"`
		DeviceName string `json:"deviceName"`
		MAC        string `json:"mac"`
	}
	if len(req.Payload) > 0 {
		if e := json.Unmarshal(req.Payload, &p); e != nil {
			return nil, errors.New("请求格式无效")
		}
	}
	if a.ctx.Err() != nil {
		return nil, errors.New("应用正在关闭")
	}
	if req.Action == "accountLogout" || req.Action == "networkStop" {
		return a.logoutAccount()
	}
	var operationCtx context.Context
	a.mu.Lock()
	blocked, epoch := a.exiting, a.epoch
	operationCtx = a.sessionCtx
	a.mu.Unlock()
	if blocked {
		return nil, errors.New("正在退出账号，请稍后重试")
	}
	if req.Action != "status" && req.Action != "accountDevices" {
		a.actionMu.RLock()
		defer a.actionMu.RUnlock()
		a.mu.Lock()
		blocked = a.exiting || a.epoch != epoch
		a.mu.Unlock()
		if blocked || operationCtx.Err() != nil {
			return nil, errors.New("本次操作已因退出账号取消")
		}
	}
	switch req.Action {
	case "accountDevices":
		return a.account.Devices(a.ctx)
	case "accountConnect":
		return a.account.Connect(operationCtx, p.ID)
	case "status":
		return a.status(), nil
	case "startHost":
		a.hostOp.Lock()
		defer a.hostOp.Unlock()
		a.mu.Lock()
		name := a.settings.DeviceName
		a.mu.Unlock()
		if e := a.transfer.Start(); e != nil {
			return nil, e
		}
		if e := a.host.Start(name); e != nil {
			a.transfer.Stop()
			return nil, e
		}
		a.network.SetReceiving(true)
		return map[string]string{"message": "已开始初始化串流服务"}, nil
	case "privacyStop":
		a.privacy.Stop()
		return map[string]string{"message": "隐私遮罩已关闭"}, nil
	case "superScreenStop":
		return nil, a.superScreen.Restore()
	case "superScreenStatus":
		return a.superScreen.Status(), nil
	case "privacyStatus":
		return a.privacy.Status(), nil
	case "stopHost":
		a.hostOp.Lock()
		defer a.hostOp.Unlock()
		a.privacy.Stop()
		if err := a.superScreen.Stop(); err != nil {
			a.logf("超级屏恢复: %v", err)
		}
		a.network.SetReceiving(false)
		e := a.host.Stop()
		a.transfer.Stop()
		return nil, e
	case "pair":
		ctx, cancel := context.WithTimeout(operationCtx, 65*time.Second)
		defer cancel()
		return nil, a.host.Pair(ctx, p.ID, p.Pin, p.Name)
	case "networkStart":
		hostname, _ := os.Hostname()
		return nil, a.network.Start(a.ctx, "dd-"+hostname)
	case "networkLogin", "accountLogin":
		hostname, _ := os.Hostname()
		if e := a.network.Start(a.ctx, "dd-"+hostname); e != nil {
			return nil, e
		}
		u, e := a.network.Login(operationCtx)
		if e != nil {
			return nil, e
		}
		if u == "" {
			return map[string]string{"message": "安全网络已经登录"}, nil
		}
		return nil, openHTTPS(u)
	case "copy":
		return nil, writeClipboard(p.Text)
	case "openFolder":
		return nil, openTarget(a.transfer.folder)
	case "openLogs":
		return nil, openTarget(a.nativeDir)
	case "openNotices":
		path, err := extractNotices(a.dir)
		if err != nil {
			return nil, err
		}
		return nil, openTarget(path)
	case "clipboardGet":
		s, e := readClipboard()
		return map[string]string{"text": s}, e
	case "clipboardSet":
		return nil, writeClipboard(p.Text)
	case "files":
		files, e := a.transfer.files()
		return map[string]any{"files": files}, e
	case "wake":
		e := wakeMAC(p.MAC)
		if e == nil {
			a.logf("已发送局域网唤醒包；是否开机取决于目标主机设置")
		}
		return nil, e
	case "lock":
		return nil, lockComputer()
	case "saveSettings":
		name := strings.TrimSpace(p.DeviceName)
		if len(name) < 1 || len(name) > 128 || strings.ContainsAny(name, "\r\n=\x00") {
			return nil, errors.New("设备名称需1至128字节，不包含换行或等号")
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.settings.DeviceName = name
		b, _ := json.MarshalIndent(a.settings, "", "  ")
		if e := os.WriteFile(filepath.Join(a.dir, "settings.json"), b, 0600); e != nil {
			return nil, e
		}
		cfg := filepath.Join(a.nativeDir, "sunshine", "sunshine.conf")
		if b, e := os.ReadFile(cfg); e == nil {
			lines := strings.Split(string(b), "\n")
			for i, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), "sunshine_name") {
					lines[i] = "sunshine_name = " + name
				}
			}
			if e = os.WriteFile(cfg, []byte(strings.Join(lines, "\n")), 0600); e != nil {
				return nil, e
			}
		}
		return map[string]string{"message": "已保存，串流设备名称下次开始接收时生效"}, nil
	case "link":
		if !a.transfer.Status().Running {
			return nil, errors.New("请先开始接收远程连接")
		}
		host := strings.TrimSpace(p.Host)
		ip := net.ParseIP(host)
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
			return nil, errors.New("请选择可由另一台设备访问的本机IP地址")
		}
		allowed := false
		for _, address := range localAddresses() {
			if host == address {
				allowed = true
			}
		}
		network := a.network.Status(a.ctx)
		if host == network.IP || host == network.IPv6 {
			allowed = true
		}
		if !allowed {
			return nil, errors.New("该地址不属于当前设备")
		}
		a.mu.Lock()
		token := a.secrets.Token
		a.mu.Unlock()
		u := url.URL{Scheme: "ddremote", Host: "connect"}
		q := url.Values{"host": {host}, "port": {"48200"}, "token": {token}, "fingerprint": {a.transfer.fingerprint}}
		u.RawQuery = q.Encode()
		png, e := qrcode.Encode(u.String(), qrcode.Medium, 320)
		if e != nil {
			return nil, e
		}
		return map[string]string{"url": u.String(), "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}, nil
	case "revoke":
		a.mu.Lock()
		old := a.secrets.Token
		a.secrets.Token = randomSecret()
		e := saveSecrets(a.dir, a.secrets)
		if e != nil {
			a.secrets.Token = old
		} else {
			a.transfer.SetToken(a.secrets.Token)
		}
		a.mu.Unlock()
		if e == nil {
			a.logf("文件与剪贴板连接密钥已更换，旧连接链接已失效")
		}
		return nil, e
	case "openSunshine":
		status, _ := a.host.Status(a.ctx)
		if !status.Ready {
			return nil, errors.New("串流服务尚未就绪")
		}
		if e := writeClipboard(a.secrets.Password); e != nil {
			return nil, e
		}
		e := openHTTPS("https://127.0.0.1:47990")
		return map[string]string{"message": "管理用户名 ddremote，密码已复制到剪贴板"}, e
	case "client":
		return a.launchClient(strings.TrimSpace(p.Host), false)
	case "clientPair":
		if strings.TrimSpace(p.Host) == "" {
			return nil, errors.New("请输入需要配对的电脑地址")
		}
		return a.launchClient(strings.TrimSpace(p.Host), true)
	default:
		return nil, errors.New("未知操作")
	}
}
func (a *App) launchClient(host string, pair bool) (any, error) {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.clientRunning {
		return nil, errors.New("控制端窗口已打开，请先关闭它再更换目标")
	}
	started := false
	prepared := false
	defer func() {
		if prepared && !started {
			a.network.CloseClient()
		}
	}()
	if host != "" {
		if strings.HasPrefix(host, "-") || strings.ContainsAny(host, "/\\\r\n \x00") {
			return nil, errors.New("请输入有效IP或设备名称")
		}
		ip := net.ParseIP(host)
		if ip != nil && len(ip.To4()) == 4 && ip.To4()[0] == 100 && ip.To4()[1] >= 64 && ip.To4()[1] <= 127 {
			var e error
			host, e = a.network.PrepareClient(a.ctx, host)
			if e != nil {
				return nil, e
			}
			prepared = true
		}
	}
	exe, e := extractPayload(a.nativeDir, "moonlight", "Moonlight.exe")
	if e != nil {
		return nil, e
	}
	args := []string{}
	if host != "" {
		if pair {
			args = []string{"pair", host}
		} else {
			args = []string{"stream", host, "Desktop", "--absolute-mouse", "--display-mode", "windowed"}
		}
	}
	cmd := hiddenCommand(exe, args...)
	profile, e := a.ensureClientProfile()
	if e != nil {
		return nil, e
	}
	cmd.Dir = profile
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	a.watchClient(cmd)
	started = true
	if host != "" {
		if pair {
			return map[string]string{"message": "配对窗口已打开，请在被控端 DD远程输入显示的PIN"}, nil
		}
		return map[string]string{"message": "正在打开远程桌面；若未配对，请先关闭窗口并点击首次配对"}, nil
	}
	return map[string]string{"message": "控制端已打开"}, nil
}
func chooseNativeDirectory(dir string) (string, error) {
	ascii := func(s string) bool {
		for _, r := range s {
			if r > 127 {
				return false
			}
		}
		return true
	}
	if ascii(dir) {
		return dir, nil
	}
	hash := sha256.Sum256([]byte(strings.ToLower(dir)))
	candidates := []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "DDRemoteRuntime", fmt.Sprintf("%x", hash[:8])), filepath.Join(os.Getenv("ProgramData"), "DDRemoteRuntime", fmt.Sprintf("%x", hash[:8]))}
	for _, p := range candidates {
		if !ascii(p) {
			continue
		}
		if e := os.MkdirAll(p, 0700); e != nil {
			continue
		}
		if e := protectNetworkStateDirectory(p); e != nil {
			continue
		}
		return p, nil
	}
	return "", errors.New("原生串流组件需要可写的英文路径，请通过 --data-dir 指定英文目录")
}

// Caller owns clientMu when starting a process or choosing its profile.
func (a *App) watchClient(cmd *exec.Cmd) {
	a.clientRunning = true
	a.clientProcess = cmd.Process
	done := make(chan struct{})
	a.clientDone = done
	go func() {
		_ = cmd.Wait()
		a.clientMu.Lock()
		a.network.CloseClient()
		a.clientRunning = false
		a.clientProcess = nil
		a.clientMu.Unlock()
		close(done)
	}()
}
func (a *App) closeClientProcess() error {
	a.clientMu.Lock()
	p, done := a.clientProcess, a.clientDone
	a.clientMu.Unlock()
	if p == nil || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	closeOwnedWindows(uint32(p.Pid))
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		killErr := p.Kill()
		select {
		case <-done:
			return nil
		case <-time.After(3 * time.Second):
			return errors.Join(errors.New("控制窗口未能及时关闭，账号网络已断开"), killErr)
		}
	}
}
func (a *App) ensureClientProfile() (string, error) {
	if a.clientProfile != "" {
		return a.clientProfile, nil
	}
	dir, err := os.MkdirTemp(a.nativeDir, "account-session-")
	if err != nil {
		return "", err
	}
	if err = protectNetworkStateDirectory(dir); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "portable.dat"), []byte("DD"), 0600); err != nil {
		return "", err
	}
	a.clientProfile = dir
	return dir, nil
}
func (a *App) clearClientProfile() error {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.clientProfile != "" {
		profile := a.clientProfile
		// A failed disk deletion must never make a later account reuse this identity.
		a.clientProfile = ""
		rel, err := filepath.Rel(a.nativeDir, profile)
		if err != nil || !filepath.IsLocal(rel) || filepath.Dir(rel) != "." || !strings.HasPrefix(filepath.Base(rel), "account-session-") {
			return errors.New("拒绝清理非DD账号会话目录")
		}
		if err = os.RemoveAll(profile); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) clearStaleClientProfiles() error {
	entries, err := os.ReadDir(a.nativeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "account-session-") && filepath.IsLocal(entry.Name()) {
			if err = os.RemoveAll(filepath.Join(a.nativeDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func main() {
	runtime.LockOSThread()
	dirFlag := flag.String("data-dir", "", "private application state directory")
	smokeReport := flag.String("smoke-report", "", "run isolated local startup check and write JSON report")
	privacyHelper := flag.Uint("privacy-helper", 0, "internal ephemeral privacy helper parent PID")
	superHelper := flag.Uint("super-screen-helper", 0, "internal ephemeral virtual display helper parent PID")
	screenWidth := flag.Uint("screen-width", 1920, "virtual display width")
	screenHeight := flag.Uint("screen-height", 1080, "virtual display height")
	screenFPS := flag.Uint("screen-fps", 60, "virtual display refresh")
	screenDiagnostic := flag.String("super-screen-diagnostic", "", "write read-only virtual display preflight JSON")
	flag.Parse()
	if *screenDiagnostic != "" {
		if err := writeSuperScreenDiagnostic(*screenDiagnostic); err != nil {
			log.Print(err)
		}
		return
	}
	if *superHelper != 0 {
		if err := runSuperScreenHelper(uint32(*superHelper), *dirFlag, uint32(*screenWidth), uint32(*screenHeight), uint32(*screenFPS)); err != nil {
			fmt.Fprintln(os.Stdout, "ERROR "+err.Error())
		}
		return
	}
	if *privacyHelper != 0 {
		if err := runPrivacyHelper(uint32(*privacyHelper)); err != nil {
			fmt.Fprintln(os.Stdout, "ERROR "+err.Error())
		}
		return
	}
	dir := *dirFlag
	if dir == "" {
		dir = filepath.Join(os.Getenv("LOCALAPPDATA"), "DDRemote")
	}
	app, e := newApp(dir)
	if e != nil {
		log.Print(e)
		showError("DD远程无法启动：" + e.Error())
		return
	}
	defer app.shutdown()
	if *smokeReport != "" {
		report := map[string]any{"version": Version}
		_, e := app.handle(Request{Action: "startHost"})
		if e != nil {
			report["error"] = e.Error()
		} else {
			deadline := time.Now().Add(45 * time.Second)
			for time.Now().Before(deadline) {
				s, _ := app.host.Status(app.ctx)
				if s.Ready || !s.Running {
					break
				}
				time.Sleep(time.Second)
			}
			report["status"] = app.status()
		}
		b, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(*smokeReport, b, 0600)
		return
	}
	w := webview.NewWithOptions(webview.WebViewOptions{DataPath: filepath.Join(app.dir, "webview"), WindowOptions: webview.WindowOptions{Title: "DD远程", Width: 1160, Height: 800, Center: true}})
	if w == nil {
		showError("无法创建界面，请安装 Microsoft Edge WebView2 Runtime。")
		return
	}
	defer w.Destroy()
	w.SetSize(760, 580, webview.HintMin)
	_ = w.Bind("ddInvoke", func(raw string) {
		go func() {
			var request Request
			if e := json.Unmarshal([]byte(raw), &request); e != nil {
				return
			}
			data, err := app.handle(request)
			reply := Reply{ID: request.ID, OK: err == nil, Data: data}
			if err != nil {
				reply.Error = err.Error()
			}
			b, _ := json.Marshal(reply)
			if app.ctx.Err() == nil {
				w.Dispatch(func() { w.Eval("window.ddResolve(" + string(b) + ")") })
			}
		}()
	})
	html, _ := uiFiles.ReadFile("web/index.html")
	w.SetHtml(string(html))
	w.Run()
	app.shutdown()
}
