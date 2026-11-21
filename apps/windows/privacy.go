package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const privacyLeaseTTL = 15 * time.Second

type PrivacyStatus struct {
	Available    bool   `json:"available"`
	Active       bool   `json:"active"`
	Experimental bool   `json:"experimental"`
	LocalEscape  string `json:"localEscape"`
	Reason       string `json:"reason,omitempty"`
}
type privacySurface interface {
	Renew() error
	Close() error
	Done() <-chan struct{}
}
type PrivacyManager struct {
	mu           sync.Mutex
	start        func(context.Context) (privacySurface, error)
	supported    func() error
	surface      privacySurface
	owner, lease string
	expires      time.Time
	now          func() time.Time
}

func newPrivacyManager(ctx context.Context) *PrivacyManager {
	m := &PrivacyManager{start: startPrivacySurface, supported: privacySupported, now: time.Now}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.Stop()
				return
			case <-ticker.C:
				m.expire()
			}
		}
	}()
	return m
}
func (m *PrivacyManager) clearLocked() {
	if m.surface != nil {
		_ = m.surface.Close()
	}
	m.surface = nil
	m.owner = ""
	m.lease = ""
	m.expires = time.Time{}
}
func (m *PrivacyManager) expire() { m.mu.Lock(); defer m.mu.Unlock(); m.expireLocked() }
func (m *PrivacyManager) expireLocked() {
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
func (m *PrivacyManager) Status() PrivacyStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	st := PrivacyStatus{Available: true, Active: m.surface != nil, Experimental: true, LocalEscape: "Ctrl+Alt+Shift+F12"}
	if err := m.supported(); err != nil {
		st.Available = false
		st.Reason = err.Error()
	}
	return st
}
func (m *PrivacyManager) Enable(ctx context.Context, owner string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if owner == "" {
		return "", errors.New("缺少设备身份")
	}
	if m.surface != nil {
		return "", errors.New("隐私遮罩已被另一个会话占用，请先关闭")
	}
	if err := m.supported(); err != nil {
		return "", err
	}
	surface, err := m.start(ctx)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		surface.Close()
		return "", err
	}
	m.surface = surface
	m.owner = owner
	m.lease = randomSecret()
	m.expires = m.now().Add(privacyLeaseTTL)
	return m.lease, nil
}
func (m *PrivacyManager) Renew(owner, lease string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.surface == nil || owner != m.owner || lease == "" || lease != m.lease {
		return errors.New("隐私遮罩会话已结束")
	}
	if err := m.surface.Renew(); err != nil {
		m.clearLocked()
		return err
	}
	m.expires = m.now().Add(privacyLeaseTTL)
	return nil
}
func (m *PrivacyManager) Disable(owner, lease string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.surface == nil {
		return nil
	}
	if owner != m.owner || lease == "" || lease != m.lease {
		return errors.New("不是当前隐私遮罩会话")
	}
	m.clearLocked()
	return nil
}
func (m *PrivacyManager) Stop() { m.mu.Lock(); defer m.mu.Unlock(); m.clearLocked() }

func (m *AccountManager) privacy(w http.ResponseWriter, r *http.Request) {
	if m.app == nil || m.app.privacy == nil {
		accountError(w, 503, "隐私遮罩不可用")
		return
	}
	if r.Method == http.MethodGet {
		accountJSON(w, 200, m.app.privacy.Status())
		return
	}
	var p struct {
		Action string `json:"action"`
		Lease  string `json:"lease"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || dec.Decode(&struct{}{}) != io.EOF {
		accountError(w, 400, "隐私遮罩参数无效")
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
	switch p.Action {
	case "enable":
		if m.app.superScreen != nil && m.app.superScreen.Status().Active {
			accountError(w, 409, "超级屏使用期间无需启用隐私遮罩")
			return
		}
		if !m.app.network.Status(r.Context()).Receiving {
			accountError(w, 409, "电脑未开启接收")
			return
		}
		lease, e := m.app.privacy.Enable(r.Context(), peer.ID)
		if e != nil {
			err = e
			break
		}
		accountJSON(w, 200, map[string]any{"lease": lease, "expiresIn": int(privacyLeaseTTL / time.Second), "status": m.app.privacy.Status()})
		return
	case "renew":
		err = m.app.privacy.Renew(peer.ID, p.Lease)
	case "disable":
		err = m.app.privacy.Disable(peer.ID, p.Lease)
	default:
		accountError(w, 400, "未知隐私遮罩操作")
		return
	}
	if err != nil {
		accountError(w, 409, err.Error())
		return
	}
	accountJSON(w, 200, m.app.privacy.Status())
}
