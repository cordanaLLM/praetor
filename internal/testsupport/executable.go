// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package testsupport holds test fixtures whose behaviour depends on the host, written once so
// that each test body asserts the same thing on every platform (HISS-21).
//
// BuildExecutable builds stand-in executables for tests that must control what a named tool
// does: a git that hangs, a pnpm that rewrites a file, a go that reports a GOPATH. Tests used
// to write those stand-ins as "#!/bin/sh" scripts. Windows does not execute a script by its
// shebang, and exec.LookPath does not find an extensionless file there, so every such test
// either ran the real tool in the shim's place or could not start it at all, and failed for a
// reason unrelated to what it asserts. A shim built from Go source runs the same program on
// every platform, so one test body carries one behaviour everywhere.
package testsupport

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// buildTimeout bounds a single shim compilation (HISS-02). A warm build takes well under a
// second; the bound exists for a cold module cache on a slow runner.
const buildTimeout = 2 * time.Minute

// ExecutableName returns the file name an executable called name has on this platform:
// name.exe on Windows, where exec.LookPath resolves a bare name only through PATHEXT, and
// name elsewhere.
func ExecutableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// BuildExecutable compiles source, a complete Go main package, into dir as the executable
// called name and returns its path. Call it before narrowing PATH: it locates the go command
// through the caller's PATH.
func BuildExecutable(t testing.TB, dir, name, source string) string {
	t.Helper()
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("testsupport: building %s needs the go command on PATH: %v", name, err)
	}
	src := t.TempDir()
	mainFile := filepath.Join(src, "main.go")
	if err := os.WriteFile(mainFile, []byte(source), 0o600); err != nil {
		t.Fatalf("testsupport: write %s source: %v", name, err)
	}
	output := filepath.Join(dir, ExecutableName(name))
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	// Building a single file needs no go.mod, so the shim never inherits a go directive or a
	// toolchain requirement from the module under test.
	// The compiler's diagnostics are on standard error, which RunCommand carries in err.
	if _, err := util.RunCommand(ctx, src, goCommand, "build", "-o", output, mainFile); err != nil {
		t.Fatalf("testsupport: build %s: %v", name, err)
	}
	return output
}
