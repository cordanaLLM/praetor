// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// Boundary: go vet skips a tracked module with no packages, but a tracked module with a vet
// failure still makes the command fail and names that module.
func TestGoVet_Boundary_SkipsEmptyModuleAndFailsBrokenModule(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "go.mod", "module example.test/root\n\ngo 1.27\n")
	writeFixtureFile(t, repo, "broken/go.mod", "module example.test/broken\n\ngo 1.27\n")
	writeFixtureFile(t, repo, "broken/broken.go", "package broken\n\nvar _ int = \"not an int\"\n")
	initGitFixture(t, repo)
	t.Chdir(repo)

	stdout, err := captureStdout(t, runGoVet)
	if err == nil || !strings.Contains(err.Error(), filepath.ToSlash("broken/go.mod")) {
		t.Fatalf("go vet error = %v, want the broken module", err)
	}
	if !strings.Contains(stdout, "go vet go.mod: "+gating.NoPackagesNote+", skipped") {
		t.Fatalf("go vet output = %q, want the empty-module skip", stdout)
	}
}
