package main

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestZipTraversalRejected(t *testing.T) {
	for _, name := range []string{"../evil.exe", "C:/evil.exe", "/evil.exe"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create(name)
		f.Write([]byte("must not escape"))
		z.Close()
		if e := extractZip(b.Bytes(), t.TempDir()); e == nil {
			t.Fatalf("unsafe member %s accepted", name)
		}
	}
}
