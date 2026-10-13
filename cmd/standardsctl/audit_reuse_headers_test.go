// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// headerAuditRoot returns a Git work tree holding files.
func headerAuditRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("git", "init", "-q", root)
	command.Env = testsupport.HermeticGitEnv(t)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

// Audit names a repository that labels its files the REUSE way by headers alone, so the REUSE gate
// adoption no longer renders there is not dropped in silence (#1244). Positive: such a repository
// gets the warning with its file count, and the audit still passes. Negative: a repository with
// REUSE.toml, and the measured layout of third-party texts without headers, get no line.
// Boundary: a tree git cannot list prints nothing and passes.
func TestAuditReuseHeadersOnly_3D(t *testing.T) {
	headerOnly := map[string]string{"LICENSES/MIT.txt": "MIT License\n", "main.go": "// SPDX-License-Identifier: MIT\npackage main\n"}
	out, err := captureStdout(t, func() error { return auditReuseHeadersOnly(t.Context(), headerAuditRoot(t, headerOnly)) })
	if err != nil || !strings.Contains(out, "[WARN] REUSE by headers only, no REUSE.toml: the REUSE gate is not rendered; add REUSE.toml to keep it (1 file(s)") {
		t.Errorf("header-only repository: err %v, output %q", err, out)
	}
	for name, files := range map[string]map[string]string{
		"REUSE.toml":          {"REUSE.toml": "version = 1\n", "LICENSES/MIT.txt": "MIT License\n", "main.go": "// SPDX-License-Identifier: MIT\n"},
		"third-party notices": {"LICENSES/goldmark-MIT.txt": "MIT License\n", "LICENSING.md": "# Licensing\n"},
	} {
		out, err := captureStdout(t, func() error { return auditReuseHeadersOnly(t.Context(), headerAuditRoot(t, files)) })
		if err != nil || out != "" {
			t.Errorf("%s: err %v, output %q; want no line", name, err, out)
		}
	}
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "main.go"), []byte("// SPDX-License-Identifier: MIT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := captureStdout(t, func() error { return auditReuseHeadersOnly(t.Context(), plain) }); err != nil || out != "" {
		t.Errorf("no work tree: err %v, output %q; want no line", err, out)
	}
}
