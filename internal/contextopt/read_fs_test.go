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
