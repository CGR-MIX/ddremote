package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var clipboardMu sync.Mutex

func openClipboard(owner uintptr) error {
	for i := 0; i < 8; i++ {
		r, _, _ := user32.NewProc("OpenClipboard").Call(owner)
		if r != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("剪贴板正在被其他程序使用，请稍后重试")
}
func readClipboard() (string, error) {
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owner, e := clipboardWindow()
	if e != nil {
		return "", e
	}
	defer user32.NewProc("DestroyWindow").Call(owner)
	if e := openClipboard(owner); e != nil {
		return "", e
	}
	defer user32.NewProc("CloseClipboard").Call()
	h, _, _ := user32.NewProc("GetClipboardData").Call(13)
	if h == 0 {
		return "", nil
	}
	n, _, _ := kernel32.NewProc("GlobalSize").Call(h)
	if n > 2*1024*1024 {
		return "", errors.New("剪贴板文字超过 1 MB 限制")
	}
	p, _, e := kernel32.NewProc("GlobalLock").Call(h)
	if p == 0 {
		return "", e
	}
	defer kernel32.NewProc("GlobalUnlock").Call(h)
	u := unsafe.Slice((*uint16)(unsafe.Pointer(p)), n/2)
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u)), nil
}
func writeClipboard(text string) error {
	if len(text) > 1024*1024 {
		return errors.New("文字超过 1 MB 限制")
	}
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owner, e := clipboardWindow()
	if e != nil {
		return e
	}
	defer user32.NewProc("DestroyWindow").Call(owner)
	if e := openClipboard(owner); e != nil {
		return e
	}
	defer user32.NewProc("CloseClipboard").Call()
	u := utf16.Encode([]rune(text))
	u = append(u, 0)
	h, _, e := kernel32.NewProc("GlobalAlloc").Call(2, uintptr(len(u)*2))
	if h == 0 {
		return e
	}
	owned := true
	defer func() {
		if owned {
			kernel32.NewProc("GlobalFree").Call(h)
		}
	}()
	p, _, e := kernel32.NewProc("GlobalLock").Call(h)
	if p == 0 {
		return e
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	kernel32.NewProc("GlobalUnlock").Call(h)
	r, _, e := user32.NewProc("EmptyClipboard").Call()
	if r == 0 {
		return e
	}
	r, _, e = user32.NewProc("SetClipboardData").Call(13, h)
	if r == 0 {
		return e
	}
	owned = false
	return nil
}
func clipboardWindow() (uintptr, error) {
	class, _ := windows.UTF16PtrFromString("STATIC")
	h, _, e := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if h == 0 {
		return 0, fmt.Errorf("无法创建剪贴板窗口: %w", e)
	}
	return h, nil
}
func lockComputer() error {
	r, _, e := user32.NewProc("LockWorkStation").Call()
	if r == 0 {
		return e
	}
	return nil
}
func showError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("DD远程")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
func openTarget(target string) error {
	v, e := windows.UTF16PtrFromString(target)
	if e != nil {
		return e
	}
	verb, _ := windows.UTF16PtrFromString("open")
	r, _, e := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(v)), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("无法打开: %v", e)
	}
	return nil
}
func openHTTPS(s string) error {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("登录地址无效")
	}
	return openTarget(s)
}
func hiddenCommand(path string, args ...string) *exec.Cmd {
	c := exec.Command(path, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c
}
func nativePath(path string) (string, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return "", e
	}
	buf := make([]uint16, 32768)
	n, e := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if e != nil {
		return "", e
	}
	if n == 0 || int(n) >= len(buf) {
		return "", errors.New("无法取得原生组件路径")
	}
	return windows.UTF16ToString(buf[:n]), nil
}
func localAddresses() []string {
	out := []string{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			ip := n.IP.To4()
			if ip[0] == 169 && ip[1] == 254 {
				continue
			}
			out = append(out, n.IP.String())
		}
	}
	return out
}
func wakeMAC(value string) error {
	mac, e := net.ParseMAC(strings.TrimSpace(value))
	if e != nil || len(mac) != 6 {
		return errors.New("请输入 6 字节 MAC 地址，例如 AA:BB:CC:DD:EE:FF")
	}
	packet := []byte{255, 255, 255, 255, 255, 255}
	for i := 0; i < 16; i++ {
		packet = append(packet, mac...)
	}
	targets := []net.IP{net.IPv4bcast}
	ifs, _ := net.Interfaces()
	for _, iface := range ifs {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || len(n.Mask) != 4 {
				continue
			}
			ip := n.IP.To4()
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^n.Mask[i]
			}
			targets = append(targets, b)
		}
	}
	var last error
	sent := 0
	for _, ip := range targets {
		c, e := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: 9})
		if e != nil {
			last = e
			continue
		}
		_ = c.SetWriteDeadline(time.Now().Add(time.Second))
		_, e = c.Write(packet)
		c.Close()
		if e != nil {
			last = e
		} else {
			sent++
		}
	}
	if sent == 0 {
		return last
	}
	return nil
}
