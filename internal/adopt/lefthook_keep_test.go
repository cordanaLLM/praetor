package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// checkpointLockSource is newAdoptLockSource carrying a complete checkpoint bundle, so the
// lifecycle is ready and the rendering carries the checkpoint jobs.
func checkpointLockSource(t *testing.T) string {
	t.Helper()
	source := newAdoptLockSource(t)
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointScript)), "#!/usr/bin/env python3\nprint('shared')\n")
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointCommon)), "class HookError(Exception):\n    pass\n")
	return source
}

// adoptWithSource adopts repoPath from source and fails on any error.
func adoptWithSource(t *testing.T, repoPath, source string, force bool) *AdoptReport {
	t.Helper()
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return rep
}

// Positive (#502): lefthook.yml is classified before the checkpoint bundle is installed. Beside
// a kept configuration only absent checkpoint files are written, --force included: an unedited
// earlier checkpoint.py is not refreshed, since the kept configuration may run it, and the
// missing common.py is installed.
func TestAdopt_Positive_KeptLefthookGetsOnlyAbsentCheckpointFiles(t *testing.T) {
	repoPath := newTestRepo(t, "kept-lefthook-checkpoint")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), preparationLefthook)
	prior := firstPriorCheckpointText(t, checkpointScript)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), prior)
	rep := adoptWithSource(t, repoPath, checkpointLockSource(t), true)
	assertLefthookKept(t, repoPath, rep, preparationLefthook)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript))); got != prior {
		t.Fatal("checkpoint.py was refreshed beside a kept lefthook.yml")
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointCommon))); !strings.Contains(got, "HookError") {
		t.Fatalf("the absent common.py was not installed: %q", got)
	}
}

// Negative: the current rendering without checkpoint jobs is praetor's own text, so once the
// lifecycle is installed a plain run adds the jobs, a reconcile with no backup, and activates
// the result; before, the run kept it as drift that only --force replaced.
func TestAdopt_Negative_CurrentLefthookGainsCheckpointJobsWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "lefthook-gains-checkpoint")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), buildLefthookYAML())
	rep := adoptWithSource(t, repoPath, checkpointLockSource(t), false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookJobLanguages, true) {
		t.Fatalf("the checkpoint jobs were not added:\n%s", got)
	}
	if !strings.Contains(lefthookDetails(rep), "Added the checkpoint lifecycle jobs") || hasAction(rep, lefthookFile, actionReplace) {
		t.Fatalf("want a reconcile adding the checkpoint jobs, no replace: %+v", rep.ActionDetails)
	}
	if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("the migrated configuration was not activated")
	}
}

// Boundary: the current rendering with checkpoint jobs is kept, not stripped, by a run that did
// not install the lifecycle, --force included. Its checkpoint scripts are missing, so it is not
// activated, and the skip names the regeneration path, never --force.
func TestAdopt_Boundary_CheckpointLefthookKeptWhenLifecycleUnavailable(t *testing.T) {
	withCheckpoint := buildLefthookYAMLFor(lefthookJobLanguages, true)
	repoPath, rep := adoptLefthookFixture(t, "lefthook-keeps-checkpoint", withCheckpoint, true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != withCheckpoint {
		t.Fatalf("the checkpoint jobs were stripped:\n%s", got)
	}
	note := lefthookDetails(rep)
	if !strings.Contains(note, "kept with its checkpoint lifecycle jobs") || hasAction(rep, lefthookFile, actionReplace) {
		t.Fatalf("want the rendering kept and reconciled: %+v", rep.ActionDetails)
	}
	if !strings.Contains(note, "not byte for byte a Praetor rendering adoption can activate") ||
		!strings.Contains(note, "remove lefthook.yml and re-run adopt") || strings.Contains(note, "--force") {
		t.Fatalf("the activation skip does not name the regeneration path: %q", note)
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("a rendering whose checkpoint scripts are missing was activated")
	}
}
