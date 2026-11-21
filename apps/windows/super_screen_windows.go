package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type superScreenProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	done     chan struct{}
	mu       sync.Mutex
	once     sync.Once
	err      error
	closeErr error
}

func (p *superScreenProcess) Done() <-chan struct{} { return p.done }
func (p *superScreenProcess) Renew() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return errors.New("超级屏辅助进程已退出")
	default:
	}
	_, err := io.WriteString(p.stdin, "renew\n")
	return err
}
func (p *superScreenProcess) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.stdin.Close()
		p.mu.Unlock()
		select {
		case <-p.done:
			p.closeErr = p.err
		case <-time.After(12 * time.Second):
			p.cmd.Process.Kill()
			<-p.done
			p.closeErr = errors.New("超级屏恢复超时；已移除临时设备并保留布局恢复记录")
		}
	})
	return p.closeErr
}
func startSuperScreen(ctx context.Context, dir string, width, height, fps uint32) (privacySurface, error) {
	if err := virtualDeviceSupported(); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := hiddenCommand(exe, "--super-screen-helper", strconv.Itoa(os.Getpid()), "--data-dir", dir, "--screen-width", strconv.Itoa(int(width)), "--screen-height", strconv.Itoa(int(height)), "--screen-fps", strconv.Itoa(int(fps)))
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
	p := &superScreenProcess{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	// Consume the complete protocol so final restoration errors reach Close.
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		first := true
		restored := false
		var finalErr error
		for scanner.Scan() {
			line := scanner.Text()
			if first && (line == "READY" || strings.HasPrefix(line, "ERROR ")) {
				ready <- line
				first = false
			}
			if line == "RESTORED" {
				restored = true
			}
			if strings.HasPrefix(line, "ERROR ") {
				finalErr = errors.New(strings.TrimPrefix(line, "ERROR "))
			}
		}
		if first {
			ready <- "ERROR 辅助进程没有就绪"
		}
		waitErr := cmd.Wait()
		if finalErr != nil {
			p.err = finalErr
		} else if waitErr != nil {
			p.err = waitErr
		} else if !restored {
			p.err = errors.New("未收到原显示布局恢复确认")
		}
		close(p.done)
	}()
	select {
	case line := <-ready:
		if line == "READY" {
			return p, nil
		}
		p.Close()
		return nil, errors.New(strings.TrimPrefix(line, "ERROR "))
	case <-ctx.Done():
		p.Close()
		return nil, ctx.Err()
	case <-time.After(25 * time.Second):
		p.Close()
		return nil, errors.New("虚拟屏初始化超时")
	}
}
func runSuperScreenHelper(parent uint32, dir string, width, height, fps uint32) (retErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := virtualDeviceSupported(); err != nil {
		return err
	}
	if err := privacyInteractiveDesktop(); err != nil {
		return err
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("缺少绝对路径的显示恢复目录")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := protectNetworkStateDirectory(dir); err != nil {
		return err
	}
	// A live second helper cannot consume another helper's recovery journal.
	mutexName, _ := windows.UTF16PtrFromString("Local\\DDRemote.SuperScreen")
	mutex, err := windows.CreateMutex(nil, true, mutexName)
	if err != nil {
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		return errors.New("另一个超级屏会话尚未结束")
	}
	defer windows.CloseHandle(mutex)
	defer windows.ReleaseMutex(mutex)
	parentHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, parent)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parentHandle)
	ok, _, e := user32.NewProc("RegisterHotKey").Call(0, 2, 0x4007, 0x7A) // Ctrl+Alt+Shift+F11
	if ok == 0 {
		return fmt.Errorf("超级屏本地恢复快捷键不可用: %v", e)
	}
	defer user32.NewProc("UnregisterHotKey").Call(0, 2)
	stopped := make(chan struct{})
	heartbeat := make(chan struct{}, 1)
	go func() {
		defer close(stopped)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64), 128)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) != "renew" {
				return
			}
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		}
	}()
	expires := time.Now().Add(20 * time.Second)
	alive := func() error {
		select {
		case <-stopped:
			return errors.New("控制连接已关闭")
		default:
		}
		select {
		case <-heartbeat:
			expires = time.Now().Add(20 * time.Second)
		default:
		}
		if time.Now().After(expires) {
			return errors.New("控制连接已超时")
		}
		state, err := windows.WaitForSingleObject(parentHandle, 0)
		if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			return errors.New("主程序已退出")
		}
		var msg privacyMessage
		for {
			has, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
			if has == 0 {
				break
			}
			if msg.Message == 0x0312 {
				return errors.New("已由本地快捷键退出")
			}
		}
		return nil
	}
	if err = recoverDisplayJournal(dir); err != nil {
		return fmt.Errorf("上次显示布局尚未恢复: %w", err)
	}
	snapshot, err := captureDisplaySnapshot()
	if err != nil {
		return err
	}
	if err = snapshot.validate(); err != nil {
		return err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	session := hex.EncodeToString(random[:])
	transaction, err := beginDisplayTransaction(snapshot, session, fileDisplayJournal{dir}, func(s DisplaySnapshot) error { return s.restore() })
	if err != nil {
		return err
	}
	defer func() {
		err := transaction.Close()
		if err != nil {
			retErr = errors.Join(retErr, err)
		} else {
			fmt.Fprintln(os.Stdout, "RESTORED")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	device, err := createVirtualDevice(ctx, session)
	if err != nil {
		return err
	}
	transaction.device = device
	var target displayPath
	deadline := time.Now().Add(12 * time.Second)
	for {
		if err = alive(); err != nil {
			return err
		}
		interfaces, interfaceErr := device.interfaces()
		if interfaceErr == nil {
			all, queryErr := queryDisplaySnapshot(1)
			if queryErr == nil {
				target, err = ownedVirtualPath(all, interfaces, displayAdapterPath)
				if err == nil {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			return errors.New("DD 虚拟显示器未出现，请检查签名驱动是否已安装")
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err = device.verifyBoundDriver(); err != nil {
		return err
	}
	layout, err := virtualOnlyLayout(target, width, height, fps)
	if err != nil {
		return err
	}
	if err = alive(); err != nil {
		return err
	}
	if err = layout.applyVirtualOnly(); err != nil {
		return err
	}
	// Do not claim success when Windows silently selected a different mode.
	current, err := captureDisplaySnapshot()
	if err != nil {
		return err
	}
	if err = verifyVirtualOnly(current, target, width, height, fps); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "READY")
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if alive() != nil {
			return nil
		}
		if privacyInteractiveDesktop() != nil {
			return nil
		}
	}
	return nil
}
func verifyVirtualOnly(s DisplaySnapshot, target displayPath, width, height, fps uint32) error {
	if len(s.Paths) != 1 {
		return errors.New("系统没有切换为单个虚拟屏")
	}
	p := s.Paths[0]
	if p.Target.Adapter != target.Target.Adapter || p.Target.ID != target.Target.ID || int(p.Source.ModeIndex) >= len(s.Modes) {
		return errors.New("系统切换到了意外的显示器")
	}
	mode := s.Modes[p.Source.ModeIndex]
	values := (*[5]uint32)(unsafe.Pointer(&mode.Data[0]))
	if mode.Kind != 1 || values[0] != width || values[1] != height || p.Target.Refresh.Denominator == 0 {
		return errors.New("虚拟屏分辨率验证失败")
	}
	rate := float64(p.Target.Refresh.Numerator) / float64(p.Target.Refresh.Denominator)
	if rate < float64(fps)-1 || rate > float64(fps)+1 {
		return errors.New("虚拟屏刷新率验证失败")
	}
	return nil
}
func superScreenDiagnostic() map[string]any {
	st := map[string]any{"experimental": true, "localEscape": "Ctrl+Alt+Shift+F11", "driverAutoInstall": false}
	if err := virtualDeviceSupported(); err != nil {
		st["available"] = false
		st["reason"] = err.Error()
	} else {
		st["available"] = true
	}
	return st
}

// Write a diagnostic result without creating a device or changing displays.
func writeSuperScreenDiagnostic(path string) error {
	data, err := json.MarshalIndent(superScreenDiagnostic(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Local explicit recovery also works after restarting the main program. The same
// mutex prevents restoring a journal while another live helper owns its display.
func restoreSuperScreenJournal(dir string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, _ := windows.UTF16PtrFromString("Local\\DDRemote.SuperScreen")
	mutex, err := windows.CreateMutex(nil, true, name)
	if err != nil {
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		return errors.New("超级屏辅助进程尚未退出，请稍后恢复")
	}
	defer windows.CloseHandle(mutex)
	defer windows.ReleaseMutex(mutex)
	return recoverDisplayJournal(dir)
}
