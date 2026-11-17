// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// Positive and boundary: the exact release-hook arguments stamp and verify a temporary root.
func TestPluginReleaseVersion_PositiveAndBoundary(t *testing.T) {
	if err := runRelease([]string{"plugin-version", "--help"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(compiler.PluginManifestRel))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory needs the owner search bit to hold entries; 0o700 is least privilege
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"name":"praetor","version":"0.1.0-dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRelease([]string{"plugin-version", "--root=" + root, "--version=0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := compiler.VerifyPluginVersion(t.Context(), root, "0.1.0"); err != nil {
		t.Fatal(err)
	}
}

// Negative: absent versions, unknown flags, extra operands and absent manifests fail closed.
func TestPluginReleaseVersion_Negative(t *testing.T) {
	cases := [][]string{
		{}, {"--unknown"}, {"--version=0.1.0", "extra"},
		{"--root=" + t.TempDir(), "--version=0.1.0"},
	}
	for i := 0; i < len(cases) && i < 8; i++ {
		if err := runRelease(append([]string{"plugin-version"}, cases[i]...)); err == nil {
			t.Errorf("accepted %v", cases[i])
		}
	}
}
