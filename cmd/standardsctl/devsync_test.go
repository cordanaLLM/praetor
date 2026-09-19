package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateDevsyncEnv points every per-user directory at temporary folders and rclone at a
// path that does not exist, so no test reads the operator's state or reaches a remote.
func isolateDevsyncEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(home, strings.ToLower(name)))
	}
	t.Setenv(rcloneBinaryEnv, filepath.Join(home, "no-such-rclone"))
	dev := filepath.Join(home, "dev")
	t.Setenv("PRAETOR_DEV_DIR", dev)
	for _, file := range []string{filepath.Join("org", "repo", ".git", "HEAD"), filepath.Join("org", "repo", "main.go")} {
		path := filepath.Join(dev, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dev
}

func TestDevsyncCLIDispatch(t *testing.T) {
	if _, ok := commandTable()["devsync"]; !ok {
		t.Fatal("devsync is not registered")
	}
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		if _, err := captureStdout(t, func() error { return runDevsync(args) }); err != nil {
			t.Fatalf("runDevsync(%q) = %v", args, err)
		}
	}
	if err := runDevsync([]string{"sync"}); err == nil {
		t.Fatal("unknown subcommand accepted")
	}
}

func TestDevsyncCLIRejectsBadArguments(t *testing.T) {
	dev := isolateDevsyncEnv(t)
	for _, args := range [][]string{
		{"init", "extra"},
		{"init", "--remote-name=bad:name"},
		{"push", "--host=a/b"},
		{"push", "--dev=" + filepath.Join(dev, "absent"), "--host=ws1"},
		{"push", "--unknown"},
		{"push", "--max-archive-size=bogus"},
		{"pull"},
		{"pull", "--host=ws1", "--into=" + dev},
		{"ls", "positional"},
	} {
		if _, err := captureStdout(t, func() error { return runDevsync(args) }); err == nil {
			t.Errorf("runDevsync(%q) accepted", args)
		}
	}
}

func TestDevsyncCLIPushDryRun(t *testing.T) {
	isolateDevsyncEnv(t)
	out, err := captureStdout(t, func() error { return runDevsync([]string{"push", "--host=ws1", "--dry-run"}) })
	if err != nil {
		t.Fatal(err, out)
	}
	for _, want := range []string{"would-upload", "ws1/dev/org/repo.tar.gz", "ws1/agent-state.tar.gz"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

func TestDevsyncCLIPushSizeCap(t *testing.T) {
	isolateDevsyncEnv(t)
	out, err := captureStdout(t, func() error {
		return runDevsync([]string{"push", "--host=ws1", "--dry-run", "--max-archive-size=1B"})
	})
	if err != nil {
		t.Fatal(err, out)
	}
	for _, want := range []string{"too-large", "ws1/dev/org/repo.tar.gz", "skipped for size: 1 archive(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("size-capped dry run lacks %q:\n%s", want, out)
		}
	}
}

func TestDevsyncCLIListWithRealRclone(t *testing.T) {
	binary, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone is not installed")
	}
	isolateDevsyncEnv(t)
	t.Setenv(rcloneBinaryEnv, binary)
	remote := filepath.Join(t.TempDir(), "absent")
	config := "--rclone-config=" + filepath.Join(t.TempDir(), "rclone.conf")
	out, err := captureStdout(t, func() error { return runDevsync([]string{"ls", "--remote=" + remote, config}) })
	if err != nil || !strings.Contains(out, "No archives") {
		t.Fatalf("ls of an empty remote = %q, %v", out, err)
	}
}
