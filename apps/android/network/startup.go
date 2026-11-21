package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Server.Dir does not cover ipnlocal's socket-statistics logger. Android app
// processes normally have no writable HOME, working directory or /tmp, so
// logpolicy.LogsDir otherwise panics while tsnet starts. Point that separate
// logger at our private no-backup directory before constructing the backend.
func prepareNetworkLogDirectory(stateDir string) error {
	if !filepath.IsAbs(stateDir) {
		return fmt.Errorf("网络身份目录必须是绝对路径")
	}
	dir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".write-check-")
	if err != nil {
		return err
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil {
		return removeErr
	}
	return os.Setenv("TS_LOGS_DIR", dir)
}

func guardedCallJSON(call func(string, string) (any, error), method, payload string) (result string) {
	defer func() {
		if failure := recover(); failure != nil {
			// A panic can contain a URL, token or another request's data. Report
			// only allow-listed causes and symbolic frames, never its raw value
			// or runtime/debug.Stack (which includes argument addresses).
			operation := "network"
			switch method {
			case "start", "login", "status", "discover", "prepare", "request", "logout", "networkChanged", "closeClient":
				operation = method
			}
			cause := "请彻底关闭应用后重新打开"
			if message, ok := failure.(string); ok && message == "no safe place found to store log state" {
				cause = "网络日志目录不可写，请更新应用"
			}
			var framesText strings.Builder
			pcs := make([]uintptr, 32)
			frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
			for {
				frame, more := frames.Next()
				if frame.Function != "" {
					fmt.Fprintf(&framesText, "%s (%s:%d)\n", frame.Function, filepath.Base(frame.File), frame.Line)
				}
				if !more {
					break
				}
			}
			data, _ := json.Marshal(map[string]string{
				"error":      fmt.Sprintf("网络组件内部错误（%s）：%s", operation, cause),
				"diagnostic": operation + "\n" + framesText.String(),
			})
			result = string(data)
		}
	}()
	value, err := call(method, payload)
	if err != nil {
		value = map[string]string{"error": err.Error()}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return `{"error":"无法读取网络状态"}`
	}
	return string(data)
}
