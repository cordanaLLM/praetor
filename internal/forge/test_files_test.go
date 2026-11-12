// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeForgeTestFile writes one fixture file under root, creating its directories.
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

// assertPositiveTestFileConventions checks that Go, Rust and spec test files are recognised under a live context.
func assertPositiveTestFileConventions(t *testing.T, root string) {
	t.Helper()
	for _, rel := range []string{"pkg/a_test.go", "tests/integration.rs", "src/unit_test.rs", "src/inline.rs", "app.spec.ts"} {
		ok, err := IsTestFileContext(t.Context(), root, rel)
		if err != nil || !ok || !IsTestFile(root, rel) {
			t.Fatalf("IsTestFileContext(%q) = (%v, %v), IsTestFile=%v; want true", rel, ok, err, IsTestFile(root, rel))
		}
	}
}

// assertBoundaryNonTestFiles checks that a plain Rust source, a missing file and a README are not test files.
func assertBoundaryNonTestFiles(t *testing.T, root string) {
	t.Helper()
	for _, rel := range []string{"src/plain.rs", "src/missing.rs", "README.md"} {
		ok, err := IsTestFileContext(t.Context(), root, rel)
		if err != nil || ok || IsTestFile(root, rel) {
			t.Fatalf("IsTestFileContext(%q) = (%v, %v), IsTestFile=%v; want false, nil", rel, ok, err, IsTestFile(root, rel))
		}
	}
}

// assertNegativeContextTestFileChecks checks that a nil or cancelled context stops the Rust check with an error.
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

// TestIsTestFileContext_RefusedRustSource_3D verifies that a Rust source the snapshot reader refuses
// (a symlink leaf, non-UTF-8 text) returns an error wrapping ErrRustSourceRefused that names the path,
// while a cancelled context still returns the context error unchanged and a readable source is unaffected.
func TestIsTestFileContext_RefusedRustSource_3D(t *testing.T) {
	root := t.TempDir()
	writeForgeTestFile(t, root, "src/real.rs", "#[cfg(test)]\nmod tests {}\n")
	writeForgeTestFile(t, root, "src/binary.rs", "\xff\xfe#[cfg(test)]\n")
	if err := os.Symlink("real.rs", filepath.Join(root, "src", "link.rs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Run("negative symlink leaf and non-UTF-8 source are refused with the path", func(t *testing.T) {
		assertRustSourcesRefused(t, root, "src/link.rs", "src/binary.rs")
	})
	t.Run("boundary context errors pass through unchanged", func(t *testing.T) {
		assertContextErrorsUnchanged(t, root)
	})
	t.Run("positive readable source is still a test file", func(t *testing.T) {
		if ok, err := IsTestFileContext(t.Context(), root, "src/real.rs"); err != nil || !ok {
			t.Fatalf("IsTestFileContext(real.rs) = (%v, %v); want true, nil", ok, err)
		}
	})
}

// assertRustSourcesRefused checks that each rel is reported as refused, wrapping ErrRustSourceRefused and naming the path.
func assertRustSourcesRefused(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		ok, err := IsTestFileContext(t.Context(), root, rel)
		if ok || !errors.Is(err, ErrRustSourceRefused) || !strings.Contains(err.Error(), rel) {
			t.Fatalf("IsTestFileContext(%q) = (%v, %v); want false and an error wrapping ErrRustSourceRefused naming the path", rel, ok, err)
		}
	}
}

// assertContextErrorsUnchanged checks that contextReadError and IsTestFileContext return context errors without wrapping them as a refusal.
func assertContextErrorsUnchanged(t *testing.T, root string) {
	t.Helper()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := contextReadError(t.Context(), "src/x.rs", cause); got != cause { //nolint:errorlint // identity: the context error must not be wrapped
			t.Fatalf("contextReadError(%v) = %v; want the error unchanged", cause, got)
		}
	}
	if err := contextReadError(t.Context(), "src/x.rs", nil); err != nil {
		t.Fatalf("contextReadError(nil) = %v on a live context; want nil", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ok, err := IsTestFileContext(ctx, root, "src/link.rs"); ok || !errors.Is(err, context.Canceled) || errors.Is(err, ErrRustSourceRefused) {
		t.Fatalf("cancelled IsTestFileContext = (%v, %v); want context.Canceled, not a refusal", ok, err)
	}
}
