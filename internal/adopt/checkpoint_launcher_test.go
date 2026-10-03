package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated checkpoint jobs start through the launcher (lefthookPythonCommand), so the
// launcher is a file of the checkpoint bundle: installed with the scripts, and required for
// the jobs (#339).

// Positive: a complete source installs the launcher with the source's bytes, the rendering
// that calls it counts as activatable only while it is there, and an unedited CRLF checkout
// of it is the current file, not a drifted one.
func TestCheckpointLauncher_Positive_InstalledWithTheBundle(t *testing.T) {
	source := checkpointSourceFixture(t, true)
	session := checkpointSession(t, source)
	if ready, err := reconcileCheckpointBundle(context.Background(), session, false); err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	installed := filepath.Join(session.repoPath, filepath.FromSlash(checkpointLauncher))
	if got := mustRead(t, installed); got != checkpointFixtureTexts[checkpointLauncher] {
		t.Fatalf("installed launcher = %q, want the source's", got)
	}
	if !checkpointFilesPresent(session.repoPath) {
		t.Fatal("a complete bundle is not reported present")
	}
	mustWrite(t, installed, crlfText(checkpointFixtureTexts[checkpointLauncher]))
	if ready, err := reconcileCheckpointBundle(context.Background(), session, false); err != nil || !ready {
		t.Fatalf("a CRLF checkout of the launcher: ready=%v err=%v", ready, err)
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if checkpointFilesPresent(session.repoPath) {
		t.Fatal("a bundle without its launcher is reported present, so jobs that call it would be activated")
	}
}

// Negative: a source root without the launcher installs no part of the bundle and enables no
// checkpoint job, since every job would call a file that is not there.
func TestCheckpointLauncher_Negative_SourceWithoutItInstallsNothing(t *testing.T) {
	source := checkpointSourceFixture(t, true)
	if err := os.Remove(filepath.Join(source, filepath.FromSlash(checkpointLauncher))); err != nil {
		t.Fatal(err)
	}
	session := checkpointSession(t, source)
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err == nil || ready || !strings.Contains(err.Error(), "checkpoint source "+checkpointLauncher) {
		t.Fatalf("ready=%v err=%v, want the missing launcher named", ready, err)
	}
	for _, name := range checkpointBundle {
		if fileExists(filepath.Join(session.repoPath, filepath.FromSlash(name))) {
			t.Errorf("%s was installed from a bundle without its launcher", name)
		}
	}
	if strings.Contains(buildLefthookYAMLFor(lefthookJobLanguages, ready), checkpointLauncher) {
		t.Fatal("the rendering calls a launcher that was not installed")
	}
}

// Boundary: an edited launcher is the repository's and is kept, --force included; the
// lifecycle is then unavailable and no other bundle file is written beside it. Beside the
// canonical hook policy the launcher belongs to the vendored bundle: an existing one stays as
// it is and a missing one is installed.
func TestCheckpointLauncher_Boundary_EditedOneIsKept(t *testing.T) {
	edited := checkpointFixtureTexts[checkpointLauncher] + "# repository note\n"
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	session.opts.Force = true
	installed := filepath.Join(session.repoPath, filepath.FromSlash(checkpointLauncher))
	mustWrite(t, installed, edited)
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err == nil || ready || !strings.Contains(err.Error(), checkpointLauncher+" differs") {
		t.Fatalf("ready=%v err=%v, want the kept launcher named", ready, err)
	}
	if got := mustRead(t, installed); got != edited {
		t.Error("--force replaced an edited launcher")
	}
	if fileExists(filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))) {
		t.Error("checkpoint.py was installed beside a kept launcher")
	}
	vendored := checkpointSession(t, checkpointSourceFixture(t, true))
	kept := filepath.Join(vendored.repoPath, filepath.FromSlash(checkpointLauncher))
	mustWrite(t, kept, edited)
	if ready, err := reconcileCheckpointBundle(context.Background(), vendored, true); err != nil || !ready {
		t.Fatalf("vendored bundle: ready=%v err=%v", ready, err)
	}
	if got := mustRead(t, kept); got != edited {
		t.Error("a vendored launcher was replaced")
	}
	missing := checkpointSession(t, checkpointSourceFixture(t, true))
	if ready, err := reconcileCheckpointBundle(context.Background(), missing, true); err != nil || !ready {
		t.Fatalf("vendored bundle without a launcher: ready=%v err=%v", ready, err)
	}
	if !fileExists(filepath.Join(missing.repoPath, filepath.FromSlash(checkpointLauncher))) {
		t.Error("the launcher a vendored bundle lacks was not installed")
	}
}

// Boundary: an earlier rendering whose checkpoint jobs named python3 migrates on a plain run
// from a source bundle, a reconcile with no replace: the jobs then call the launcher, and the
// launcher is installed with them, so no job calls a file that is absent.
func TestAdopt_Boundary_Python3ByNameRenderingMigratesToTheLauncher(t *testing.T) {
	prior := string(readPriorLefthookFixtures(t)["python3-name-go-rust-checkpoint.lefthook.yml"])
	if !strings.Contains(prior, "python3 -B "+checkpointScript) {
		t.Fatal("the fixture does not name python3 in its checkpoint jobs")
	}
	repoPath := newTestRepo(t, "python3-by-name")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), prior)
	rep := adoptWithSource(t, repoPath, checkpointLockSource(t), false)
	got := mustRead(t, filepath.Join(repoPath, lefthookFile))
	if got != buildLefthookYAMLFor(lefthookJobLanguages, true) {
		t.Fatalf("not migrated to the current rendering with checkpoint jobs:\n%s", got)
	}
	if strings.Contains(got, "python3") || strings.Count(got, "sh "+checkpointLauncher) != len(checkpointEvents(true)) {
		t.Errorf("the migrated checkpoint jobs do not start through the launcher:\n%s", got)
	}
	if !fileExists(filepath.Join(repoPath, filepath.FromSlash(checkpointLauncher))) {
		t.Error("the launcher the migrated jobs call was not installed")
	}
	if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("want a reconcile and no replace: %+v", rep.ActionDetails)
	}
}
