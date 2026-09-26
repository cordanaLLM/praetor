// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package repairrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// realGoToolchain returns the go binary this test process can run and the GOROOT that binary
// reports, both symlink-resolved. `go test` puts GOROOT/bin first on PATH, so LookPath here
// finds the real binary rather than a distro link to it.
func realGoToolchain(t *testing.T) (string, string) {
	t.Helper()
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go is not on PATH here: %v", err)
	}
	data, err := exec.CommandContext(t.Context(), goPath, "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	resolvedGo, err := filepath.EvalSymlinks(goPath)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return resolvedGo, resolvedRoot
}

// skipWithoutPosixLinks skips where the fixture cannot be built: creating a symlink needs
// Developer Mode or elevation on Windows, and a shebang script is not executable there. The
// function under test only feeds the Linux bubblewrap sandbox (verificationArguments), so the
// Linux and macOS legs carry the coverage.
func skipWithoutPosixLinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink and shebang fixtures need POSIX semantics; the sandbox path is Linux-only")
	}
}

// Positive: go reached through a symlink on PATH -- /usr/bin/go -> /usr/lib/go/bin/go on Arch
// and Debian, Homebrew's /opt/homebrew/bin/go -- resolves to the real binary inside the GOROOT
// it reports, so the sandbox binds that root and runs the binary it contains. Comparing the
// link itself against GOROOT refused every such host.
func TestGoToolchainRootFollowsASymlinkedGoOnPath(t *testing.T) {
	skipWithoutPosixLinks(t)
	realGo, realRoot := realGoToolchain(t)
	linkDir := t.TempDir()
	if err := os.Symlink(realGo, filepath.Join(linkDir, "go")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", linkDir)

	goPath, root, err := goToolchainRoot(t.Context())
	if err != nil {
		t.Fatalf("a symlinked go on PATH must resolve, got %v", err)
	}
	if root != realRoot {
		t.Errorf("root = %q, want the resolved GOROOT %q", root, realRoot)
	}
	if goPath != realGo {
		t.Errorf("goPath = %q, want the resolved binary %q, not the link", goPath, realGo)
	}
	if !strings.HasPrefix(goPath, root+string(filepath.Separator)) {
		t.Errorf("goPath %q must sit under the root %q the sandbox binds", goPath, root)
	}
}

// Negative: a host without go says so, naming the tool, before any sandbox is assembled.
func TestGoToolchainRootReportsAMissingGo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := goToolchainRoot(t.Context())
	if err == nil {
		t.Fatal("a host without go must be reported, not discovered inside the sandbox")
	}
	if !strings.Contains(err.Error(), "go on PATH") {
		t.Errorf("the error must name the cause, got %v", err)
	}
}

// Boundary: the reported GOROOT is trusted only as far as the layout contract holds. A root
// that does not contain the resolved binary, a relative root, an empty report and a root that
// does not exist are each refused with their own reason.
func TestGoToolchainRootRejectsAReportedRootItCannotBind(t *testing.T) {
	skipWithoutPosixLinks(t)
	elsewhere := t.TempDir()
	for name, tc := range map[string]struct {
		reported string
		want     string
	}{
		"root that does not contain the binary": {elsewhere, "not under its own GOROOT"},
		"relative root":                         {"relative/goroot", "invalid installed Go toolchain root"},
		"empty report":                          {"", "invalid installed Go toolchain root"},
		"root that does not exist":              {filepath.Join(elsewhere, "absent"), "resolve installed Go toolchain root"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + tc.reported + "'\n"
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			_, _, err := goToolchainRoot(t.Context())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("reported GOROOT %q: err = %v, want %q", tc.reported, err, tc.want)
			}
		})
	}
}

// The resolution is shared, not copied: git and go name their own tool when PATH lacks it.
func TestPathBinaryNamesTheMissingTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"git", "go"} {
		if _, err := pathBinary(name); err == nil || !strings.Contains(err.Error(), "requires "+name+" on PATH") {
			t.Errorf("pathBinary(%q) = %v, want the missing tool named", name, err)
		}
	}
}
