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
)

const adoptSuccessLine = "Repository successfully adopted into cordanaLLM/praetor governance!"

// adoptSuccessLineFixture is a git repository with an origin remote holding files, adopted with
// lefthook stubbed out so adoption installs no hooks, and the source checkout it pins its lock
// against.
func adoptSuccessLineFixture(t *testing.T, files map[string]string) (repo, source string) {
	t.Helper()
	repo = t.TempDir()
	return repo, adoptFixtureAt(t, repo, files)
}

// adoptFixtureAt builds the adoptSuccessLineFixture repository at repo, a directory the caller
// chose (one below a dev root for --all-missing), and returns the source checkout.
func adoptFixtureAt(t *testing.T, repo string, files map[string]string) (source string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	stubDir := t.TempDir()
	if err := os.Chmod(writeFixtureFile(t, stubDir, "lefthook", "#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+filepath.Dir(gitPath))
	for rel, content := range files {
		writeFixtureFile(t, repo, rel, content)
	}
	env := initGitFixture(t, repo)
	if output, err := runFixtureGit(t, repo, env, "remote", "add", "origin", "https://github.com/example/adopted.git"); err != nil {
		t.Fatalf("add origin remote: %v (%s)", err, output)
	}
	source, err = filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// runAdoptOutput runs one plain adoption of repo through runAdopt and returns what it printed.
func runAdoptOutput(t *testing.T, repo, source string) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return runAdopt([]string{"--path", repo, "--profile", "framework", "--lock-source-root", source})
	})
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	return out
}

// #594: only the Verification Gate qualifies the success line. A repository that already has a
// .devcontainer gets the preserved-DevContainer warning on every plain run, the first and each
// re-run, and that informational warning stays on the DevContainer pillar line: both runs still
// end with the success line (positive, and boundary: the re-run). A plan whose verify-all can only
// exit 1 qualifies the line with the Verification Gate alone, never the preserved DevContainer
// beside it (negative).
func TestAdoptExistingDevContainerKeepsTheSuccessLine(t *testing.T) {
	devcontainer := `{"name": "adopted", "image": "mcr.microsoft.com/devcontainers/base:ubuntu"}` + "\n"
	repo, source := adoptSuccessLineFixture(t, map[string]string{
		"go.mod": "module example.invalid/adopted\n\ngo 1.27\n", ".devcontainer/devcontainer.json": devcontainer,
	})
	for run := 1; run <= 2; run++ {
		out := runAdoptOutput(t, repo, source)
		mustContain(t, out, "⚠ DevContainer", "[warned: 1 warning(s)]", adoptSuccessLine)
		if strings.Contains(out, "not ready yet") {
			t.Fatalf("run %d: a preserved DevContainer qualified the success line:\n%s", run, out)
		}
	}
	unavailable, source := adoptSuccessLineFixture(t, map[string]string{
		"pyproject.toml":                  "[project]\nname = \"pytool\"\n\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n",
		".devcontainer/devcontainer.json": devcontainer,
	})
	out := runAdoptOutput(t, unavailable, source)
	mustContain(t, out, "⚠ DevContainer", "⚠ Verification Gate", "; not ready yet: Verification Gate. See the warnings above.")
	if strings.Contains(out, adoptSuccessLine) {
		t.Fatalf("an unavailable plan ended with the success line:\n%s", out)
	}
}
