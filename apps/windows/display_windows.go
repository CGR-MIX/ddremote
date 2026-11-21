package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Win32 DISPLAYCONFIG layouts match wingdi.h; QDC_ONLY_ACTIVE_PATHS keeps
// a complete restorable snapshot without altering a display or driver.
type displayLUID struct {
	Low  uint32
	High int32
}
type displayRational struct{ Numerator, Denominator uint32 }
type displaySource struct {
	Adapter               displayLUID
	ID, ModeIndex, Status uint32
}
type displayTarget struct {
	Adapter                                      displayLUID
	ID, ModeIndex, Technology, Rotation, Scaling uint32
	Refresh                                      displayRational
	ScanLine                                     uint32
	Available                                    int32
	Status                                       uint32
}
type displayPath struct {
	Source displaySource
	Target displayTarget
	Flags  uint32
}
type displayMode struct {
	Kind, ID uint32
	Adapter  displayLUID
	Data     [48]byte
}
type displayInfoHeader struct {
	Kind, Size uint32
	Adapter    displayLUID
	ID         uint32
}
type displayTargetName struct {
	Header                displayInfoHeader
	Flags, Technology     uint32
	Manufacturer, Product uint16
	Connector             uint32
	Name                  [64]uint16
	Path                  [128]uint16
}
type displaySourceName struct {
	Header displayInfoHeader
	Name   [32]uint16
}
type displayAdapterName struct {
	Header displayInfoHeader
	Path   [128]uint16
}
type DisplaySnapshot struct {
	Paths []displayPath `json:"paths"`
	Modes []displayMode `json:"modes"`
}
type DisplayInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Source        string `json:"source"`
	Adapter       string `json:"adapter"`
	Path          string `json:"path"`
	Technology    uint32 `json:"technology"`
	Width, Height uint32
	X, Y          int32
	Refresh       float64 `json:"refresh"`
	Indirect      bool    `json:"indirect"`
}

func captureDisplaySnapshot() (DisplaySnapshot, error) { return queryDisplaySnapshot(2) }

func queryDisplaySnapshot(flags uint32) (DisplaySnapshot, error) {
	for attempt := 0; attempt < 4; attempt++ {
		var pc, mc uint32
		r, _, _ := user32.NewProc("GetDisplayConfigBufferSizes").Call(uintptr(flags), uintptr(unsafe.Pointer(&pc)), uintptr(unsafe.Pointer(&mc)))
		if r != 0 {
			return DisplaySnapshot{}, fmt.Errorf("读取显示布局大小: %w", windows.Errno(r))
		}
		if pc == 0 || pc > 256 || (flags == 2 && mc == 0) || mc > 1024 {
			return DisplaySnapshot{}, errors.New("当前没有可恢复的活动显示布局")
		}
		snap := DisplaySnapshot{Paths: make([]displayPath, pc), Modes: make([]displayMode, mc)}
		var modes uintptr
		if mc > 0 {
			modes = uintptr(unsafe.Pointer(&snap.Modes[0]))
		}
		r, _, _ = user32.NewProc("QueryDisplayConfig").Call(uintptr(flags), uintptr(unsafe.Pointer(&pc)), uintptr(unsafe.Pointer(&snap.Paths[0])), uintptr(unsafe.Pointer(&mc)), modes, 0)
		if r == 122 {
			continue
		}
		if r != 0 {
			return DisplaySnapshot{}, fmt.Errorf("读取显示布局: %w", windows.Errno(r))
		}
		snap.Paths = snap.Paths[:pc]
		snap.Modes = snap.Modes[:mc]
		return snap, nil
	}
	return DisplaySnapshot{}, errors.New("显示布局持续变化，请稍后重试")
}
func displayInfo(header unsafe.Pointer) error {
	r, _, _ := user32.NewProc("DisplayConfigGetDeviceInfo").Call(uintptr(header))
	if r != 0 {
		return windows.Errno(r)
	}
	return nil
}
func listDisplays() ([]DisplayInfo, error) {
	snap, err := captureDisplaySnapshot()
	if err != nil {
		return nil, err
	}
	out := []DisplayInfo{}
	for _, p := range snap.Paths {
		target := displayTargetName{Header: displayInfoHeader{Kind: 2, Size: uint32(unsafe.Sizeof(displayTargetName{})), Adapter: p.Target.Adapter, ID: p.Target.ID}}
		if err = displayInfo(unsafe.Pointer(&target)); err != nil {
			return nil, err
		}
		source := displaySourceName{Header: displayInfoHeader{Kind: 1, Size: uint32(unsafe.Sizeof(displaySourceName{})), Adapter: p.Source.Adapter, ID: p.Source.ID}}
		if err = displayInfo(unsafe.Pointer(&source)); err != nil {
			return nil, err
		}
		adapter := displayAdapterName{Header: displayInfoHeader{Kind: 4, Size: uint32(unsafe.Sizeof(displayAdapterName{})), Adapter: p.Target.Adapter}}
		if err = displayInfo(unsafe.Pointer(&adapter)); err != nil {
			return nil, err
		}
		d := DisplayInfo{ID: fmt.Sprintf("%08x:%08x:%d", uint32(p.Target.Adapter.High), p.Target.Adapter.Low, p.Target.ID), Name: windows.UTF16ToString(target.Name[:]), Source: windows.UTF16ToString(source.Name[:]), Adapter: windows.UTF16ToString(adapter.Path[:]), Path: windows.UTF16ToString(target.Path[:]), Technology: p.Target.Technology, Indirect: p.Target.Technology == 16 || p.Target.Technology == 17}
		if p.Target.Refresh.Denominator != 0 {
			d.Refresh = float64(p.Target.Refresh.Numerator) / float64(p.Target.Refresh.Denominator)
		}
		if int(p.Source.ModeIndex) < len(snap.Modes) {
			mode := snap.Modes[p.Source.ModeIndex]
			if mode.Kind == 1 {
				v := (*[5]uint32)(unsafe.Pointer(&mode.Data[0]))
				d.Width = v[0]
				d.Height = v[1]
				d.X = int32(v[3])
				d.Y = int32(v[4])
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// Validate is read-only. Restore must only be used by a supervised display
// transaction after the caller has persisted and checked this original snapshot.
func (s DisplaySnapshot) validate() error {
	if len(s.Paths) == 0 || len(s.Paths) > 256 || len(s.Modes) == 0 || len(s.Modes) > 1024 {
		return errors.New("显示布局快照无效")
	}
	r, _, _ := user32.NewProc("SetDisplayConfig").Call(uintptr(len(s.Paths)), uintptr(unsafe.Pointer(&s.Paths[0])), uintptr(len(s.Modes)), uintptr(unsafe.Pointer(&s.Modes[0])), 0x60)
	if r != 0 {
		return fmt.Errorf("显示布局验证失败: %w", windows.Errno(r))
	}
	return nil
}
func (s DisplaySnapshot) restore() error {
	if err := s.validate(); err != nil {
		return err
	}
	r, _, _ := user32.NewProc("SetDisplayConfig").Call(uintptr(len(s.Paths)), uintptr(unsafe.Pointer(&s.Paths[0])), uintptr(len(s.Modes)), uintptr(unsafe.Pointer(&s.Modes[0])), 0xA0)
	if r != 0 {
		return fmt.Errorf("恢复显示布局失败: %w", windows.Errno(r))
	}
	return nil
}
