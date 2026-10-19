// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolateUserConfig_Positive_UserConfigDirUnderRoot(t *testing.T) {
	root, cleanup, err := IsolateUserConfig()
	if err != nil {
		t.Fatalf("IsolateUserConfig: %v", err)
	}
	defer cleanup()

	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}

	rel, under, err := PathUnder(root, dir)
	if err != nil || !under {
		t.Fatalf("os.UserConfigDir %s does not lie under root %s (rel: %s, %v)", dir, root, rel, err)
	}
}

func TestIsolateUserConfig_Negative_DirectoryCreationError(t *testing.T) {
	orig := mkdirTempHook
	defer func() { mkdirTempHook = orig }()
	mkdirTempHook = func(dir, pattern string) (string, error) {
		return "", errors.New("simulated mkdir error")
	}

	root, cleanup, err := IsolateUserConfig()
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("expected error creating temp config directory, got nil")
	}
	if root != "" {
		t.Fatalf("expected empty root on error, got %q", root)
	}
	if !strings.Contains(err.Error(), "simulated mkdir error") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestIsolateUserConfig_Boundary_CleanupRemovesDirectory(t *testing.T) {
	root, cleanup, err := IsolateUserConfig()
	if err != nil {
		t.Fatalf("IsolateUserConfig: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("temp directory %s should exist before cleanup: %v", root, err)
	}
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("temp directory %s should be removed by cleanup, got err: %v", root, err)
	}
}

// TestIsolateUserConfig_Positive_GoCachesStayOutside: while HOME points at the temporary root, the Go
// caches keep their places, so a `go` command a test runs never puts a module cache under the root.
func TestIsolateUserConfig_Positive_GoCachesStayOutside(t *testing.T) {
	before := goCaches()
	unset, wasSet := os.LookupEnv("GOMODCACHE")
	root, cleanup, err := IsolateUserConfig()
	if err != nil {
		t.Fatalf("IsolateUserConfig: %v", err)
	}
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		got := os.Getenv(key)
		if want, ok := before[key]; ok && got != want {
			t.Errorf("%s = %q under isolation, want %q", key, got, want)
		}
		_, under, pathErr := PathUnder(root, got)
		if pathErr != nil {
			t.Fatalf("PathUnder(%s, %s): %v", root, got, pathErr)
		}
		if got != "" && under {
			t.Errorf("%s = %q lies under the temporary root %s", key, got, root)
		}
	}
	cleanup()
	if now, set := os.LookupEnv("GOMODCACHE"); set != wasSet || now != unset {
		t.Errorf("GOMODCACHE not restored: %q (set %v), want %q (set %v)", now, set, unset, wasSet)
	}
}

// TestIsolateUserConfig_Boundary_CleanupRemovesReadOnlyTree: a read-only module-cache-like tree under
// the root (the 2026-10-19 leak: 937 directories that os.RemoveAll left behind) is removed.
func TestIsolateUserConfig_Boundary_CleanupRemovesReadOnlyTree(t *testing.T) {
	root, cleanup, err := IsolateUserConfig()
	if err != nil {
		t.Fatalf("IsolateUserConfig: %v", err)
	}
	mod := filepath.Join(root, "go", "pkg", "mod", "example.com", "m@v1.0.0")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	for dir := mod; dir != root; dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	cleanup()
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only tree under %s survived the cleanup: %v", root, err)
	}
}

// TestIsolateUserConfig_Positive_HelperProcessReusesRoot: a process that inherits an isolated root
// (a helper re-run of the test binary) gets it back with a no-op cleanup, never a nested directory.
func TestIsolateUserConfig_Positive_HelperProcessReusesRoot(t *testing.T) {
	root, cleanup, err := IsolateUserConfig()
	if err != nil {
		t.Fatalf("IsolateUserConfig: %v", err)
	}
	defer cleanup()
	if os.Getenv(IsolatedRootEnv) != root {
		t.Fatalf("%s = %q, want the isolated root %q", IsolatedRootEnv, os.Getenv(IsolatedRootEnv), root)
	}
	again, noop, err := IsolateUserConfig()
	if err != nil || again != root {
		t.Fatalf("an inherited root must be reused: got %q, %v", again, err)
	}
	noop()
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the reuse cleanup removed the parent's root: %v", err)
	}
}
