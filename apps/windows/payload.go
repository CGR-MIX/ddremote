package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed payload/sunshine.zip payload/moonlight.zip
var payloads embed.FS
var extractMu sync.Mutex
var payloadHashes = map[string]string{"sunshine": "233008e46f4c0e501a586cbfd6c4fd4a4c0d414a0b5fc7f13c070eb92ec3824b", "moonlight": "6f91d268b41ed5ca8066d9d7578dc403b8db190991ff48f00b1700465f8f7ebf"}

func extractPayload(base, name, executable string) (string, error) {
	extractMu.Lock()
	defer extractMu.Unlock()
	data, e := payloads.ReadFile("payload/" + name + ".zip")
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if expected := payloadHashes[name]; expected != "" && expected != digest {
		return "", errors.New("内置组件校验失败: " + name)
	}
	dest := filepath.Join(base, "components", name+"-"+digest[:12])
	if _, e = os.Stat(filepath.Join(dest, ".complete")); e == nil {
		return findExecutable(dest, executable)
	}
	parent := filepath.Dir(dest)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return "", e
	}
	temp, e := os.MkdirTemp(parent, name+"-extract-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(temp)
	if e = extractZip(data, temp); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(temp, ".complete"), []byte(digest), 0600); e != nil {
		return "", e
	}
	if e = os.Rename(temp, dest); e != nil {
		return "", e
	}
	return findExecutable(dest, executable)
}
func extractZip(data []byte, dest string) error {
	r, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return e
	}
	for _, f := range r.File {
		rel := filepath.FromSlash(f.Name)
		if !filepath.IsLocal(rel) || f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe archive member: %q", f.Name)
		}
		out := filepath.Join(dest, rel)
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(out, 0700); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
			return e
		}
		src, e := f.Open()
		if e != nil {
			return e
		}
		dst, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			src.Close()
			return e
		}
		_, e = io.Copy(dst, io.LimitReader(src, 512*1024*1024))
		src.Close()
		closeErr := dst.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func findExecutable(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("组件中缺少 %s", name)
	}
	return found, nil
}
