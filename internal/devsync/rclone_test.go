package devsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemotePath(t *testing.T) {
	cases := []struct{ remote, want string }{
		{"praetor-sync:", "praetor-sync:h/dev/a.tar.gz"},
		{"praetor-sync:sub", "praetor-sync:sub/h/dev/a.tar.gz"},
		{"/tmp/store/", "/tmp/store/h/dev/a.tar.gz"},
		{"/tmp/store", "/tmp/store/h/dev/a.tar.gz"},
	}
	for _, c := range cases {
		if got := remotePath(c.remote, "h", "dev/a.tar.gz"); got != c.want {
			t.Errorf("remotePath(%q) = %q, want %q", c.remote, got, c.want)
		}
	}
	if err := validateRemotePath("-x:"); err == nil {
		t.Error("remote path starting with '-' accepted")
	}
	if err := validateRemotePath("r:it's"); err == nil {
		t.Error("remote path with a quote accepted")
	}
}

func TestUploadCause(t *testing.T) {
	produce, stream := errors.New("walk failed"), errors.New("rclone failed")
	stopped := fmt.Errorf("write: %w", io.ErrClosedPipe)
	cases := []struct{ produce, stream, want error }{
		{nil, nil, nil},
		{produce, stream, produce},
		{stopped, stream, stream},
		{nil, stream, stream},
		{stopped, nil, stopped},
	}
	for _, c := range cases {
		if got := uploadCause(c.produce, c.stream); !errors.Is(got, c.want) || (c.want == nil) != (got == nil) {
			t.Errorf("uploadCause(%v, %v) = %v, want %v", c.produce, c.stream, got, c.want)
		}
	}
}

func TestExitCode(t *testing.T) {
	if code := exitCode(errors.New("not an exit")); code != -1 {
		t.Fatalf("exit code of a plain error = %d", code)
	}
	if code := exitCode(nil); code != -1 {
		t.Fatalf("exit code of nil = %d", code)
	}
	ctx, rclone, _ := newFakeRclone(t, "")
	_, err := rclone.run(ctx, "cat", "praetor-sync:missing")
	if code := exitCode(err); code != rcloneDirNotFound {
		t.Fatalf("exit code = %d (%v), want %d", code, err, rcloneDirNotFound)
	}
}

func TestDownloadStopsRcloneWhenConsumerFails(t *testing.T) {
	ctx, rclone, store := newFakeRclone(t, "")
	writeTestFile(t, filepath.Join(store, "data", "big.tar.gz"), strings.Repeat("x", 1<<20))
	sentinel := errors.New("consumer refused")
	err := rclone.download(ctx, "praetor-sync:big.tar.gz", func(io.Reader) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("download = %v, want the consumer's error", err)
	}
	err = rclone.download(ctx, "praetor-sync:absent.tar.gz", func(r io.Reader) error {
		_, copyErr := io.Copy(io.Discard, r)
		return copyErr
	})
	if exitCode(err) != rcloneDirNotFound {
		t.Fatalf("missing archive = %v", err)
	}
}

// rcloneOnPath returns the installed rclone or skips: the fake covers the logic everywhere,
// this test pins devsync to real rclone behaviour where rclone exists.
func rcloneOnPath(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed; the fake rclone tests cover the same logic")
	}
	return binary
}

// TestRealRcloneRoundTrip runs init, push, ls and pull against real rclone with a temporary
// config file and a local directory as the crypt base: it never reads the operator's rclone
// configuration and never reaches a network remote.
func TestRealRcloneRoundTrip(t *testing.T) {
	rclone := Rclone{Binary: rcloneOnPath(t), Config: filepath.Join(t.TempDir(), "rclone.conf")}
	ctx := context.Background()
	base := t.TempDir()
	if err := Init(ctx, InitOptions{Base: base, Rclone: rclone}); err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, InitOptions{Base: base, Rclone: rclone}); !errors.Is(err, ErrRemoteExists) {
		t.Fatalf("second init = %v", err)
	}
	dev := makeDevTree(t)
	opts := pushOptions(t, rclone, dev, nil)
	if _, err := Push(ctx, opts); err != nil {
		t.Fatal(err)
	}
	assertEncrypted(t, base)
	archives, err := List(ctx, ListOptions{Remote: DefaultRemote, Rclone: rclone})
	if err != nil || len(archives) != 6 || archives[0].Host != "ws1" {
		t.Fatalf("list = %+v, %v", archives, err)
	}
	into := t.TempDir()
	if _, err := Pull(ctx, PullOptions{Remote: DefaultRemote, Host: "ws1", Into: into, DevDir: dev, Rclone: rclone}); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, filepath.Join(into, "ws1", "dev", "org", "app", "main.go")) != "package main\n" {
		t.Fatal("restored tree differs")
	}
}

// assertEncrypted checks that the crypt base holds no plaintext archive or folder names.
func assertEncrypted(t *testing.T, base string) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if strings.HasSuffix(name, archiveSuffix) || name == "ws1" || name == devFolder {
			return fmt.Errorf("plaintext name %s on the crypt base", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRealRclonePlainDirectoryRemote(t *testing.T) {
	rclone := Rclone{Binary: rcloneOnPath(t), Config: filepath.Join(t.TempDir(), "rclone.conf")}
	remote := t.TempDir()
	opts := pushOptions(t, rclone, makeDevTree(t), nil)
	opts.Remote = remote
	if _, err := Push(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote, "ws1", "dev", "solo.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if archives, err := List(context.Background(), ListOptions{Remote: filepath.Join(remote, "absent"), Rclone: rclone}); err != nil || len(archives) != 0 {
		t.Fatalf("absent directory remote = %v, %v", archives, err)
	}
}
