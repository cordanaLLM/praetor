// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package repairrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: git is resolved from the caller's PATH, so the package runs wherever git lives.
// A hardcoded /usr/bin/git failed twelve cases on Windows for that reason alone (#134).
func TestGitBinaryResolvesFromPath(t *testing.T) {
	resolved, err := gitBinary()
	if err != nil {
		t.Skipf("git is not on PATH here: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Errorf("the child must receive an absolute path, got %q", resolved)
	}
	expected, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expected {
		t.Errorf("gitBinary() = %q, want the PATH resolution %q", resolved, expected)
	}
}

// Negative: a host without git must say so rather than failing later with a confusing exec error.
func TestGitBinaryReportsAMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := gitBinary(); err == nil {
		t.Error("a host without git must be reported, not discovered mid-operation")
	} else if !strings.Contains(err.Error(), "git on PATH") {
		t.Errorf("the error must name the cause, got %v", err)
	}
}

// The hardening is the point of this package's environment, so it must survive the fix.
func TestGitEnvironmentStaysScrubbed(t *testing.T) {
	env := gitEnvironment(filepath.Join("/opt", "tools", "bin", "git"))
	seen := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("malformed environment entry %q", entry)
		}
		seen[key] = value
	}
	for key, want := range map[string]string{
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_ATTR_NOSYSTEM":      "1",
		"GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_TERMINAL_PROMPT":    "0",
		"GIT_ALLOW_PROTOCOL":     "",
		"LANG":                   "C.UTF-8",
	} {
		if got, ok := seen[key]; !ok || got != want {
			t.Errorf("%s = %q (present=%v), want %q", key, got, ok, want)
		}
	}
	// The child must not inherit the caller's environment: only the scrubbed keys are present.
	if _, leaked := seen["GITHUB_TOKEN"]; leaked {
		t.Error("the scrubbed environment must not carry ambient credentials")
	}
}

// The PATH given to the child must contain exactly the resolved binary's directory. A fixed
// /usr/bin:/bin was minimal but wrong; the caller's whole PATH would be right but not minimal.
func TestGitEnvironmentPathIsTheResolvedDirectoryAlone(t *testing.T) {
	gitPath := filepath.Join(string(filepath.Separator), "opt", "homebrew", "bin", "git")
	for _, entry := range gitEnvironment(gitPath) {
		key, value, _ := strings.Cut(entry, "=")
		if key != "PATH" {
			continue
		}
		if value != filepath.Dir(gitPath) {
			t.Errorf("PATH = %q, want exactly the resolved binary's directory %q",
				value, filepath.Dir(gitPath))
		}
		if strings.Contains(value, string(os.PathListSeparator)) {
			t.Errorf("PATH must name one directory, got %q", value)
		}
		return
	}
	t.Error("the scrubbed environment must set PATH")
}

// os.DevNull is "/dev/null" on this host, so comparing the built environment cannot tell a
// literal from the constant -- the same trap as filepath.ToSlash, which is the identity off
// Windows. The property is therefore checked where it is observable: the source must not name a
// POSIX-only device path when building the environment.
func TestGitEnvironmentDoesNotHardcodeAPosixDevicePath(t *testing.T) {
	data, err := os.ReadFile("workspace_command.go")
	if err != nil {
		t.Fatal(err)
	}
	for number, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, literal := range []string{`"/dev/null"`, `"/nonexistent"`, `=/dev/null"`, `=/nonexistent"`} {
			if strings.Contains(line, literal) {
				t.Errorf("workspace_command.go:%d hardcodes %s; use os.DevNull or a host-built path, "+
					"because a POSIX-only literal is not a path on Windows:\n  %s",
					number+1, literal, trimmed)
			}
		}
	}
}

// Boundary: the null device and the unreadable home must exist in a form the host understands.
// /dev/null is not a path on Windows, so hardcoding it turned config suppression into a broken
// filename.
func TestGitEnvironmentUsesPlatformNullAndHome(t *testing.T) {
	env := gitEnvironment(filepath.Join("/usr", "bin", "git"))
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_CONFIG_GLOBAL="+os.DevNull) {
		t.Errorf("the global config must be suppressed with this platform's null device (%s)", os.DevNull)
	}
	if strings.Contains(joined, "HOME=/nonexistent") {
		t.Error("HOME must not be a POSIX-only literal")
	}
	if _, err := os.Stat(nonexistentHome); err == nil {
		t.Errorf("HOME %q must not exist, or git can read a user configuration", nonexistentHome)
	}
}
