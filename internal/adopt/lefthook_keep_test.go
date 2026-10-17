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
	writeCheckpointBundle(t, source)
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

// shippedCheckpointLockSource is newAdoptLockSource carrying the checkpoint bundle this
// repository ships, so the texts adoption installs are the ones audit calls current.
func shippedCheckpointLockSource(t *testing.T) string {
	t.Helper()
	source := newAdoptLockSource(t)
	for _, rel := range checkpointBundle {
		mustWrite(t, filepath.Join(source, filepath.FromSlash(rel)), shippedCheckpointText(t, rel))
	}
	return source
}

// Positive (#502, engine-adopt-keep-evaluator-refresh): lefthook.yml is classified before the
// checkpoint bundle is installed. Beside a kept configuration that does not extend the canonical
// policy, unedited earlier Praetor evaluators are refreshed as one unit and the absent launcher
// is installed, with and without --force, so the audit has no earlier evaluator left to report.
func TestAdopt_Positive_KeptLefthookRefreshesEarlierCheckpointBundle(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath := newTestRepo(t, "kept-lefthook-checkpoint")
		mustWrite(t, filepath.Join(repoPath, lefthookFile), preparationLefthook)
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), firstPriorCheckpointText(t, checkpointScript))
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointCommon)), firstPriorCheckpointText(t, checkpointCommon))
		rep := adoptWithSource(t, repoPath, shippedCheckpointLockSource(t), force)
		assertLefthookKept(t, repoPath, rep, preparationLefthook)
		for _, rel := range checkpointBundle {
			if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != shippedCheckpointText(t, rel) {
				t.Fatalf("force=%v: %s is not this release's text beside a kept lefthook.yml", force, rel)
			}
		}
		if lines := auditCheckpointLines(t, repoPath); len(lines) != 0 {
			t.Fatalf("force=%v: audit still reports an earlier evaluator: %q", force, lines)
		}
	}
}

// Negative: when a bundle file beside a kept configuration holds an edited copy, the
// configuration runs scripts of its own: no existing file is refreshed, --force included, only
// the absent launcher is installed, the run reports no issue, and the audit line for the
// earlier checkpoint.py names the edited file instead of a plain adopt run.
func TestAdopt_Negative_KeptLefthookKeepsBundleBesideEditedCopy(t *testing.T) {
	repoPath := newTestRepo(t, "kept-lefthook-edited-checkpoint")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), preparationLefthook)
	prior := firstPriorCheckpointText(t, checkpointScript)
	edited := firstPriorCheckpointText(t, checkpointCommon) + "# local edit\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), prior)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointCommon)), edited)
	rep := adoptWithSource(t, repoPath, shippedCheckpointLockSource(t), true)
	assertLefthookKept(t, repoPath, rep, preparationLefthook)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript))); got != prior {
		t.Fatal("checkpoint.py was refreshed beside an edited common.py")
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointCommon))); got != edited {
		t.Fatal("the edited common.py was replaced")
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointLauncher))); got != shippedCheckpointText(t, checkpointLauncher) {
		t.Fatal("the absent launcher was not installed")
	}
	lines := auditCheckpointLines(t, repoPath)
	if len(lines) != 1 || !strings.Contains(lines[0], checkpointCommon+" holds an edited copy") || strings.Contains(lines[0], "no --force needed") {
		t.Fatalf("want one line naming the edited %s, not a plain adopt run: %q", checkpointCommon, lines)
	}
}

// Boundary: a kept configuration that extends the canonical policy vendors its bundle (BUG-858):
// an unedited earlier checkpoint.py stays as it is, and the audit stays quiet.
func TestAdopt_Boundary_CanonicalLefthookKeepsEarlierCheckpoint(t *testing.T) {
	repoPath := newTestRepo(t, "canonical-lefthook-checkpoint")
	canonical := "extends:\n  - " + canonicalLefthookPolicy + "\n"
	mustWrite(t, filepath.Join(repoPath, lefthookFile), canonical)
	prior := firstPriorCheckpointText(t, checkpointScript)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), prior)
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: shippedCheckpointLockSource(t), Path: repoPath}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript))); got != prior {
		t.Fatal("checkpoint.py was refreshed beside the canonical policy")
	}
	if lines := auditCheckpointLines(t, repoPath); len(lines) != 0 {
		t.Fatalf("a vendored bundle is reported: %q", lines)
	}
}

// Negative: the current rendering without checkpoint jobs is praetor's own text, so once the
// lifecycle is installed a plain run adds the jobs, a reconcile with no backup, and activates
// the result; before, the run kept it as drift that only --force replaced.
func TestAdopt_Negative_CurrentLefthookGainsCheckpointJobsWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "lefthook-gains-checkpoint")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), buildLefthookYAML())
	rep := adoptWithSource(t, repoPath, checkpointLockSource(t), false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, true) {
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
	withCheckpoint := buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, true)
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
