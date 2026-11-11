package hisscoverage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type blockedFS struct {
	fstest.MapFS
	target  string
	started chan struct{}
	release chan struct{}
}
type blockedFile struct {
	fs.File
	started chan struct{}
	release chan struct{}
}

func (f blockedFile) Read(p []byte) (int, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return f.File.Read(p)
}
func (f blockedFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil || name != f.target {
		return file, err
	}
	return blockedFile{file, f.started, f.release}, nil
}

func (f blockedFS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(file)
	return data, errors.Join(readErr, file.Close())
}

func TestReplayReadCancellation(t *testing.T) {
	const fixture = ".config/hiss/testdata/HISS-01/go/positive/recursion.go"
	for _, name := range [...]string{CatalogFile, fixture} {
		t.Run(name, func(t *testing.T) {
			f := blockedFS{MapFS: fstest.MapFS{name: &fstest.MapFile{Data: []byte(recursiveGo)}}, target: name, started: make(chan struct{}, 1), release: make(chan struct{})}
			defer close(f.release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if name == CatalogFile {
					_, err := readCatalogFS(ctx, f)
					done <- err
				} else {
					_, err := (fixtureStage{ruleID: "HISS-01", language: "go"}).reported(ctx, f, ".config/hiss/testdata/HISS-01/go/positive", "recursion.go")
					done <- err
				}
			}()
			select {
			case <-f.started:
			case <-time.After(time.Second):
				t.Fatal("read did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation: %v", err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("blocked read ignored cancellation")
			}
		})
	}
}

func TestReplayReadControlAndCatalogBoundary(t *testing.T) {
	for _, size := range []int{maxCatalogBytes, maxCatalogBytes + 1} {
		data, err := readCatalogFS(context.Background(), fstest.MapFS{CatalogFile: &fstest.MapFile{Data: bytes.Repeat([]byte("x"), size)}})
		if size == maxCatalogBytes && (err != nil || len(data) != size) {
			t.Fatalf("exact bound: %d, %v", len(data), err)
		}
		if size > maxCatalogBytes && (err == nil || !strings.Contains(err.Error(), "exceeds")) {
			t.Fatalf("oversize: %v", err)
		}
	}
	const path = ".config/hiss/testdata/HISS-01/go/positive/recursion.go"
	report, err := VerifyFS(context.Background(), fstest.MapFS{path: &fstest.MapFile{Data: []byte(recursiveGo)}}, catalogFor("HISS-01", "go", StatePartial))
	if err != nil || !report.Passed() || report.Fixtures != 1 {
		t.Fatalf("fixture replay: %+v, %v", report, err)
	}
}

type blockedReadDirFS struct {
	fstest.MapFS
	started chan struct{}
	release chan struct{}
}

func (f blockedReadDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return fs.ReadDir(f.MapFS, name)
}

// waitForReadDirStart waits up to timeout for ReadDir to report entry on started.
func waitForReadDirStart(started <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-started:
		return true
	case <-time.After(timeout):
		return false
	}
}

// assertBlockedReadDirCancels verifies VerifyFS waits for ReadDir entry and exits on cancellation.
func assertBlockedReadDirCancels(t *testing.T) {
	t.Helper()
	f := blockedReadDirFS{
		MapFS:   fstest.MapFS{},
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	defer close(f.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := VerifyFS(ctx, f, catalogFor("HISS-01", "go", StatePartial))
		done <- err
	}()
	if !waitForReadDirStart(f.started, time.Second) {
		t.Fatal("ReadDir did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("VerifyFS on blocked ReadDir = %v, want context.Canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked ReadDir ignored context cancellation")
	}
}

// TestReplayContextBoundsReadDirAndAbsentProbe verifies pre-cancelled and expired contexts
// return without filesystem entry and blocked ReadDir cancels after the started handshake.
func TestReplayContextBoundsReadDirAndAbsentProbe(t *testing.T) {
	t.Run("cancelled_before_absent_bucket", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := VerifyFS(ctx, fstest.MapFS{}, catalogFor("HISS-01", "go", StatePartial))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("VerifyFS with cancelled ctx on absent bucket = %v, want context.Canceled", err)
		}
	})
	t.Run("expired_deadline", func(t *testing.T) {
		f := blockedReadDirFS{
			MapFS:   fstest.MapFS{},
			started: make(chan struct{}, 1),
			release: make(chan struct{}),
		}
		defer close(f.release)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
		defer cancel()
		_, err := VerifyFS(ctx, f, catalogFor("HISS-01", "go", StatePartial))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("VerifyFS with expired deadline = %v, want context.DeadlineExceeded", err)
		}
		if len(f.started) != 0 {
			t.Fatal("VerifyFS entered ReadDir despite already-expired deadline")
		}
	})
	t.Run("blocked_readdir_canceled", assertBlockedReadDirCancels)
	t.Run("unstarted_readdir_fails_wait", func(t *testing.T) {
		if waitForReadDirStart(make(chan struct{}), time.Millisecond) {
			t.Fatal("waitForReadDirStart succeeded without ReadDir starting")
		}
	})
}
