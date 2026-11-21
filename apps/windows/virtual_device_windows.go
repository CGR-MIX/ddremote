package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SwDeviceLifetimeHandle is intentional: Windows removes this DD-owned devnode
// when the helper closes or dies. Never operate on ROOT\DISPLAY (UU) devices.
type softwareDeviceCreateInfo struct {
	Size                                 uint32
	Instance, HardwareIDs, CompatibleIDs *uint16
	Container                            *windows.GUID
	Capabilities                         uint32
	Description, Location                *uint16
	SecurityDescriptor                   *windows.SECURITY_DESCRIPTOR
}
type softwareDeviceResult struct {
	instance string
	code     uint32
}
type virtualDevice struct {
	handle   uintptr
	instance string
	once     sync.Once
}

var virtualCallbacks sync.Map
var virtualCallbackID atomic.Uintptr
var virtualCreateCallback = syscall.NewCallback(func(handle, result, token, instance uintptr) uintptr {
	if value, ok := virtualCallbacks.LoadAndDelete(token); ok {
		name := ""
		if instance != 0 {
			name = windows.UTF16PtrToString((*uint16)(unsafe.Pointer(instance)))
		}
		value.(chan softwareDeviceResult) <- softwareDeviceResult{instance: name, code: uint32(result)}
	}
	return 0
})
var swdevice = windows.NewLazySystemDLL("cfgmgr32.dll")

func virtualDeviceSupported() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("超级屏试验版需要在电脑本地以管理员身份运行 DD，并先安装已验证的虚拟屏驱动")
	}
	if err := swdevice.NewProc("SwDeviceCreate").Find(); err != nil {
		return err
	}
	_, err := verifiedVirtualDriverINF()
	return err
}
func createVirtualDevice(ctx context.Context, session string) (*virtualDevice, error) {
	if err := virtualDeviceSupported(); err != nil {
		return nil, err
	}
	if !accountNoncePattern.MatchString(session) {
		return nil, errors.New("虚拟屏会话标识无效")
	}
	enumerator, _ := windows.UTF16PtrFromString("DDRemote")
	parent, _ := windows.UTF16PtrFromString("HTREE\\ROOT\\0")
	instance, _ := windows.UTF16PtrFromString("DDRemote-" + session)
	description, _ := windows.UTF16PtrFromString("DD远程临时超级屏")
	hardware := append(windows.StringToUTF16("MttVDD"), 0) // MULTI_SZ
	info := softwareDeviceCreateInfo{Size: uint32(unsafe.Sizeof(softwareDeviceCreateInfo{})), Instance: instance, HardwareIDs: &hardware[0], Capabilities: 1 | 8, Description: description}
	token := virtualCallbackID.Add(1)
	result := make(chan softwareDeviceResult, 1)
	virtualCallbacks.Store(token, result)
	var handle uintptr
	hr, _, _ := swdevice.NewProc("SwDeviceCreate").Call(uintptr(unsafe.Pointer(enumerator)), uintptr(unsafe.Pointer(parent)), uintptr(unsafe.Pointer(&info)), 0, 0, virtualCreateCallback, token, uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(info)
	runtime.KeepAlive(hardware)
	runtime.KeepAlive(parent)
	runtime.KeepAlive(enumerator)
	if int32(hr) < 0 {
		virtualCallbacks.Delete(token)
		return nil, fmt.Errorf("创建 DD 虚拟显示设备失败: HRESULT 0x%08x", uint32(hr))
	}
	device := &virtualDevice{handle: handle}
	select {
	case value := <-result:
		if int32(value.code) < 0 || !strings.HasPrefix(strings.ToUpper(value.instance), "SWD\\DDREMOTE\\DDREMOTE-") {
			device.Close()
			return nil, fmt.Errorf("虚拟设备初始化失败或身份不匹配: HRESULT 0x%08x", value.code)
		}
		device.instance = value.instance
		return device, nil
	case <-ctx.Done():
		device.Close()
		virtualCallbacks.Delete(token)
		return nil, ctx.Err()
	}
}
func (v *virtualDevice) Close() error {
	v.once.Do(func() {
		if v.handle != 0 {
			swdevice.NewProc("SwDeviceClose").Call(v.handle)
		}
	})
	return nil
}

var displayAdapterInterface = windows.GUID{Data1: 0x5b45201d, Data2: 0xf2f2, Data3: 0x4f3b, Data4: [8]byte{0x85, 0xbb, 0x30, 0xff, 0x1f, 0x95, 0x35, 0x99}}

func (v *virtualDevice) interfaces() ([]string, error) {
	// Query by exact instance returned by SwDeviceCreate, not friendly-name text.
	return windows.CM_Get_Device_Interface_List(v.instance, &displayAdapterInterface, 0)
}
func ownedVirtualPath(snap DisplaySnapshot, interfaces []string, adapterPath func(displayLUID) (string, error)) (displayPath, error) {
	var candidates []displayPath
	for _, p := range snap.Paths {
		if p.Target.Available == 0 || (p.Target.Technology != 16 && p.Target.Technology != 17) {
			continue
		}
		path, err := adapterPath(p.Target.Adapter)
		if err != nil {
			continue
		}
		matched := false
		for _, iface := range interfaces {
			if strings.EqualFold(path, iface) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// QDC_ALL_PATHS may expose multiple possible sources for one target.
		if len(candidates) > 0 {
			old := candidates[0]
			if old.Target.Adapter != p.Target.Adapter || old.Target.ID != p.Target.ID {
				return displayPath{}, errors.New("DD 虚拟适配器暴露多个显示器，拒绝猜测切换目标")
			}
			if p.Flags&1 != 0 {
				candidates[0] = p
			}
		} else {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) != 1 {
		return displayPath{}, errors.New("尚未发现 DD 自己创建的虚拟显示器；请检查驱动安装")
	}
	return candidates[0], nil
}
func displayAdapterPath(id displayLUID) (string, error) {
	value := displayAdapterName{Header: displayInfoHeader{Kind: 4, Size: uint32(unsafe.Sizeof(displayAdapterName{})), Adapter: id}}
	if err := displayInfo(unsafe.Pointer(&value)); err != nil {
		return "", err
	}
	return windows.UTF16ToString(value.Path[:]), nil
}

func virtualOnlyLayout(path displayPath, width, height, fps uint32) (DisplaySnapshot, error) {
	if (width != 1280 || height != 720) && (width != 1920 || height != 1080) && (width != 2560 || height != 1440) {
		return DisplaySnapshot{}, errors.New("当前超级屏试验只支持 720p、1080p、1440p 横屏")
	}
	if fps != 30 && fps != 60 {
		return DisplaySnapshot{}, errors.New("当前超级屏试验只支持 30 或 60Hz")
	}
	if path.Target.Available == 0 || (path.Target.Technology != 16 && path.Target.Technology != 17) {
		return DisplaySnapshot{}, errors.New("目标不是可用的间接显示器")
	}
	path.Flags = 1
	path.Source.ModeIndex = 0
	path.Target.ModeIndex = 0xffffffff
	path.Target.Rotation = 1
	path.Target.Scaling = 1
	path.Target.Refresh = displayRational{fps, 1}
	path.Target.ScanLine = 1
	mode := displayMode{Kind: 1, ID: path.Source.ID, Adapter: path.Source.Adapter}
	data := (*[5]uint32)(unsafe.Pointer(&mode.Data[0]))
	data[0] = width
	data[1] = height
	data[2] = 4 // PIXELFORMAT_32BPP, position 0,0
	return DisplaySnapshot{Paths: []displayPath{path}, Modes: []displayMode{mode}}, nil
}
func (s DisplaySnapshot) applyVirtualOnly() error {
	if len(s.Paths) != 1 || len(s.Modes) != 1 || s.Paths[0].Target.ModeIndex != 0xffffffff {
		return errors.New("无效的超级屏布局")
	}
	// Let Windows choose target timings for the exact supplied resolution/rate.
	// Do not SAVE_TO_DATABASE: persistent desktop preferences belong to the user.
	for _, flags := range []uintptr{0x460, 0x4a0} {
		result, _, _ := user32.NewProc("SetDisplayConfig").Call(1, uintptr(unsafe.Pointer(&s.Paths[0])), 1, uintptr(unsafe.Pointer(&s.Modes[0])), flags)
		if result != 0 {
			return fmt.Errorf("切换超级屏失败: %w", windows.Errno(result))
		}
	}
	return nil
}

const virtualDriverINFHash = "550d211fe481e74dfe3f9d724ed78be48b3a9113405965d683d9373e8d672f5d"

func verifiedVirtualDriverINF() (string, error) {
	// PnPUtil copies the verified package's unmodified INF to Windows\INF.
	// This path is protected by Windows. A friendly device name is insufficient.
	paths, err := filepath.Glob(filepath.Join(os.Getenv("WINDIR"), "INF", "oem*.inf"))
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		data, e := os.ReadFile(path)
		if e == nil && fmt.Sprintf("%x", sha256.Sum256(data)) == virtualDriverINFHash {
			return path, nil
		}
	}
	return "", errors.New("请先在电脑安装 DD 已验证的 Virtual Display Driver 25.7.23 驱动包")
}

func (v *virtualDevice) verifyBoundDriver() error {
	instance, _ := windows.UTF16PtrFromString(v.instance)
	var node uint32
	result, _, _ := swdevice.NewProc("CM_Locate_DevNodeW").Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(instance)), 0)
	if result != 0 {
		return errors.New("无法确认虚拟设备驱动实例")
	}
	key := windows.DEVPROPKEY{FmtID: windows.DEVPROPGUID{Data1: 0xa8b865dd, Data2: 0x2e3d, Data3: 0x4094, Data4: [8]byte{0xad, 0x97, 0xe5, 0x93, 0xa7, 0x0c, 0x75, 0xd6}}, PID: 5}
	var value [260]uint16
	size := uint32(unsafe.Sizeof(value))
	var kind uint32
	result, _, _ = swdevice.NewProc("CM_Get_DevNode_PropertyW").Call(uintptr(node), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&size)), 0)
	if result != 0 || kind != 18 {
		return errors.New("无法确认虚拟设备使用的驱动包")
	}
	inf := windows.UTF16ToString(value[:])
	if !strings.EqualFold(filepath.Base(inf), inf) || !strings.HasPrefix(strings.ToLower(inf), "oem") || !strings.HasSuffix(strings.ToLower(inf), ".inf") {
		return errors.New("虚拟设备驱动标识无效")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "INF", inf))
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != virtualDriverINFHash {
		return errors.New("Windows 为虚拟屏选择了未经本版本验证的其他驱动，已停止切换")
	}
	return nil
}
