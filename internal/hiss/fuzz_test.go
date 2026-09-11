package hiss

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func FuzzHissScan(f *testing.F) {
	// Seed corpus
	f.Add([]byte("package test\nfunc foo() {}\n"), "foo.go")
	f.Add([]byte("def bar():\n    pass\n"), "bar.py")
	f.Add([]byte("fn baz() -> () {}\n"), "baz.rs")
	f.Add([]byte("void qux(void) {}\n"), "qux.c")

	f.Fuzz(func(t *testing.T, data []byte, filename string) {
		if len(filename) > 32 || len(filename) == 0 {
			filename = "input.go"
		}
		filename = filepath.Base(filename)
		if filename == "." || filename == "/" {
			filename = "test.go"
		}

		tmpDir := t.TempDir()
		target := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(target, data, 0644); err != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		rep, err := Scan(ctx, tmpDir, ScanOptions{MaxFuncLOC: 60, Cap: 50})
		if err != nil {
			return
		}
		if rep == nil {
			t.Fatal("Scan returned nil report without error")
		}
		if rep.TotalInfractions < 0 {
			t.Fatalf("negative total infractions: %d", rep.TotalInfractions)
		}
	})
}
