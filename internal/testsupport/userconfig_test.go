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

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonicalRoot = root
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canonicalDir = dir
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("os.UserConfigDir %s does not lie under root %s (rel: %s)", dir, root, rel)
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
