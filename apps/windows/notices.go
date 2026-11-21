package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed notices
var noticesFiles embed.FS

func extractNotices(dir string) (string, error) {
	root := filepath.Join(dir, "开源许可")
	err := fs.WalkDir(noticesFiles, "notices", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("notices", path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := noticesFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
	return root, err
}
