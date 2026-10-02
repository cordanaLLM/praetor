package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// recordingSession is an adoption session that records a baseline for root under identity.
func recordingSession(root string, identity repoIdentity) *adoptSession {
	return &adoptSession{repoPath: root, identity: identity, arch: "framework",
		opts:   AdoptOptions{Path: root, RecordBaseline: true},
		report: &AdoptReport{DebtBreakdown: map[string]int{}}}
}

// rerecordingSession is recordingSession with the explicit re-record of an existing baseline.
func rerecordingSession(root string, identity repoIdentity) *adoptSession {
	s := recordingSession(root, identity)
	s.opts.RerecordBaseline = true
	return s
}

// TestReconcileBaseline_RecordsRepositoryIdentity_3D pins BUG-801 for adoption: the recorded
// baseline names the session's repository and the HEAD commit, and never the unborn marker.
func TestReconcileBaseline_RecordsRepositoryIdentity_3D(t *testing.T) {
	// Positive: a committed repository with a resolved session identity.
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "https://github.com/acme/widgets.git")
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, root, "commit", "--allow-empty", "-q", "-m", "fixture"); err != nil {
		t.Fatalf("commit: %v (%s)", err, out)
	}
	head, err := util.RunGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileBaseline(t.Context(), recordingSession(root, repoIdentity{owner: "acme", name: "widgets"})); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	b, err := baseline.LoadBaseline(filepath.Join(root, baselineFile))
	if err != nil {
		t.Fatal(err)
	}
	if b.Repository != "acme/widgets" || b.CommitSHA != head {
		t.Fatalf("recorded identity = %q @ %q; want acme/widgets @ %q", b.Repository, b.CommitSHA, head)
	}

	// Boundary: an unborn branch and an unresolved identity record neither field.
	unborn := t.TempDir()
	initTestGit(t, unborn)
	if err := reconcileBaseline(t.Context(), recordingSession(unborn, repoIdentity{})); err != nil {
		t.Fatalf("reconcile unborn: %v", err)
	}
	if b, err = baseline.LoadBaseline(filepath.Join(unborn, baselineFile)); err != nil || b.Repository != "" || b.CommitSHA != "" {
		t.Fatalf("unborn identity = %+v, %v; want both empty", b, err)
	}

	// Negative: Git metadata that does not answer fails the step and writes no baseline.
	broken := t.TempDir()
	mustWrite(t, filepath.Join(broken, ".git"), "gitdir: nonexistent\n")
	s := recordingSession(broken, repoIdentity{})
	if err := reconcileBaseline(t.Context(), s); err == nil || s.report.BaselineStatus != "failed" {
		t.Fatalf("broken metadata = %v, status %q; want a failed step", err, s.report.BaselineStatus)
	}
	if _, err := os.Stat(filepath.Join(broken, baselineFile)); !os.IsNotExist(err) {
		t.Fatalf("failed step still wrote a baseline: %v", err)
	}
}

// committedRecordingRepo returns a repository with an origin naming acme/widgets and one
// commit, as a re-adoption finds it.
func committedRecordingRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "https://github.com/acme/widgets.git")
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, root, "commit", "--allow-empty", "-q", "-m", "fixture"); err != nil {
		t.Fatalf("commit: %v (%s)", err, out)
	}
	return root
}

// TestReconcileBaseline_KeepsUnchangedBaseline_3D: a re-record that rescans the same debt
// keeps the recorded baseline byte for byte, where it used to rewrite generated_at on every
// run (positive); a changed repository identity rewrites it on a re-record and is kept by a
// plain re-adoption (negative); an unreadable baseline fails the step instead of being replaced
// by a rescan no earlier count ratchets (boundary).
func TestReconcileBaseline_KeepsUnchangedBaseline_3D(t *testing.T) {
	root := committedRecordingRepo(t)
	widgets := repoIdentity{owner: "acme", name: "widgets"}
	if err := reconcileBaseline(t.Context(), recordingSession(root, widgets)); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	full := filepath.Join(root, baselineFile)
	first, err := baseline.LoadBaseline(full)
	if err != nil {
		t.Fatal(err)
	}
	const old = "2020-01-01T00:00:00Z"
	planted := strings.Replace(mustRead(t, full), `"generated_at": "`+first.GeneratedAt+`"`, `"generated_at": "`+old+`"`, 1)
	if !strings.Contains(planted, old) {
		t.Fatal("fixture did not plant the old timestamp")
	}
	mustWrite(t, full, planted)

	again := rerecordingSession(root, widgets)
	if err := reconcileBaseline(t.Context(), again); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if mustRead(t, full) != planted || !strings.Contains(actionDetail(again.report, baselineFile, actionReconcile), "baseline unchanged") {
		t.Fatalf("unchanged debt rewrote the baseline, or was not reported as unchanged: %+v", again.report.ActionDetails)
	}

	gadgets := repoIdentity{owner: "acme", name: "gadgets"}
	if err := reconcileBaseline(t.Context(), recordingSession(root, gadgets)); err != nil || mustRead(t, full) != planted {
		t.Fatalf("a plain re-adoption under another identity = %v; want the recorded baseline kept byte for byte", err)
	}
	if err := reconcileBaseline(t.Context(), rerecordingSession(root, gadgets)); err != nil {
		t.Fatalf("re-record under another identity: %v", err)
	}
	if b, err := baseline.LoadBaseline(full); err != nil || b.Repository != "acme/gadgets" || b.GeneratedAt == old {
		t.Fatalf("changed identity kept the recorded baseline: %+v, %v", b, err)
	}

	mustWrite(t, full, "{")
	for name, s := range map[string]*adoptSession{"re-adoption": recordingSession(root, widgets), "re-record": rerecordingSession(root, widgets)} {
		if err := reconcileBaseline(t.Context(), s); err == nil || s.report.BaselineStatus != "failed" || mustRead(t, full) != "{" {
			t.Fatalf("%s over an unreadable baseline = %v, status %q; want a failed step and the file untouched",
				name, err, s.report.BaselineStatus)
		}
	}
}

// TestReconcileBaseline_UnresolvedIdentityKeepsRecordedRepository_3D pins #123: a re-record
// without a resolved identity never blanks the repository the baseline records. Positive: the
// same debt keeps the file byte for byte. Boundary: changed debt rewrites the file and carries
// the repository forward, as baseline.Record does. Negative: a resolved identity still replaces
// it (TestReconcileBaseline_KeepsUnchangedBaseline_3D).
func TestReconcileBaseline_UnresolvedIdentityKeepsRecordedRepository_3D(t *testing.T) {
	root := committedRecordingRepo(t)
	if err := reconcileBaseline(t.Context(), recordingSession(root, repoIdentity{owner: "acme", name: "widgets"})); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	full := filepath.Join(root, baselineFile)
	recorded := mustRead(t, full)
	if err := reconcileBaseline(t.Context(), rerecordingSession(root, repoIdentity{})); err != nil {
		t.Fatalf("unresolved reconcile: %v", err)
	}
	if mustRead(t, full) != recorded {
		t.Fatalf("an unresolved rescan of the same debt rewrote the baseline:\n%s", mustRead(t, full))
	}

	planted := strings.Replace(recorded, `"infractions": []`,
		`"infractions": [{"rule_id": "HISS-04", "file_path": "gone.go", "line_number": 1, "fingerprint": "gone.go:1:HISS-04"}]`, 1)
	if planted == recorded {
		t.Fatalf("fixture did not plant the changed debt:\n%s", recorded)
	}
	mustWrite(t, full, planted)
	stale := recordingSession(root, repoIdentity{})
	if err := reconcileBaseline(t.Context(), stale); err != nil || mustRead(t, full) != planted {
		t.Fatalf("a plain re-adoption over a stale entry = %v; want the baseline kept byte for byte", err)
	}
	if verdict := stale.report.BaselineRatchet; verdict == nil || !verdict.Passed || len(stale.report.Warnings) != 1 ||
		!strings.Contains(stale.report.Warnings[0], "baseline entry matches nothing in the tree") {
		t.Fatalf("stale entry: verdict %+v, warnings %v; want a pass with the stale notice", verdict, stale.report.Warnings)
	}
	if err := reconcileBaseline(t.Context(), rerecordingSession(root, repoIdentity{})); err != nil {
		t.Fatalf("unresolved reconcile of changed debt: %v", err)
	}
	if b, err := baseline.LoadBaseline(full); err != nil || b.Repository != "acme/widgets" || len(b.Infractions) != 0 {
		t.Fatalf("changed debt must be rewritten with the recorded repository kept: %+v, %v", b, err)
	}
}
