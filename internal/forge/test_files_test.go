// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeForgeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-mkdir -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestIsTestFileContext_RustAndConventions_3D verifies positive, boundary, and negative cases for
// IsTestFileContext and IsTestFile, including stopping on a cancelled context before reading Rust files.
func TestIsTestFileContext_RustAndConventions_3D(t *testing.T) {
	root := t.TempDir()
	writeForgeTestFile(t, root, "src/inline.rs", "pub fn add() -> i32 { 1 }\n#[cfg(test)]\nmod tests {}\n")
	writeForgeTestFile(t, root, "src/plain.rs", "pub fn add() -> i32 { 1 }\n")

	t.Run("positive conventions and inline Rust cfg(test)", func(t *testing.T) {
		assertPositiveTestFileConventions(t, root)
	})
	t.Run("boundary non-test and missing Rust files return false without error", func(t *testing.T) {
		assertBoundaryNonTestFiles(t, root)
	})
	t.Run("negative cancelled or nil context stops Rust check with error", func(t *testing.T) {
		assertNegativeContextTestFileChecks(t, root)
	})
}

func assertPositiveTestFileConventions(t *testing.T, root string) {
	t.Helper()
	for _, rel := range []string{"pkg/a_test.go", "tests/integration.rs", "src/unit_test.rs", "src/inline.rs", "app.spec.ts"} {
		ok, err := IsTestFileContext(t.Context(), root, rel)
		if err != nil || !ok || !IsTestFile(root, rel) {
			t.Fatalf("IsTestFileContext(%q) = (%v, %v), IsTestFile=%v; want true", rel, ok, err, IsTestFile(root, rel))
		}
	}
}

func assertBoundaryNonTestFiles(t *testing.T, root string) {
	t.Helper()
	for _, rel := range []string{"src/plain.rs", "src/missing.rs", "README.md"} {
		ok, err := IsTestFileContext(t.Context(), root, rel)
		if err != nil || ok || IsTestFile(root, rel) {
			t.Fatalf("IsTestFileContext(%q) = (%v, %v), IsTestFile=%v; want false, nil", rel, ok, err, IsTestFile(root, rel))
		}
	}
}

func assertNegativeContextTestFileChecks(t *testing.T, root string) {
	t.Helper()
	if ok, err := IsTestFileContext(nil, root, "src/inline.rs"); err == nil || ok { //nolint:staticcheck // deliberate nil context negative test
		t.Fatalf("expected error for nil context, got ok=%v err=%v", ok, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ok, err := IsTestFileContext(ctx, root, "src/inline.rs"); !errors.Is(err, context.Canceled) || ok {
		t.Fatalf("expected context.Canceled on cancelled context before reading inline.rs, got ok=%v err=%v", ok, err)
	}
}
