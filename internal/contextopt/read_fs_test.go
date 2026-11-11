// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

type cancelFS struct {
	fstest.MapFS
	cancel context.CancelFunc
}
type cancelFile struct {
	fs.File
	cancel context.CancelFunc
	reads  int
}

func (f *cancelFile) Read(p []byte) (int, error) {
	f.reads++
	if f.reads == 1 {
		f.cancel()
		p[0] = 'x'
		return 1, nil
	}
	return 0, io.EOF
}
func (f cancelFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return &cancelFile{File: file, cancel: f.cancel}, nil
}

// TestReadFSBetweenChunks proves cancellation is observed after a successful read.
func TestReadFSBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := readFS(ctx, cancelFS{MapFS: fstest.MapFS{"f": &fstest.MapFile{}}, cancel: cancel}, "f", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("between chunks: %v", err)
	}
}

func TestReadFSBoundsAndErrors(t *testing.T) {
	f := fstest.MapFS{"f": &fstest.MapFile{Data: []byte("abc")}}
	for _, tc := range []struct {
		limit int
		want  error
	}{{3, nil}, {2, util.ErrFileTooLarge}, {-1, errors.New("invalid")}} {
		data, err := ReadFS(context.Background(), f, "f", tc.limit)
		if tc.limit < 0 {
			if err == nil {
				t.Fatal("negative limit accepted")
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("limit %d: %v", tc.limit, err)
		}
		if tc.want == nil && string(data) != "abc" {
			t.Fatalf("data: %q", data)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadFS(ctx, f, "f", 3); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "f") {
		t.Fatalf("canceled: %v", err)
	}
	if _, err := ReadFS(context.Background(), f, "missing", 3); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

type blockedDirStatFS struct {
	fstest.MapFS
	started chan struct{}
	release chan struct{}
}

func (f blockedDirStatFS) ReadDir(name string) ([]fs.DirEntry, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return fs.ReadDir(f.MapFS, name)
}

func (f blockedDirStatFS) Stat(name string) (fs.FileInfo, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return fs.Stat(f.MapFS, name)
}

// TestReadDirFSAndStatFS_3D covers positive, negative and cancellation boundary cases for ReadDirFS and StatFS.
func TestReadDirFSAndStatFS_3D(t *testing.T) {
	fsys := fstest.MapFS{"dir/a.txt": &fstest.MapFile{Data: []byte("ok")}}
	entries, err := ReadDirFS(context.Background(), fsys, "dir")
	if err != nil || len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("ReadDirFS positive: %v, %v", entries, err)
	}
	info, err := StatFS(context.Background(), fsys, "dir/a.txt")
	if err != nil || info.IsDir() || info.Size() != 2 {
		t.Fatalf("StatFS positive: %v, %v", info, err)
	}
	if _, err := ReadDirFS(context.Background(), fsys, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDirFS missing: %v", err)
	}
	if _, err := StatFS(context.Background(), nil, "dir"); err == nil || !strings.Contains(err.Error(), "filesystem is required") {
		t.Fatalf("StatFS nil fsys: %v", err)
	}
	for _, op := range []string{"readdir", "stat"} {
		t.Run(op, func(t *testing.T) {
			assertBlockedFSOpCancels(t, op)
		})
	}
}

func assertBlockedFSOpCancels(t *testing.T, op string) {
	t.Helper()
	b := blockedDirStatFS{
		MapFS:   fstest.MapFS{"dir/a.txt": &fstest.MapFile{Data: []byte("ok")}},
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	defer close(b.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var err error
		if op == "readdir" {
			_, err = ReadDirFS(ctx, b, "dir")
		} else {
			_, err = StatFS(ctx, b, "dir/a.txt")
		}
		done <- err
	}()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("operation did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s cancel err = %v, want context.Canceled", op, err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("%s ignored cancellation", op)
	}
}
