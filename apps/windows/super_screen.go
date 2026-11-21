package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type SuperScreenStatus struct {
	Available     bool   `json:"available"`
	Active        bool   `json:"active"`
	Experimental  bool   `json:"experimental"`
	LocalEscape   string `json:"localEscape"`
	Reason        string `json:"reason,omitempty"`
	RecoveryError string `json:"recoveryError,omitempty"`
}
type SuperScreenManager struct {
	mu            sync.Mutex
	start         func(context.Context, uint32, uint32, uint32) (privacySurface, error)
	supported     func() error
	now           func() time.Time
	surface       privacySurface
	owner, lease  string
	expires       time.Time
	recoveryError string
	recover       func() error
	pending       func() error
}

func newSuperScreenManager(ctx context.Context, dir string) *SuperScreenManager {
	m := &SuperScreenManager{now: time.Now, supported: virtualDeviceSupported, recover: func() error { return restoreSuperScreenJournal(dir) }, pending: func() error {
		_, err := (fileDisplayJournal{dir}).Load()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("存在上次未恢复的显示布局，请点击恢复电脑显示")
	}, start: func(ctx context.Context, w, h, f uint32) (privacySurface, error) {
		return startSuperScreen(ctx, dir, w, h, f)
	}}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				m.Stop()
				return
			case <-tick.C:
				m.mu.Lock()
				m.expireLocked()
				m.mu.Unlock()
			}
		}
	}()
	return m
}
func (m *SuperScreenManager) clearLocked() error {
	var err error
	if m.surface != nil {
		err = m.surface.Close()
		if err != nil {
			m.recoveryError = err.Error()
		}
	}
	m.surface = nil
	m.owner = ""
	m.lease = ""
	m.expires = time.Time{}
	return err
}
func (m *SuperScreenManager) expireLocked() {
	if m.surface == nil {
		return
	}
	select {
	case <-m.surface.Done():
		m.clearLocked()
		return
	default:
	}
	if !m.now().Before(m.expires) {
		m.clearLocked()
	}
}
func (m *SuperScreenManager) Status() SuperScreenStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.surface == nil && m.pending != nil {
		if err := m.pending(); err != nil {
			m.recoveryError = err.Error()
		}
	}
	st := SuperScreenStatus{Available: true, Active: m.surface != nil, Experimental: true, LocalEscape: "Ctrl+Alt+Shift+F11", RecoveryError: m.recoveryError}
	if err := m.supported(); err != nil {
		st.Available = false
		st.Reason = err.Error()
	}
	return st
}
func (m *SuperScreenManager) Enable(ctx context.Context, owner string, width, height, fps uint32) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if owner == "" {
		return "", errors.New("缺少已验证的设备身份")
	}
	if m.surface != nil {
		return "", errors.New("另一个会话正在使用超级屏")
	}
	if _, err := virtualOnlyLayout(displayPath{Target: displayTarget{Available: 1, Technology: 17}}, width, height, fps); err != nil {
		return "", err
	}
	if err := m.supported(); err != nil {
		return "", err
	}
	surface, err := m.start(ctx, width, height, fps)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		cleanupErr := surface.Close()
		if cleanupErr != nil {
			m.recoveryError = cleanupErr.Error()
		}
		return "", errors.Join(err, cleanupErr)
	}
	m.surface = surface
	m.owner = owner
	m.lease = randomSecret()
	m.expires = m.now().Add(privacyLeaseTTL)
	m.recoveryError = ""
	return m.lease, nil
}
func (m *SuperScreenManager) Renew(owner, lease string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.surface == nil || owner != m.owner || lease == "" || lease != m.lease {
		return errors.New("超级屏会话已结束")
	}
	if err := m.surface.Renew(); err != nil {
		return errors.Join(err, m.clearLocked())
	}
	m.expires = m.now().Add(privacyLeaseTTL)
	return nil
}
func (m *SuperScreenManager) Disable(owner, lease string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.surface == nil {
		if m.recoveryError != "" {
			return errors.New(m.recoveryError)
		}
		return nil
	}
	if owner != m.owner || lease == "" || lease != m.lease {
		return errors.New("不是当前超级屏会话")
	}
	return m.clearLocked()
}
func (m *SuperScreenManager) Stop() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.clearLocked()
	if err == nil && m.recoveryError != "" {
		return errors.New(m.recoveryError)
	}
	return err
}
func (m *AccountManager) superScreen(w http.ResponseWriter, r *http.Request) {
	if m.app == nil || m.app.superScreen == nil {
		accountError(w, 503, "电脑端未提供超级屏")
		return
	}
	if r.Method == http.MethodGet {
		accountJSON(w, 200, m.app.superScreen.Status())
		return
	}
	var request struct {
		Action string `json:"action"`
		Lease  string `json:"lease"`
		Width  uint32 `json:"width"`
		Height uint32 `json:"height"`
		FPS    uint32 `json:"fps"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if dec.Decode(&request) != nil || dec.Decode(&struct{}{}) != io.EOF {
		accountError(w, 400, "超级屏参数无效")
		return
	}
	peer, ok := NetworkAccountPeer(r.Context())
	if !ok || peer.ID == "" {
		accountError(w, 403, "需要已验证的设备身份")
		return
	}
	m.app.actionMu.RLock()
	defer m.app.actionMu.RUnlock()
	m.app.hostOp.Lock()
	defer m.app.hostOp.Unlock()
	m.app.mu.Lock()
	exiting := m.app.exiting
	m.app.mu.Unlock()
	if exiting {
		accountError(w, 409, "账号正在退出")
		return
	}
	var err error
	switch request.Action {
	case "enable":
		if !m.app.network.Status(r.Context()).Receiving {
			accountError(w, 409, "电脑未开启接收")
			return
		}
		if m.app.privacy != nil && m.app.privacy.Status().Active {
			accountError(w, 409, "请先关闭隐私遮罩")
			return
		}
		if err := m.app.host.VerifySuperScreenCaptureConfig(); err != nil {
			accountError(w, 409, err.Error())
			return
		}
		lease, e := m.app.superScreen.Enable(r.Context(), peer.ID, request.Width, request.Height, request.FPS)
		if e != nil {
			err = e
			break
		}
		accountJSON(w, 200, map[string]any{"lease": lease, "expiresIn": 15, "status": m.app.superScreen.Status()})
		return
	case "renew":
		err = m.app.superScreen.Renew(peer.ID, request.Lease)
	case "disable":
		err = m.app.superScreen.Disable(peer.ID, request.Lease)
	default:
		accountError(w, 400, "未知超级屏操作")
		return
	}
	if err != nil {
		accountError(w, 409, err.Error())
		return
	}
	accountJSON(w, 200, m.app.superScreen.Status())
}

func (m *SuperScreenManager) Restore() error {
	if err := m.Stop(); err != nil && m.recover == nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recover != nil {
		if err := m.recover(); err != nil {
			m.recoveryError = err.Error()
			return err
		}
	}
	m.recoveryError = ""
	return nil
}
