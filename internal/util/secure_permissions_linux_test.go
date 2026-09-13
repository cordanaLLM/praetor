package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSecurePermissions_RestrictiveUmask(t *testing.T) {
	const marker = "PRAETOR_TEST_SECURE_UMASK"
	if os.Getenv(marker) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		runPermissionTestChild(t, executable, t.Name(), marker+"=1", nil)
		return
	}
	// This test runs alone in a subprocess: umask never changes the parent suite.
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	root := t.TempDir()
	file := filepath.Join(root, "private.txt")
	if err := WriteFileSecure(file, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, file, 0o600)
	nested := filepath.Join(root, "new", "leaf")
	if err := MkdirSecure(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	assertSecureMode(t, filepath.Dir(nested), 0o700)
	assertSecureMode(t, nested, 0o700)
}

func TestWriteFileSecure_MetadataFailurePreservesContent(t *testing.T) {
	const marker = "PRAETOR_TEST_SECURE_FOREIGN_DIR"
	if root := os.Getenv(marker); root != "" {
		verifyForeignOwnedWrites(t, root)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to launch a fixture subprocess with a different uid")
	}
	root := t.TempDir()
	for _, path := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"tighten", "preserve"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("original content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o660); err != nil {
			t.Fatal(err)
		}
	}
	executable := copyPermissionTestExecutable(t, root)
	credential := &syscall.Credential{Uid: 65534, Gid: uint32(os.Getgid()), NoSetGroups: true}
	runPermissionTestChild(t, executable, t.Name(), marker+"="+root, credential)
}

func verifyForeignOwnedWrites(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "tighten")
	if err := WriteFileSecure(path, []byte("replacement"), 0o600); !errors.Is(err, os.ErrPermission) {
		t.Errorf("expected a permission error for a file owned by a different uid, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original content" {
		t.Errorf("failed metadata change modified content: %q, %v", data, err)
	}
	path = filepath.Join(root, "preserve")
	if err := WriteFileSecure(path, []byte("replacement"), 0o660); err != nil {
		t.Fatalf("mode-preserving shared write: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Errorf("shared write content: %q, %v", data, err)
	}
	assertSecureMode(t, path, 0o660)
}

func copyPermissionTestExecutable(t *testing.T, root string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(root, "permissions.test")
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return copyPath
}

func runPermissionTestChild(t *testing.T, executable, name, environment string, credential *syscall.Credential) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+name+"$", "-test.v")
	cmd.Env = append(os.Environ(), environment)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("permission fixture failed: %v\n%s", err, output)
	}
}
