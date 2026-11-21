package main

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxFileSize int64 = 1024 * 1024 * 1024

type FileEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}
type TransferStatus struct {
	Running     bool   `json:"running"`
	Port        int    `json:"port"`
	Folder      string `json:"folder"`
	Fingerprint string `json:"fingerprint"`
}
type TransferServer struct {
	mu                         sync.RWMutex
	folder, token, fingerprint string
	cert                       tls.Certificate
	server                     *http.Server
	root                       *os.Root
	readClipboard              func() (string, error)
	writeClipboard             func(string) error
	logf                       func(string, ...any)
}

func NewTransfer(folder string, secrets *Secrets, logf func(string, ...any)) (*TransferServer, error) {
	if e := os.MkdirAll(folder, 0700); e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(folder)
	if e != nil {
		return nil, e
	}
	cert, fp, e := secrets.certificate()
	if e != nil {
		root.Close()
		return nil, e
	}
	return &TransferServer{folder: folder, token: secrets.Token, fingerprint: fp, cert: cert, root: root, readClipboard: readClipboard, writeClipboard: writeClipboard, logf: logf}, nil
}
func (t *TransferServer) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.server != nil {
		return nil
	}
	ln, e := tls.Listen("tcp4", "0.0.0.0:48200", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{t.cert}})
	if e != nil {
		return fmt.Errorf("文件服务端口48200无法启动: %w", e)
	}
	s := &http.Server{Handler: t, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	t.server = s
	go func() { _ = s.Serve(ln) }()
	return nil
}
func (t *TransferServer) Stop() error {
	t.mu.Lock()
	s := t.server
	t.server = nil
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}
func (t *TransferServer) Close() { t.Stop(); t.root.Close() }
func (t *TransferServer) Status() TransferStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TransferStatus{t.server != nil, 48200, t.folder, t.fingerprint}
}
func (t *TransferServer) SetToken(token string) { t.mu.Lock(); t.token = token; t.mu.Unlock() }
func validFileName(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 240 || !utf8.ValidString(s) || strings.ContainsAny(s, "/\\:\x00\r\n<>\"|?*") || strings.TrimRight(s, " .") != s || strings.HasPrefix(s, ".dd-upload-") {
		return false
	}
	for _, r := range s {
		if r < 32 {
			return false
		}
	}
	return true
}
func (t *TransferServer) files() ([]FileEntry, error) {
	dir, e := t.root.Open(".")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil {
		return nil, e
	}
	out := []FileEntry{}
	for _, d := range entries {
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !validFileName(d.Name()) {
			continue
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, FileEntry{d.Name(), info.Size(), info.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func (t *TransferServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.URL.Path == "/" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html lang=zh-CN><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><title>DD远程</title><body style='font:17px system-ui;max-width:540px;margin:15vh auto;padding:24px;color:#152C56'><h1>DD远程</h1><p>文件与剪贴板通道已运行。</p><p>请在 DD远程 Android 客户端打开「文件与剪贴板」，粘贴这台电脑生成的连接链接。客户端会验证设备证书。</p></body></html>")
		return
	}
	t.mu.RLock()
	token := t.token
	t.mu.RUnlock()
	header := r.Header.Get("Authorization")
	auth := strings.TrimPrefix(header, "Bearer ")
	if !strings.HasPrefix(header, "Bearer ") || len(auth) != len(token) || subtle.ConstantTimeCompare([]byte(auth), []byte(token)) != 1 {
		apiError(w, 401, "需要有效的设备连接密钥")
		return
	}
	switch {
	case r.URL.Path == "/api/files" && r.Method == "GET":
		files, e := t.files()
		if e != nil {
			apiError(w, 500, "无法读取接收目录")
			return
		}
		writeJSON(w, 200, map[string]any{"files": files})
	case r.URL.Path == "/api/files/download" && r.Method == "GET":
		name := r.URL.Query().Get("name")
		if !validFileName(name) {
			apiError(w, 400, "文件名无效")
			return
		}
		file, e := t.root.Open(name)
		if e != nil {
			apiError(w, 404, "文件不存在")
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			apiError(w, 400, "只支持普通文件")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		http.ServeContent(w, r, name, info.ModTime(), file)
	case r.URL.Path == "/api/files/upload" && r.Method == "POST":
		t.upload(w, r)
	case r.URL.Path == "/api/clipboard" && r.Method == "GET":
		text, e := t.readClipboard()
		if e != nil {
			apiError(w, 500, e.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"text": text})
	case r.URL.Path == "/api/clipboard" && r.Method == "POST":
		var body struct {
			Text string `json:"text"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); e != nil || len(body.Text) > 1<<20 {
			apiError(w, 400, "剪贴板内容无效或超过1MB")
			return
		}
		if e := t.writeClipboard(body.Text); e != nil {
			apiError(w, 500, e.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		apiError(w, 404, "接口不存在")
	}
}
func (t *TransferServer) upload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validFileName(name) {
		apiError(w, 400, "文件名无效")
		return
	}
	if r.ContentLength > maxFileSize {
		apiError(w, 413, "单文件最大1GB")
		return
	}
	tempName := ".dd-upload-" + randomSecret()
	temp, e := t.root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		apiError(w, 500, "无法创建接收文件")
		return
	}
	defer t.root.Remove(tempName)
	n, e := io.Copy(temp, http.MaxBytesReader(w, r.Body, maxFileSize))
	ce := temp.Close()
	if e != nil || ce != nil {
		var maxErr *http.MaxBytesError
		if errors.As(e, &maxErr) {
			apiError(w, 413, "单文件最大1GB")
		} else {
			apiError(w, 400, "传输未完成，临时文件已删除")
		}
		return
	}
	saved := name
	for i := 0; i < 10000; i++ {
		if i > 0 {
			ext := filepath.Ext(name)
			saved = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext)
		}
		f, e := t.root.OpenFile(saved, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(e) {
			continue
		}
		if e != nil {
			apiError(w, 500, "无法保存接收文件")
			return
		}
		f.Close()
		if e = t.root.Rename(tempName, saved); e != nil {
			_ = t.root.Remove(saved)
			apiError(w, 500, "无法完成文件保存")
			return
		}
		t.logf("接收文件完成，%d 字节", n)
		writeJSON(w, 201, map[string]any{"ok": true, "name": saved, "size": n})
		return
	}
	apiError(w, 409, "同名文件过多，请更换名称")
}
