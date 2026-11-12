// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const maxRustTestReadBytes = 64 * 1024

// IsTestFileContext reports whether rel, a path below root, is a test file by its language's own
// naming convention while honoring ctx on any Rust source read.
func IsTestFileContext(ctx context.Context, root, rel string) (bool, error) {
	if ctx == nil {
		return false, errors.New("test file check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return classifyTestFile(root, rel, func(r, slash string) (bool, error) {
		return isRustTestContext(ctx, r, slash)
	})
}

// IsTestFile reports whether rel, a path below root, is a test file by its language's own
// naming convention: Go and Python, JavaScript and TypeScript markers, Rust test folders or a
// #[cfg(test)] module, C, C++, Java, Kotlin, Swift and C#, Ruby, shell, Dart, PHP and Lua. It
// is the one definition the pinned-fixture audit and the release test levels gate share.
func IsTestFile(root, rel string) bool {
	ok, err := classifyTestFile(root, rel, isRustTest)
	return err == nil && ok
}

func classifyTestFile(root, rel string, rustCheck func(string, string) (bool, error)) (bool, error) {
	slash := path.Clean(filepath.ToSlash(rel))
	base := strings.ToLower(path.Base(slash))
	if strings.HasSuffix(base, ".rs") {
		if strings.HasSuffix(base, "_test.rs") || isRustTestDir(slash) {
			return true, nil
		}
		return rustCheck(root, slash)
	}
	return isGoOrPythonTest(base) || isJSTSTest(base) || isCompiledTest(base) || isScriptTest(base), nil
}

func isRustTestDir(slash string) bool {
	return strings.HasPrefix(slash, "tests/") || strings.Contains(slash, "/tests/")
}

func contextReadError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ctx.Err()
}

// isRustTestContext checks whether a Rust source file below root contains a #[cfg(test)] module
// using the repository's confined context-bounded snapshot reader.
func isRustTestContext(ctx context.Context, root, slash string) (bool, error) {
	if !filepath.IsLocal(filepath.FromSlash(slash)) {
		return false, nil
	}
	data, exists, readErr := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(slash))
	if ctxErr := contextReadError(ctx, readErr); ctxErr != nil {
		return false, ctxErr
	}
	return readErr == nil && exists && bytes.Contains(data, []byte("#[cfg(test)]")), nil
}

// isRustTest reports whether a Rust source file at slash in root contains a #[cfg(test)] module.
func isRustTest(root, slash string) (bool, error) {
	data, readErr := util.ReadConfinedLimited(root, filepath.FromSlash(slash), maxRustTestReadBytes)
	return readErr == nil && bytes.Contains(data, []byte("#[cfg(test)]")), nil
}

// isGoOrPythonTest checks Go and Python test file conventions.
func isGoOrPythonTest(base string) bool {
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	if strings.HasSuffix(base, ".py") {
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	}
	return false
}

// isJSTSTest checks JavaScript and TypeScript test file conventions.
func isJSTSTest(base string) bool {
	for _, marker := range []string{".test.", ".spec.", ".integration.", ".e2e.", ".cy."} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

// isCompiledTest checks C, C++, Java, Kotlin, Swift, and C# test conventions.
func isCompiledTest(base string) bool {
	for _, suf := range []string{"_test.cpp", "_test.cc", "_test.cxx", "_test.c", "test.java", "test.kt", "test.cs", "test.swift", "tests.swift"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return strings.HasPrefix(base, "test_") && (strings.HasSuffix(base, ".cpp") || strings.HasSuffix(base, ".cc") || strings.HasSuffix(base, ".c"))
}

// isScriptTest checks Ruby, Shell, Dart, PHP, and Lua test conventions.
func isScriptTest(base string) bool {
	for _, suf := range []string{"_test.rb", "_spec.rb", "_test.sh", ".bats", "_test.dart", "test.php", "_test.lua", "_spec.lua"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return strings.HasPrefix(base, "test_") && (strings.HasSuffix(base, ".rb") || strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".php"))
}
