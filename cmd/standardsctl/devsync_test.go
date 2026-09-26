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
	t.Setenv(devRootEnv, dev)
	t.Setenv(legacyDevRootEnv, "")
	writeDevsyncRepo(t, dev, "org", "repo")
	return dev
}

// writeDevsyncRepo creates the git repository <dev>/<org>/<name> holding one file.
func writeDevsyncRepo(t *testing.T, dev, org, name string) {
	t.Helper()
	for _, file := range []string{filepath.Join(org, name, ".git", "HEAD"), filepath.Join(org, name, "main.go")} {
		path := filepath.Join(dev, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDevsyncCLIDevRootEnv(t *testing.T) {
	legacy := isolateDevsyncEnv(t)
	canonical := t.TempDir()
	writeDevsyncRepo(t, canonical, "team", "app")
	push := func() (string, error) {
		return captureStdout(t, func() error { return runDevsync([]string{"push", "--host=ws1", "--dry-run"}) })
	}

	// Boundary: only the earlier PRAETOR_DEV_DIR name set still selects the tree.
	t.Setenv(devRootEnv, "")
	t.Setenv(legacyDevRootEnv, legacy)
	out, err := push()
	if err != nil {
		t.Fatal(err, out)
	}
	mustContain(t, out, "ws1/dev/org/repo.tar.gz")

	// Positive + boundary: PRAETOR_DEV_ROOT is honoured and wins when both are set.
	t.Setenv(devRootEnv, canonical)
	out, err = push()
	if err != nil {
		t.Fatal(err, out)
	}
	mustContain(t, out, "ws1/dev/team/app.tar.gz")
	if strings.Contains(out, "org/repo") {
		t.Fatalf("push read PRAETOR_DEV_DIR although PRAETOR_DEV_ROOT is set:\n%s", out)
	}

	// Positive: pull guards the PRAETOR_DEV_ROOT tree, not <home>/dev.
	_, err = captureStdout(t, func() error { return runDevsync([]string{"pull", "--host=ws1", "--into=" + canonical}) })
	if err == nil || !strings.Contains(err.Error(), "inside the dev folder") {
		t.Fatalf("pull into PRAETOR_DEV_ROOT = %v; want the dev-folder refusal", err)
	}

	// Negative: without any dev root push and pull fail instead of using ./dev.
	t.Setenv(devRootEnv, "")
	t.Setenv(legacyDevRootEnv, "")
	clearHomeDir(t)
	if _, err := push(); err == nil || !strings.Contains(err.Error(), "--dev") {
		t.Fatalf("push without a dev root = %v; want an error naming --dev", err)
	}
	_, err = captureStdout(t, func() error { return runDevsync([]string{"pull", "--host=ws1", "--into=" + t.TempDir()}) })
	if err == nil || !strings.Contains(err.Error(), devRootEnv) {
		t.Fatalf("pull without a dev root = %v; want an error naming %s", err, devRootEnv)
	}
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
