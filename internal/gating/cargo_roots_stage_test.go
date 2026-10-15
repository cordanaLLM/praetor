// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Positive: a Cargo workspace below the repository root gets every Cargo part of the toolchain
// stages, in its own directory, and the stage reasons name it (#1102). With the root-only check
// the gate ran no cargo command for worker/Cargo.lock and reported the stages not applicable.
func TestCargoStages_Positive_RunEveryTrackedWorkspace(t *testing.T) {
	repo := newHermeticGitRepo(t)
	commitFile(t, repo, "worker/Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\n")
	commitFile(t, repo, "worker/Cargo.lock", "version = 4\n")
	commitFile(t, repo, "worker/crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	commitFile(t, repo, "api/Cargo.toml", "[package]\nname = \"api\"\nversion = \"0.1.0\"\n")
	commitFile(t, repo, "api/Cargo.lock", "version = 4\n")
	writeLockfiles(t, repo)
	recorded := &[]recordedCommand{}
	cfg := newStageConfig(repo, false, &PipelineReport{Stages: make([]StageResult, 0, maxStages)})
	cfg.run = fakeRunner(recorded, "", nil)
	cfg.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	cfg.inspectTree = cleanTree(cfg.rep)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("a stage failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed}, stageSecurity: {Status: StagePassed}, stageTests: {Status: StagePassed},
	})
	for _, root := range []string{"api", "worker"} {
		for _, command := range []string{"fetch --locked", "audit"} {
			if !ranIn(*recorded, "cargo "+command, filepath.Join(repo, root)) {
				t.Errorf("cargo %s did not run in %s: %+v", command, root, *recorded)
			}
		}
		if !ranBelow(*recorded, "cargo test", string(filepath.Separator)+root) {
			t.Errorf("cargo test did not run in a worktree's %s: %+v", root, *recorded)
		}
		for _, result := range got {
			if !strings.Contains(result.Message, root+"/Cargo.lock") {
				t.Errorf("%s reason does not name %s/Cargo.lock: %q", result.Name, root, result.Message)
			}
		}
	}
	if !slices.Equal(cfg.verified, []string{languageCargo}) {
		t.Errorf("verified languages = %q, want [cargo]", cfg.verified)
	}
}

// ranIn reports whether a recorded command line starting with prefix ran in dir.
func ranIn(recorded []recordedCommand, prefix, dir string) bool {
	return slices.ContainsFunc(recorded, func(c recordedCommand) bool {
		return c.dir == dir && strings.HasPrefix(c.name+" "+strings.Join(c.args, " "), prefix)
	})
}

// ranBelow reports whether a recorded command line starting with prefix ran in a directory
// ending in suffix.
func ranBelow(recorded []recordedCommand, prefix, suffix string) bool {
	return slices.ContainsFunc(recorded, func(c recordedCommand) bool {
		return strings.HasSuffix(c.dir, suffix) && strings.HasPrefix(c.name+" "+strings.Join(c.args, " "), prefix)
	})
}
