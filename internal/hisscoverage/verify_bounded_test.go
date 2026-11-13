package hisscoverage

import (
	"bytes"
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

type catalogReadFS struct {
	fstest.MapFS
	wholeRead bool
	bytesRead int
}

type catalogCountFile struct {
	fs.File
	owner *catalogReadFS
}

func (f *catalogCountFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.owner.bytesRead += n
	return n, err
}

func (f *catalogReadFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return &catalogCountFile{File: file, owner: f}, nil
}

func (f *catalogReadFS) ReadFile(name string) ([]byte, error) {
	f.wholeRead = true
	return fs.ReadFile(f.MapFS, name)
}

func TestCatalogReadRejectsOversizeWithoutWholeFileRead(t *testing.T) {
	f := &catalogReadFS{MapFS: fstest.MapFS{CatalogFile: &fstest.MapFile{Data: bytes.Repeat([]byte("x"), maxCatalogBytes+2)}}}
	if _, err := readCatalogFS(context.Background(), f); err == nil {
		t.Fatal("oversized catalog accepted")
	}
	if f.wholeRead {
		t.Fatal("oversized catalog read in full")
	}
	if f.bytesRead > maxCatalogBytes+1 {
		t.Fatalf("read %d bytes, limit is %d", f.bytesRead, maxCatalogBytes+1)
	}
}
