// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The gate runs from git hooks, where git exports GIT_DIR and GIT_INDEX_FILE for the hook's
// repository. The production runner behind the go-test stage must resolve the repository its
// working directory names, or the suite's git fixtures write into the hook's (BUG-886).
func TestStageRunnerIgnoresAmbientGitRepository(t *testing.T) {
	hookRepo := newHermeticGitRepo(t)
	target := newHermeticGitRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(hookRepo, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(hookRepo, ".git", "index"))

	cfg := newStageConfig(target, false, &PipelineReport{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	gitDir, err := cfg.run(ctx, target, "git", "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if !util.SameDirectory(gitDir, filepath.Join(target, ".git")) {
		t.Fatalf("stage runner resolved %q, want the target's .git, not the hook's %q",
			gitDir, filepath.Join(hookRepo, ".git"))
	}

	// Negative: a failing stage command still reports why, from standard error.
	_, err = cfg.run(ctx, target, "git", "rev-parse", "--verify", "refs/heads/no-such-branch")
	if err == nil || err.Error() == "exit status 128" {
		t.Fatalf("stage runner dropped the failure's standard error: %v", err)
	}
}
