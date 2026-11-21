package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"unsafe"
)

type HostStatus struct {
	Running bool   `json:"running"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error"`
}
type Pairing struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}
type HostManager struct {
	mu            sync.Mutex
	dir           string
	secrets       *Secrets
	cmd           *exec.Cmd
	done          chan struct{}
	job           windows.Handle
	status        HostStatus
	client        *http.Client
	logf          func(string, ...any)
	onExit        func()
	forgetPending bool
}

func NewHost(dir string, secrets *Secrets, logf func(string, ...any)) *HostManager {
	return &HostManager{dir: dir, secrets: secrets, logf: logf}
}

// ForgetClients revokes this DD session's incoming desktop authorizations.
// Preserve the host UUID and unrelated state; both current and legacy client
// certificate lists are read by the pinned Sunshine version.
func (h *HostManager) ForgetClients() (result error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	defer func() { h.forgetPending = result != nil }()
	if h.cmd != nil {
		return errors.New("串流服务仍在运行，无法清理桌面授权")
	}
	path := filepath.Join(h.dir, "sunshine", "state.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state map[string]json.RawMessage
	if err = json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("无法读取桌面授权记录: %w", err)
	}
	var root map[string]json.RawMessage
	if value, ok := state["root"]; ok {
		if err = json.Unmarshal(value, &root); err != nil {
			return fmt.Errorf("无法读取桌面授权记录: %w", err)
		}
	}
	if root == nil {
		return nil
	}
	delete(root, "devices")
	delete(root, "named_devices")
	state["root"], err = json.Marshal(root)
	if err != nil {
		return err
	}
	data, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (h *HostManager) Start(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.forgetPending {
		return errors.New("上次桌面授权清理未完成，请修复数据目录权限后重新打开DD")
	}
	if h.cmd != nil {
		return nil
	}
	for _, port := range []string{"47984", "47989", "47990", "48010"} {
		c, e := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if e == nil {
			c.Close()
			return fmt.Errorf("端口 %s 已被占用，请停止其他 Sunshine 或当前控制会话", port)
		}
	}
	exe, e := extractPayload(h.dir, "sunshine", "sunshine.exe")
	if e != nil {
		return e
	}
	state := filepath.Join(h.dir, "sunshine")
	if e = os.MkdirAll(state, 0700); e != nil {
		return e
	}
	if e = protectNetworkStateDirectory(state); e != nil {
		return e
	}
	state, e = nativePath(state)
	if e != nil {
		return e
	}
	exe, e = nativePath(exe)
	if e != nil {
		return e
	}
	p := func(n string) string { return filepath.ToSlash(filepath.Join(state, n)) }
	cfg := filepath.Join(state, "sunshine.conf")
	config := fmt.Sprintf("sunshine_name = %s\nfile_state = %s\ncredentials_file = %s\nfile_apps = %s\npkey = %s\ncert = %s\nlog_path = %s\norigin_web_ui_allowed = pc\nupnp = disabled\naddress_family = ipv4\nbind_address = 127.0.0.1\nmin_log_level = 2\n", name, p("state.json"), p("credentials.json"), p("apps.json"), p("key.pem"), p("cert.pem"), p("sunshine.log"))
	if existing, readErr := os.ReadFile(cfg); readErr == nil {
		managed := map[string]bool{}
		for _, line := range strings.Split(config, "\n") {
			key, _, ok := strings.Cut(line, "=")
			if ok {
				managed[strings.TrimSpace(key)] = true
			}
		}
		for _, line := range strings.Split(string(existing), "\n") {
			key, _, ok := strings.Cut(line, "=")
			if !ok || !managed[strings.TrimSpace(key)] {
				config += line + "\n"
			}
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	if e = os.WriteFile(cfg, []byte(config), 0600); e != nil {
		return e
	}
	apps := map[string]any{"env": map[string]string{}, "apps": []any{map[string]any{"name": "Desktop", "image-path": "desktop.png"}}}
	b, _ := json.MarshalIndent(apps, "", "  ")
	if _, e = os.Stat(filepath.Join(state, "apps.json")); os.IsNotExist(e) {
		if e = os.WriteFile(filepath.Join(state, "apps.json"), b, 0600); e != nil {
			return e
		}
	}
	// Sunshine's util::hex emits reversed-byte uppercase SHA256. Avoid passing plaintext credentials through process arguments.
	salt := randomSecret()[:16]
	sum := sha256.Sum256([]byte(h.secrets.Password + salt))
	for i, j := 0, len(sum)-1; i < j; i, j = i+1, j-1 {
		sum[i], sum[j] = sum[j], sum[i]
	}
	creds := map[string]string{"username": h.secrets.User, "salt": salt, "password": strings.ToUpper(hex.EncodeToString(sum[:]))}
	b, _ = json.Marshal(creds)
	if e = os.WriteFile(filepath.Join(state, "credentials.json"), b, 0600); e != nil {
		return e
	}
	logFile, e := os.OpenFile(filepath.Join(state, "process.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	cmd := hiddenCommand(exe, cfg)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if e = cmd.Start(); e != nil {
		logFile.Close()
		return e
	}
	job, e := assignKillJob(cmd.Process.Pid)
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		logFile.Close()
		return fmt.Errorf("无法保护串流进程生命周期: %w", e)
	}
	h.cmd = cmd
	h.job = job
	h.done = make(chan struct{})
	h.status = HostStatus{Running: true}
	h.client = &http.Client{Timeout: 65 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
		data, e := os.ReadFile(filepath.Join(state, "cert.pem"))
		if e != nil {
			return e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return errors.New("无效的本机串流证书")
		}
		if len(cs.PeerCertificates) == 0 {
			return errors.New("本机串流没有证书")
		}
		_, e = cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
		return e
	}}}}
	done := h.done
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		h.mu.Lock()
		if h.cmd == cmd {
			h.cmd = nil
			h.status.Running = false
			h.status.Ready = false
			h.status.Error = "串流服务已退出，请查看诊断日志"
			windows.CloseHandle(h.job)
			h.job = 0
		}
		h.mu.Unlock()
		if h.onExit != nil {
			h.onExit()
		}
		close(done)
	}()
	h.logf("串流服务已启动，正在检测显示器和编码器")
	return nil
}
func assignKillJob(pid int) (windows.Handle, error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return 0, e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if e != nil {
		windows.CloseHandle(job)
		return 0, e
	}
	p, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if e != nil {
		windows.CloseHandle(job)
		return 0, e
	}
	defer windows.CloseHandle(p)
	if e = windows.AssignProcessToJobObject(job, p); e != nil {
		windows.CloseHandle(job)
		return 0, e
	}
	return job, nil
}
func (h *HostManager) Stop() error {
	h.mu.Lock()
	if h.cmd == nil {
		h.mu.Unlock()
		return nil
	}
	cmd, done := h.cmd, h.done
	h.mu.Unlock()
	e := cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return errors.New("等待串流服务停止超时")
	}
	h.mu.Lock()
	h.status.Error = ""
	h.mu.Unlock()
	h.logf("已停止接收远程连接")
	if errors.Is(e, os.ErrProcessDone) {
		return nil
	}
	return e
}
func (h *HostManager) API(ctx context.Context, method, path string, body any) (map[string]any, error) {
	h.mu.Lock()
	client := h.client
	running := h.cmd != nil
	user, pass := h.secrets.User, h.secrets.Password
	h.mu.Unlock()
	if !running || client == nil {
		return nil, errors.New("请先开始接收远程连接")
	}
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://127.0.0.1:47990"+path, r)
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	res, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("本机串流服务尚未就绪: %w", e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("串流服务返回 %d", res.StatusCode)
	}
	var out map[string]any
	if e = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&out); e != nil {
		return nil, e
	}
	return out, nil
}
func (h *HostManager) Status(ctx context.Context) (HostStatus, []Pairing) {
	h.mu.Lock()
	status := h.status
	h.mu.Unlock()
	pending := []Pairing{}
	if status.Running {
		c, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		defer cancel()
		out, e := h.API(c, "GET", "/api/pin", nil)
		if e == nil {
			status.Ready = true
			b, _ := json.Marshal(out["pairings"])
			_ = json.Unmarshal(b, &pending)
		} else {
			status.Ready = false
		}
		h.mu.Lock()
		if h.status.Running {
			h.status.Ready = status.Ready
		}
		h.mu.Unlock()
	}
	return status, pending
}

var pairID = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var pinPattern = regexp.MustCompile(`^[0-9]{4}$`)

func (h *HostManager) Pair(ctx context.Context, id, pin, name string) error {
	if !pairID.MatchString(id) || !pinPattern.MatchString(pin) || len(strings.TrimSpace(name)) == 0 || len(name) > 128 {
		return errors.New("请选择正在请求配对的设备，并填写4位PIN与设备名称")
	}
	out, e := h.API(ctx, "POST", "/api/pin", map[string]string{"pairing_id": id, "pin": pin, "name": name})
	if e != nil {
		return e
	}
	if out["status"] != true {
		return errors.New("配对未完成：请核对PIN并在控制端重新发起")
	}
	h.logf("已批准控制端配对")
	return nil
}

func (h *HostManager) VerifySuperScreenCaptureConfig() error {
	data, err := os.ReadFile(filepath.Join(h.dir, "sunshine", "sunshine.conf"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if (key == "output_name" && value != "") || (key == "dd_configuration_option" && value != "disabled") {
			return errors.New("当前 Sunshine 使用自定义显示目标或布局管理，请先恢复自动捕获后再使用超级屏")
		}
	}
	return nil
}
