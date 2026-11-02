// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const brokenForgeManifest = "version: 1\nexceptions:\n  - rule: HISS-11\n    path: .github/workflows/release.yml\n    reason: builder migration\n    expires: 2099-01-01\n"

func TestForgeManifestDeadlock(t *testing.T) {
	root := repositoryWithManifest(t, brokenForgeManifest)
	cases := []struct{ name, command string }{
		{"comment", `gh pr comment 1 --body "Manifest repair ready"`},
		{"thread reply", `gh api repos/own/project/pulls/1/comments/2/replies -f body="Repair ready"`},
		{"create", `gh pr create --title "Repair manifest" --body "Set category and entered"`},
		{"merge", `gh pr merge 1 --squash`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreToolCommand(t, root, tc.command, nil)
			if resp.ExitCode != 0 || !strings.Contains(string(resp.Stderr), "manifest finding") {
				t.Fatalf("want allow with manifest finding, got %+v", resp)
			}
		})
	}
	assertForgeTextDenied(t, runPreToolCommand(t, root, `gh pr comment 1 --body "outside/project#2"`, nil), "outside/project#2")
}

func TestForgeManifestCommandDirectory(t *testing.T) {
	invalid := repositoryWithManifest(t, brokenForgeManifest)
	valid := forgeManifestWorktree(t, invalid)
	for _, prefix := range []string{"cd %q && ", "env -C %q "} {
		command := fmt.Sprintf(prefix, valid) + `gh pr comment 1 --body "worktree-org/project#2"`
		resp := runPreToolCommand(t, invalid, command, nil)
		if resp.ExitCode != 0 || strings.Contains(string(resp.Stderr), "manifest finding") {
			t.Errorf("valid command directory: %+v", resp)
		}
	}
	assertForgeTextDenied(t, runPreToolCommand(t, valid, `gh pr comment 1 --body "outside/project#2"`, nil), "outside/project#2")
}

func forgeManifestWorktree(t *testing.T, root string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	git := func(args ...string) string {
		t.Helper()
		out, err := util.RunGit(ctx, root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git("add", manifestName)
	tree := git("write-tree")
	commit := git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit-tree", tree, "-m", "fixture")
	git("update-ref", "HEAD", commit)
	worktree := filepath.Join(t.TempDir(), "worktree")
	git("worktree", "add", "--detach", worktree, commit)
	writeTestFixtureFile(t, filepath.Join(worktree, manifestName), "version: 1\nforge:\n  own_organizations: [worktree-org]\n")
	return worktree
}

func TestForgeManifestIndependentSources(t *testing.T) {
	root := repositoryWithManifest(t, brokenForgeManifest+"forge:\n  own_organizations: [untrusted-org]\n")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := util.RunGit(ctx, root, "remote", "add", "origin", "https://github.com/remote-org/project.git"); err != nil {
		t.Fatal(err)
	}
	fleet := filepath.Join(t.TempDir(), "fleet.yaml")
	writeTestFixtureFile(t, fleet, "forge:\n  own_organizations: [operator-org]\n")
	getenv := func(key string) string {
		if key == "PRAETOR_FLEET_CONFIG" {
			return fleet
		}
		return ""
	}
	for _, org := range []string{"remote-org", "operator-org"} {
		resp := runPreToolCommand(t, root, fmt.Sprintf(`gh pr comment 1 --body "%s/project#2"`, org), getenv)
		if resp.ExitCode != 0 || !strings.Contains(string(resp.Stderr), "git remote owner") || !strings.Contains(string(resp.Stderr), "operator layer") {
			t.Errorf("independent source missing: %+v", resp)
		}
	}
	for _, org := range []string{"outside", "untrusted-org"} {
		ref := org + "/project#2"
		assertForgeTextDenied(t, runPreToolCommand(t, root, fmt.Sprintf(`gh pr comment 1 --body "%s"`, ref), getenv), ref)
	}
}

func TestForgeManifestMalformedAndUnreadableText(t *testing.T) {
	for _, manifest := range []string{"version: [", brokenForgeManifest} {
		root := repositoryWithManifest(t, manifest)
		resp := runPreToolCommand(t, root, `gh pr comment 1 --body "Repair ready"`, nil)
		if resp.ExitCode != 0 || !strings.Contains(string(resp.Stderr), "manifest finding") {
			t.Fatalf("plain write refused: %+v", resp)
		}
		assertForgeTextDenied(t, runPreToolCommand(t, root, `gh pr create --body-file missing.md`, nil), "missing.md")
		assertForgeTextDenied(t, runPreToolCommand(t, root, `gh pr comment 1 --body "https://github.com/outside/project/pull/2"`, nil), "https://github.com/outside/project/pull/2")
	}
}
