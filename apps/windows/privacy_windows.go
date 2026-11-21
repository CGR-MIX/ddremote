package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

func privacyInteractiveDesktop() error {
	handle, _, e := user32.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if handle == 0 {
		return fmt.Errorf("输入桌面不可访问: %v", e)
	}
	defer user32.NewProc("CloseDesktop").Call(handle)
	var name [128]uint16
	var needed uint32
	ok, _, e := user32.NewProc("GetUserObjectInformationW").Call(handle, 2, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Sizeof(name)), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return fmt.Errorf("无法确认输入桌面: %v", e)
	}
	if !strings.EqualFold(windows.UTF16ToString(name[:]), "Default") {
		return errors.New("请先解锁电脑，隐私遮罩不支持系统安全桌面")
	}
	return nil
}

func privacySupported() error {
	if windows.RtlGetVersion().BuildNumber < 19041 {
		return errors.New("隐私遮罩需要 Windows 10 2004 或更新版本")
	}
	enabled := uint32(0)
	r, _, _ := windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmIsCompositionEnabled").Call(uintptr(unsafe.Pointer(&enabled)))
	if int32(r) < 0 || enabled == 0 {
		return errors.New("桌面合成不可用，不能排除隐私遮罩捕获")
	}
	return nil
}

type privacyProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
	mu    sync.Mutex
	once  sync.Once
}

func (p *privacyProcess) Done() <-chan struct{} { return p.done }
func (p *privacyProcess) Renew() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return errors.New("隐私遮罩已由本机退出")
	default:
	}
	_, err := io.WriteString(p.stdin, "renew\n")
	return err
}
func (p *privacyProcess) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.stdin.Close()
		p.mu.Unlock()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			p.cmd.Process.Kill()
			<-p.done
		}
	})
	return nil
}
func startPrivacySurface(ctx context.Context) (privacySurface, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := hiddenCommand(exe, "--privacy-helper", strconv.Itoa(os.Getpid()))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	p := &privacyProcess{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	go func() { cmd.Wait(); close(p.done) }()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- "helper exited before readiness"
		}
	}()
	select {
	case line := <-ready:
		if line == "READY" {
			return p, nil
		}
		p.Close()
		return nil, fmt.Errorf("隐私遮罩启动失败: %s", line)
	case <-ctx.Done():
		p.Close()
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		p.Close()
		return nil, errors.New("隐私遮罩启动超时")
	}
}

type privacyPoint struct{ X, Y int32 }
type privacyMessage struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   privacyPoint
	Private uint32
}
type privacyClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}

var maskWindow atomic.Uintptr
var maskExpiry atomic.Int64
var maskParent windows.Handle
var maskCallback = syscall.NewCallback(privacyWindowProc)

func privacyWindowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	switch msg {
	case 0x0010, 0x0312: // close or local escape
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 0x0002:
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	case 0x0014: // WM_ERASEBKGND: solid black, no captured content
		var rect [4]int32
		user32.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		brush, _, _ := windows.NewLazySystemDLL("gdi32.dll").NewProc("GetStockObject").Call(4)
		user32.NewProc("FillRect").Call(w, uintptr(unsafe.Pointer(&rect)), brush)
		return 1
	case 0x0084:
		return ^uintptr(0) // HTTRANSPARENT, remote input reaches desktop
	case 0x007E: // Display layout changed: remove mask instead of leaving uncovered regions
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 0x0113:
		state, _ := windows.WaitForSingleObject(maskParent, 0)
		if time.Now().UnixNano() > maskExpiry.Load() || state != uint32(windows.WAIT_TIMEOUT) || privacyInteractiveDesktop() != nil {
			user32.NewProc("DestroyWindow").Call(hwnd)
		}
		return 0
	}
	value, _, _ := user32.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), w, l)
	return value
}
func runPrivacyHelper(parent uint32) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := privacySupported(); err != nil {
		return err
	}
	var err error
	maskParent, err = windows.OpenProcess(windows.SYNCHRONIZE, false, parent)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(maskParent)
	// Per-monitor V2 keeps the overlay aligned across mixed-DPI physical screens.
	user32.NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
	if err := privacyInteractiveDesktop(); err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("DDRemotePrivacyMask")
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	brush, _, _ := windows.NewLazySystemDLL("gdi32.dll").NewProc("GetStockObject").Call(4)
	class := privacyClass{Size: uint32(unsafe.Sizeof(privacyClass{})), Proc: maskCallback, Instance: instance, Background: brush, ClassName: name}
	atom, _, e := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("注册遮罩失败: %v", e)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	metric := func(n uintptr) int32 { v, _, _ := user32.NewProc("GetSystemMetrics").Call(n); return int32(v) }
	x, y, width, height := metric(76), metric(77), metric(78), metric(79)
	if width <= 0 || height <= 0 {
		return errors.New("没有可用显示器")
	}
	// LAYERED + TRANSPARENT preserves input delivery; NOACTIVATE keeps keyboard focus.
	hwnd, _, e := user32.NewProc("CreateWindowExW").Call(0x080800A8, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x80000000, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建遮罩失败: %v", e)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	opacity, _, e := user32.NewProc("SetLayeredWindowAttributes").Call(hwnd, 0, 255, 2)
	if opacity == 0 {
		return fmt.Errorf("无法设置遮罩不透明度: %v", e)
	}
	ok, _, e := user32.NewProc("SetWindowDisplayAffinity").Call(hwnd, 0x11)
	if ok == 0 {
		return fmt.Errorf("系统无法排除遮罩捕获: %v", e)
	}
	var affinity uint32
	ok, _, _ = user32.NewProc("GetWindowDisplayAffinity").Call(hwnd, uintptr(unsafe.Pointer(&affinity)))
	if ok == 0 || affinity != 0x11 {
		return errors.New("遮罩捕获排除状态验证失败")
	}
	hotkey, _, e := user32.NewProc("RegisterHotKey").Call(hwnd, 1, 0x4007, 0x7B)
	if hotkey == 0 {
		return fmt.Errorf("本地解除快捷键不可用: %v", e)
	}
	defer user32.NewProc("UnregisterHotKey").Call(hwnd, 1)
	maskWindow.Store(hwnd)
	defer maskWindow.Store(0)
	maskExpiry.Store(time.Now().Add(20 * time.Second).UnixNano())
	timer, _, e := user32.NewProc("SetTimer").Call(hwnd, 1, 250, 0)
	if timer == 0 {
		return e
	}
	defer user32.NewProc("KillTimer").Call(hwnd, 1)
	user32.NewProc("ShowWindow").Call(hwnd, 4)
	user32.NewProc("SetWindowPos").Call(hwnd, ^uintptr(0), 0, 0, 0, 0, 0x0053)
	user32.NewProc("UpdateWindow").Call(hwnd)
	fmt.Fprintln(os.Stdout, "READY")
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64), 128)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) != "renew" {
				break
			}
			maskExpiry.Store(time.Now().Add(20 * time.Second).UnixNano())
		}
		if h := maskWindow.Load(); h != 0 {
			user32.NewProc("PostMessageW").Call(h, 0x0010, 0, 0)
		}
	}()
	var msg privacyMessage
	for {
		r, _, e := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return e
		}
		if r == 0 {
			break
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
