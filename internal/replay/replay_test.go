// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/replay"
	"github.com/cordanaLLM/praetor/internal/router"
)

// TestReviewDraftCopiesIsolateEveryRun preserves draft bytes and basenames across arms and orders.
func TestReviewDraftCopiesIsolateEveryRun(t *testing.T) {
	for variant := 0; variant < 2; variant++ {
		t.Run(fmt.Sprint(variant), func(t *testing.T) { checkReviewDraftCopies(t, variant) })
	}
}

// checkReviewDraftCopies exercises mutating arms with distinct or identical draft basenames.
func checkReviewDraftCopies(t *testing.T, variant int) {
	t.Helper()
	_, opts := reviewFixture(t, "stable-reject")
	drafts := make([]string, 2)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("draft-%d.json", i)
		if variant == 1 {
			name = "draft.json"
		}
		drafts[i] = filepath.Join(t.TempDir(), name)
		if err := contextopt.WriteSnapshot(t.Context(), drafts[i], []byte(fmt.Sprintf("%s:%d", name, i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arm := replay.Arm{Name: "mutator", Command: []string{exe, "-test.run=^TestHelperReplayDraftMutation$", "--", "{tree}", "{drafts}"}}
	opts.Arms = []replay.Arm{arm, arm}
	opts.Samples[0].Drafts = drafts
	rep, err := replay.Replay(t.Context(), opts)
	if err != nil {
		t.Fatalf("draft isolation: %v", err)
	}
	if rep.Arms[0].OrderSensitive || !rep.Arms[1].VerdictMatch {
		t.Fatalf("mutated inputs changed scores: %+v", rep.Arms)
	}
	for i := 0; i < 2; i++ {
		raw, err := contextopt.ReadSnapshot(t.Context(), drafts[i])
		if err != nil || string(raw) != fmt.Sprintf("%s:%d", filepath.Base(drafts[i]), i) {
			t.Fatalf("original draft changed: %s %v", raw, err)
		}
	}
}

// TestHelperReplayDraftMutation changes every private draft after verifying its original bytes.
func TestHelperReplayDraftMutation(t *testing.T) {
	sep := slices.Index(os.Args[:min(len(os.Args), 256)], "--")
	if sep < 0 {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	drafts := os.Args[sep+2:]
	seen := map[string]bool{}
	for i := 0; i < len(drafts) && i < replay.MaxDrafts; i++ {
		raw, err := contextopt.ReadSnapshot(ctx, drafts[i])
		name, id, ok := strings.Cut(string(raw), ":")
		if err != nil || !ok || name != filepath.Base(drafts[i]) || seen[id] {
			os.Exit(20)
		}
		seen[id] = true
		if err := contextopt.WriteSnapshot(ctx, drafts[i], []byte("mutated"), 0o600); err != nil {
			os.Exit(21)
		}
	}
	if len(seen) != 2 {
		os.Exit(22)
	}
	writeHelperVerdict("stable-reject", replay.VerdictChangesRequested, nil)
	os.Exit(0)
}

// TestReviewSuppliedTreeMatchesCanonicalCutoff refuses older and divergent pre-cutoff trees.
func TestReviewSuppliedTreeMatchesCanonicalCutoff(t *testing.T) {
	for variant := 0; variant < 3; variant++ {
		t.Run(fmt.Sprint(variant), func(t *testing.T) {
			repo, opts := reviewFixture(t, "stable-reject")
			sample := &opts.Samples[0]
			older := sample.PR.Merge
			sample.Cutoff = sample.Until.Add(time.Hour)
			canonical := repo.commitFiles(sample.Cutoff, "canonical cutoff", map[string]string{"f.txt": "padding\nbase\nfixed\n"})
			sample.Until = sample.Cutoff.Add(time.Hour)
			repo.commitFiles(sample.Until, "Fixes #3", map[string]string{"f.txt": "padding\nbase\nrepaired\n"})
			selected := older
			switch variant {
			case 1:
				repo.git(time.Time{}, "checkout", "--quiet", "-b", "divergent", older)
				selected = repo.commitFiles(sample.Cutoff.Add(-time.Minute), "side tree", map[string]string{"f.txt": "side\nbase\nbug\n"})
			case 2:
				selected = canonical
			}
			repo.git(time.Time{}, "checkout", "--quiet", "--detach", selected)
			sample.TreeDir = repo.dir
			_, err := replay.Replay(t.Context(), opts)
			if variant == 2 && err != nil {
				t.Fatalf("canonical tree refused: %v", err)
			}
			if variant < 2 && !errors.Is(err, replay.ErrRefused) {
				t.Fatalf("noncanonical tree accepted: %v", err)
			}
		})
	}
}

// TestReviewRejectsUnmappableGroundTruth refuses repairs confined to post-cutoff files.
func TestReviewRejectsUnmappableGroundTruth(t *testing.T) {
	for variant := 0; variant < 2; variant++ {
		t.Run(fmt.Sprint(variant), func(t *testing.T) {
			repo := newFixtureRepo(t)
			start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
			repo.commitFiles(start, "seed", map[string]string{"f.txt": "base\n"})
			cutoff := start.Add(time.Hour)
			merge := repo.commitFiles(cutoff, "PR #3", map[string]string{"f.txt": "base\nbug\n"})
			repo.commitFiles(cutoff.Add(time.Hour), "new file", map[string]string{"later.txt": "new bug\n"})
			until := cutoff.Add(2 * time.Hour)
			files := map[string]string{"later.txt": "new fix\n"}
			if variant == 1 {
				files["f.txt"] = "base\nfixed\n"
			}
			repo.commitFiles(until, "Fixes #3", files)
			arm, err := replay.ParseArm("stable=" + armCommand(t, "stable-reject", "f.txt", 2))
			if err != nil {
				t.Fatal(err)
			}
			_, err = replay.Replay(t.Context(), replay.Options{RepoDir: repo.dir, Base: "main", OutcomeLog: filepath.Join(t.TempDir(), "rows.jsonl"),
				Samples: []replay.Sample{{PR: replay.PRSpec{Number: 3, Merge: merge}, Cutoff: cutoff, Until: until}}, Arms: []replay.Arm{arm}})
			if variant == 0 && !errors.Is(err, replay.ErrNoGroundTruth) {
				t.Fatalf("unscorable repair accepted: %v", err)
			}
			if variant == 1 && err != nil {
				t.Fatalf("mapped repair refused: %v", err)
			}
		})
	}
}

// TestReviewPartialFabricationFailsOutcome preserves confirmed failures with incomplete annotations.
func TestReviewPartialFabricationFailsOutcome(t *testing.T) {
	for variant := 0; variant < 4; variant++ {
		t.Run(fmt.Sprint(variant), func(t *testing.T) {
			_, opts := reviewFixture(t, "stable-reject")
			opts.Samples = append(opts.Samples, opts.Samples[0])
			if variant < 2 {
				opts.Samples[variant].Fabrications = []replay.Finding{{File: "f.txt", Line: 2}}
			} else if variant == 2 {
				opts.Samples[0].Fabrications = []replay.Finding{}
			}
			rep, err := replay.Replay(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			want := router.OutcomeOK
			if variant < 2 {
				want = router.OutcomeFail
			}
			if rep.Outcomes[0].Result != want || rep.Arms[0].Fabrication != nil {
				t.Fatalf("partial measurements: result=%s want=%s aggregate=%v", rep.Outcomes[0].Result, want, rep.Arms[0].Fabrication)
			}
		})
	}
}

// TestCutoffSelectsLatestAtOrBeforeCutoffAndRefusesMissing pins Cutoff positive and negative cases.
func TestCutoffSelectsLatestAtOrBeforeCutoffAndRefusesMissing(t *testing.T) {
	repo := newFixtureRepo(t)
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)
	sha0 := repo.commitFiles(t0, "initial", map[string]string{"a.go": "package p\n"})
	sha1 := repo.commitFiles(t1, "second", map[string]string{"a.go": "package p\nvar x = 1\n"})
	repo.commitFiles(t2, "third", map[string]string{"a.go": "package p\nvar x = 2\n"})

	gotExact, err := replay.Cutoff(t.Context(), repo.dir, "main", t1)
	if err != nil || gotExact != sha1 {
		t.Fatalf("Cutoff at t1 = %q, %v; want %s", gotExact, err, sha1)
	}
	gotBetween, err := replay.Cutoff(t.Context(), repo.dir, "main", t0.Add(30*time.Minute))
	if err != nil || gotBetween != sha0 {
		t.Fatalf("Cutoff between t0 and t1 = %q, %v; want %s", gotBetween, err, sha0)
	}
	if _, err := replay.Cutoff(t.Context(), repo.dir, "main", t0.Add(-time.Minute)); !errors.Is(err, replay.ErrRefused) {
		t.Fatalf("Cutoff before first commit must refuse with ErrRefused, got %v", err)
	}
	if _, err := replay.Cutoff(t.Context(), repo.dir, "no-such-branch", t1); !errors.Is(err, replay.ErrRefused) {
		t.Fatalf("Cutoff on missing branch must refuse, got %v", err)
	}
}

// TestReplayRefusesCutoffLeakNamingCommit pins refusing trees holding post-cutoff commits.
func TestReplayRefusesCutoffLeakNamingCommit(t *testing.T) {
	repo := newFixtureRepo(t)
	t0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	cutoff := t0.Add(time.Hour)
	leakedTime := cutoff.Add(2 * time.Hour)
	until := cutoff.Add(4 * time.Hour)

	repo.commitFiles(t0, "base", map[string]string{"svc.go": "package svc\nfunc Run() int { return 0 }\n"})
	repo.git(t0, "checkout", "--quiet", "-b", "pr-branch")
	leakSHA := repo.commitFiles(leakedTime, "side commit with post-cutoff committer date", map[string]string{
		"svc.go": "package svc\nfunc Run() int { return 1 }\n",
	})
	repo.git(cutoff, "checkout", "--quiet", "main")
	repo.git(cutoff, "merge", "--quiet", "--no-ff", "-m", "Merge PR #11", "pr-branch")
	mergeSHA := repo.git(cutoff, "rev-parse", "HEAD")
	repo.commitFiles(until, "Fixes #11", map[string]string{"svc.go": "package svc\nfunc Run() int { return 2 }\n"})

	arm, err := replay.ParseArm("stable=" + armCommand(t, "stable-reject", "svc.go", 2))
	if err != nil {
		t.Fatalf("ParseArm: %v", err)
	}
	_, err = replay.Replay(t.Context(), replay.Options{
		RepoDir:    repo.dir,
		Base:       "main",
		OutcomeLog: filepath.Join(t.TempDir(), "outcomes.jsonl"),
		Samples:    []replay.Sample{{PR: replay.PRSpec{Number: 11, Merge: mergeSHA}, Cutoff: cutoff, Until: until}},
		Arms:       []replay.Arm{arm},
	})
	if !errors.Is(err, replay.ErrRefused) || !errors.Is(err, replay.ErrLeak) || !strings.Contains(err.Error(), leakSHA) {
		t.Fatalf("expected ErrLeak naming %s, got %v", leakSHA, err)
	}
	if verifyErr := replay.VerifyTree(t.Context(), repo.dir, cutoff); !errors.Is(verifyErr, replay.ErrLeak) {
		t.Fatalf("VerifyTree on post-cutoff HEAD must fail with ErrLeak, got %v", verifyErr)
	}
}

// TestGroundTruthMatchesBlameAndPRNumberExcludesUnrelatedAndReland pins all GroundTruth matching rules.
func TestGroundTruthMatchesBlameAndPRNumberExcludesUnrelatedAndReland(t *testing.T) {
	repo, prSHA, blameFixSHA, msgFixSHA, unrelatedSHA, relandSHA, cutoff, until := buildGroundTruthFixture(t)
	gt, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 42, Merge: prSHA, Base: "main"}, cutoff, until)
	if err != nil {
		t.Fatalf("GroundTruth: %v", err)
	}
	matched := make(map[string]bool, len(gt.Fixes))
	for _, fix := range gt.Fixes {
		matched[fix.Commit] = true
	}
	if !matched[blameFixSHA] {
		t.Errorf("blame-attributed fix %s was not matched: %+v", blameFixSHA, gt.Fixes)
	}
	if !matched[msgFixSHA] {
		t.Errorf("PR-number fix %s was not matched: %+v", msgFixSHA, gt.Fixes)
	}
	if matched[unrelatedSHA] {
		t.Errorf("unrelated commit %s was falsely matched: %+v", unrelatedSHA, gt.Fixes)
	}
	if matched[relandSHA] {
		t.Errorf("patch-identical re-land commit %s was falsely matched as a fix: %+v", relandSHA, gt.Fixes)
	}
}

// TestGroundTruthMatchesSubjectWithoutBlame pins subject-only repairs and patch-id exclusion.
func TestGroundTruthMatchesSubjectWithoutBlame(t *testing.T) {
	repo := newFixtureRepo(t)
	start := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	repo.commitFiles(start, "seed", map[string]string{"feature.txt": "seed\n", "notes.txt": "seed\n"})
	cutoff := start.Add(time.Hour)
	pr := repo.commitFiles(cutoff, "feat(replay): derive replay ground truth (#1932)", map[string]string{"feature.txt": "seed\nfeature\n"})
	repair := repo.commitFiles(cutoff.Add(time.Hour), "fix(replay): handle empty bundles after derive replay ground truth", map[string]string{"notes.txt": "repair\n"})
	repo.commitFiles(cutoff.Add(2*time.Hour), "temporary reset", map[string]string{"feature.txt": "seed\n"})
	until := cutoff.Add(3 * time.Hour)
	reland := repo.commitFiles(until, "re-land derive replay ground truth", map[string]string{"feature.txt": "seed\nfeature\n"})
	gt, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 1932, Merge: pr, Base: "main"}, cutoff, until)
	if err != nil {
		t.Fatalf("GroundTruth: %v", err)
	}
	found := false
	for i := 0; i < len(gt.Fixes) && i < replay.MaxCommitScan; i++ {
		fix := gt.Fixes[i]
		if fix.Commit == repair {
			found = true
			if fix.Reason != "subject" || len(fix.Hunks) != 1 || fix.Hunks[0].File != "notes.txt" {
				t.Errorf("subject-only repair: %+v", fix)
			}
		}
		if fix.Commit == reland {
			t.Errorf("patch-identical subject-referenced re-land matched: %+v", fix)
		}
	}
	if !found {
		t.Fatalf("subject-only repair %s absent: %+v", repair, gt.Fixes)
	}
}

// buildGroundTruthFixture builds a history with a PR commit, an unrelated commit, a blame fix,
// a revert + patch-identical re-land, and a message-referenced fix.
func buildGroundTruthFixture(t *testing.T) (*fixtureRepo, string, string, string, string, string, time.Time, time.Time) {
	t.Helper()
	repo := newFixtureRepo(t)
	t0 := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	repo.commitFiles(t0, "seed", map[string]string{
		"calc.go":  "package p\nfunc Base() int { return 1 }\n",
		"other.go": "package p\nvar Other = 1\n",
	})
	tPR := t0.Add(time.Hour)
	prSHA := repo.commitFiles(tPR, "Add Divide (#42)", map[string]string{
		"calc.go": "package p\nfunc Base() int { return 1 }\nfunc Divide(a, b int) int { return a / b }\n",
	})
	unrelatedSHA := repo.commitFiles(tPR.Add(time.Hour), "Bump Other for #420", map[string]string{
		"other.go": "package p\nvar Other = 2\n",
	})
	blameFixSHA := repo.commitFiles(tPR.Add(2*time.Hour), "Guard zero divisor", map[string]string{
		"calc.go": "package p\nfunc Base() int { return 1 }\nfunc Divide(a, b int) int {\n\tif b == 0 { return 0 }\n\treturn a / b\n}\n",
	})
	// Revert back to seed calc.go then re-land the exact PR patch with patch-id parity.
	repo.commitFiles(tPR.Add(3*time.Hour), "Temporary reset", map[string]string{
		"calc.go": "package p\nfunc Base() int { return 1 }\n",
	})
	relandSHA := repo.commitFiles(tPR.Add(4*time.Hour), "Re-land PR #42", map[string]string{
		"calc.go": "package p\nfunc Base() int { return 1 }\nfunc Divide(a, b int) int { return a / b }\n",
	})
	msgFixSHA := repo.commitFiles(tPR.Add(5*time.Hour), "Follow-up check for PR #42", map[string]string{
		"notes.txt": "documented zero check\n",
	})
	return repo, prSHA, blameFixSHA, msgFixSHA, unrelatedSHA, relandSHA, tPR, tPR.Add(6 * time.Hour)
}

// TestReplayArmsOrderSensitivityAndOutcomeRow pins swapped-order detection and outcome log rows.
func TestReplayArmsOrderSensitivityAndOutcomeRow(t *testing.T) {
	repo := newFixtureRepo(t)
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	repo.commitFiles(t0, "seed", map[string]string{"handler.go": "package h\nfunc Serve() {}\n"})
	cutoff := t0.Add(time.Hour)
	prSHA := repo.commitFiles(cutoff, "Add auth (#77)", map[string]string{
		"handler.go": "package h\nfunc Serve() {}\nfunc Auth(tok string) bool { return len(tok) >= 0 }\n",
	})
	until := cutoff.Add(2 * time.Hour)
	repo.commitFiles(until, "Require nonempty token", map[string]string{
		"handler.go": "package h\nfunc Serve() {}\nfunc Auth(tok string) bool { return len(tok) > 0 }\n",
	})
	draftA, draftB := writeDraftPair(t)
	stableArm, err := replay.ParseArm("stable-arm=" + armCommand(t, "stable-reject", "handler.go", 3))
	if err != nil {
		t.Fatalf("ParseArm stable: %v", err)
	}
	flakyArm, err := replay.ParseArm("flaky-arm=" + armCommand(t, "flip-on-reverse", "handler.go", 3))
	if err != nil {
		t.Fatalf("ParseArm flaky: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "outcomes.jsonl")
	report, err := replay.Replay(t.Context(), replay.Options{
		RepoDir:    repo.dir,
		Base:       "main",
		Suite:      "combiner-eval",
		OutcomeLog: logPath,
		Samples: []replay.Sample{{
			PR:     replay.PRSpec{Number: 77, Merge: prSHA},
			Cutoff: cutoff,
			Until:  until,
			Drafts: []string{draftA, draftB},
		}},
		Arms: []replay.Arm{stableArm, flakyArm},
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	assertArmScoresAndRows(t, report, logPath, cutoff)
}

// writeDraftPair writes deterministic draft inputs for both execution orders.
func writeDraftPair(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	draftA := filepath.Join(dir, "draft-a.json")
	draftB := filepath.Join(dir, "draft-b.json")
	if err := contextopt.WriteSnapshot(t.Context(), draftA, []byte(`{"reviewer":"a","defect":"len(tok)>=0 always true"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := contextopt.WriteSnapshot(t.Context(), draftB, []byte(`{"reviewer":"b","debunk":"len is unsigned"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return draftA, draftB
}

// assertArmScoresAndRows checks score aggregates and their persisted outcome metadata.
func assertArmScoresAndRows(t *testing.T, report *replay.Report, logPath string, cutoff time.Time) {
	t.Helper()
	if len(report.Arms) != 2 {
		t.Fatalf("expected 2 arm scores, got %d", len(report.Arms))
	}
	assertStableAndFlakyScores(t, report.Arms[0], report.Arms[1])
	rows, err := router.ReadOutcomes(t.Context(), logPath)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ReadOutcomes: %+v %v", rows, err)
	}
	for i, wantArm := range []string{"stable-arm", "flaky-arm"} {
		assertLoggedReplayRow(t, rows[i], wantArm, cutoff)
	}
}

// assertStableAndFlakyScores checks stable retention and deliberately order-sensitive verdicts.
func assertStableAndFlakyScores(t *testing.T, stable, flaky replay.ArmScore) {
	t.Helper()
	if stable.OrderSensitive || !stable.VerdictMatch || stable.RetainedDefects != 1 || stable.SampleCount != 1 {
		t.Fatalf("stable arm score wrong: %+v", stable)
	}
	if !flaky.OrderSensitive || flaky.VerdictMatch {
		t.Fatalf("flaky arm must flag OrderSensitive and fail VerdictMatch: %+v", flaky)
	}
}

// assertLoggedReplayRow checks the replay identifiers on one persisted arm outcome.
func assertLoggedReplayRow(t *testing.T, row router.Outcome, wantArm string, cutoff time.Time) {
	t.Helper()
	if row.Arm != wantArm || row.Suite != "combiner-eval" || row.SampleCount != 1 {
		t.Fatalf("outcome row metadata mismatch: %+v", row)
	}
	if row.Cutoff == nil || !row.Cutoff.Equal(cutoff) {
		t.Fatalf("outcome row cutoff mismatch: %+v", row)
	}
}

// reviewFixture provides one repaired PR and a deterministic replay invocation.
func reviewFixture(t *testing.T, modes ...string) (*fixtureRepo, replay.Options) {
	t.Helper()
	repo := newFixtureRepo(t)
	start := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	repo.commitFiles(start, "seed", map[string]string{"f.txt": "base\n"})
	cutoff := start.Add(time.Hour)
	sha := repo.commitFiles(cutoff, "PR #3", map[string]string{"f.txt": "base\nbug\n"})
	until := cutoff.Add(time.Hour)
	repo.commitFiles(until, "Fixes #3", map[string]string{"f.txt": "base\nfixed\n"})
	a, b := writeDraftPair(t)
	opts := replay.Options{RepoDir: repo.dir, Base: "main", Suite: "review-tests", OutcomeLog: filepath.Join(t.TempDir(), "rows.jsonl"),
		Samples: []replay.Sample{{PR: replay.PRSpec{Number: 3, Merge: sha}, Cutoff: cutoff, Until: until, Drafts: []string{a, b}}}}
	for i := 0; i < len(modes) && i < replay.MaxArms; i++ {
		arm, err := replay.ParseArm(fmt.Sprintf("arm%d=", i) + armCommand(t, modes[i], "f.txt", 2))
		if err != nil {
			t.Fatal(err)
		}
		opts.Arms = append(opts.Arms, arm)
	}
	return repo, opts
}

// TestReviewRejectsPrePRCutoff prevents scoring code absent from the historical tree.
func TestReviewRejectsPrePRCutoff(t *testing.T) {
	repo, opts := reviewFixture(t, "stable-reject")
	sample := opts.Samples[0]
	_, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 3, Merge: sample.PR.Merge, Base: "main"}, sample.Cutoff.Add(-time.Minute), sample.Until)
	if !errors.Is(err, replay.ErrRefused) {
		t.Fatalf("pre-PR cutoff accepted: %v", err)
	}
}

// TestReviewRejectsSiblingRange prevents unrelated ancestors entering the PR window.
func TestReviewRejectsSiblingRange(t *testing.T) {
	repo, opts := reviewFixture(t, "stable-reject")
	repo.git(time.Time{}, "checkout", "--quiet", "-b", "sibling", "main~2")
	sibling := repo.commitFiles(opts.Samples[0].Cutoff, "sibling", map[string]string{"other": "sibling\n"})
	_, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Merge: sibling + ".." + opts.Samples[0].PR.Merge, Base: "main"}, opts.Samples[0].Cutoff, opts.Samples[0].Until)
	if !errors.Is(err, replay.ErrRefused) {
		t.Fatalf("sibling range accepted: %v", err)
	}
}

// TestReviewArmIsolation pins fresh tracked and untracked content and absence of future refs.
func TestReviewArmIsolation(t *testing.T) {
	_, opts := reviewFixture(t, "isolation", "isolation")
	if _, err := replay.Replay(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
}

// TestReviewDirtyTreeRefused rejects both tracked edits and untracked input in supplied trees.
func TestReviewDirtyTreeRefused(t *testing.T) {
	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			repo, opts := reviewFixture(t, "stable-reject")
			repo.git(time.Time{}, "checkout", "--quiet", "--detach", opts.Samples[0].PR.Merge)
			file := "f.txt"
			if i == 1 {
				file = "untracked"
			}
			repo.writeFile(file, "dirty\n")
			opts.Samples[0].TreeDir = repo.dir
			if _, err := replay.Replay(t.Context(), opts); !errors.Is(err, replay.ErrRefused) {
				t.Fatalf("dirty tree accepted: %v", err)
			}
		})
	}
}

// TestReviewTrailingOutputRefused rejects a valid verdict followed by another JSON object.
func TestReviewTrailingOutputRefused(t *testing.T) {
	_, opts := reviewFixture(t, "trailing")
	if _, err := replay.Replay(t.Context(), opts); err == nil {
		t.Fatal("trailing verdict accepted")
	}
}

// TestReviewFailedArmLeavesNoRows prevents successful early arms persisting failed runs.
func TestReviewFailedArmLeavesNoRows(t *testing.T) {
	_, opts := reviewFixture(t, "stable-reject", "fail")
	if _, err := replay.Replay(t.Context(), opts); err == nil {
		t.Fatal("failing arm accepted")
	}
	rows, err := router.ReadOutcomes(t.Context(), opts.OutcomeLog)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial rows: %d %v", len(rows), err)
	}
}

// TestReviewReplayLane separates replay evaluations from ordinary default-lane routing runs.
func TestReviewReplayLane(t *testing.T) {
	_, opts := reviewFixture(t, "stable-reject")
	rep, err := replay.Replay(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	lanes := router.ReconcileLanes(rep.Outcomes)
	if len(lanes) != 1 || lanes[0].Lane != "replay" {
		t.Fatalf("routing pollution: %+v", lanes)
	}
}

// TestReviewDigestIncludesInputs changes identity when drafts, suite, or repair window change.
func TestReviewDigestIncludesInputs(t *testing.T) {
	_, opts := reviewFixture(t, "stable-reject")
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		rep, err := replay.Replay(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		digest := rep.Outcomes[0].Identity.ContextDigest
		if seen[digest] {
			t.Errorf("input %d reused digest %s", i, digest)
		}
		seen[digest] = true
		switch i {
		case 0:
			if err := contextopt.WriteSnapshot(t.Context(), opts.Samples[0].Drafts[0], []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		case 1:
			opts.Suite = "other-suite"
		case 2:
			opts.Samples[0].Until = opts.Samples[0].Until.Add(time.Second)
		}
	}
}

// TestReviewMergeDraftIncludesPRChange pins first-parent patches for ordinary merge commits.
func TestReviewMergeDraftIncludesPRChange(t *testing.T) {
	repo, opts := reviewFixture(t, "stable-reject")
	repo.git(time.Time{}, "checkout", "--quiet", "-b", "side", "main~1")
	repo.commitFiles(opts.Samples[0].Cutoff, "side", map[string]string{"feature": "merged feature\n"})
	repo.git(time.Time{}, "checkout", "--quiet", "main")
	repo.git(opts.Samples[0].Cutoff, "merge", "--quiet", "--no-ff", "-m", "Merge PR #4", "side")
	merge := repo.git(time.Time{}, "rev-parse", "HEAD")
	// The merge itself is a cutoff; a later repair supplies ground truth.
	opts.Samples[0].Cutoff = opts.Samples[0].Until
	opts.Samples[0].Until = opts.Samples[0].Until.Add(time.Hour)
	repo.commitFiles(opts.Samples[0].Until, "Fixes #4", map[string]string{"feature": "repaired\n"})
	opts.Samples[0].PR = replay.PRSpec{Number: 4, Merge: merge}
	opts.Samples[0].Drafts = nil
	arm, err := replay.ParseArm("inspect=" + armCommand(t, "require-patch", "feature", 1))
	if err != nil {
		t.Fatal(err)
	}
	opts.Arms = []replay.Arm{arm}
	if _, err := replay.Replay(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
}

// TestReviewMixedSampleMetadata requires per-sample cutoff and verdict metadata.
func TestReviewMixedSampleMetadata(t *testing.T) {
	_, opts := reviewFixture(t, "stable-reject")
	other := opts.Samples[0]
	other.Cutoff = other.Cutoff.Add(time.Second)
	opts.Samples = append(opts.Samples, other)
	rep, err := replay.Replay(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rep.Arms[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["samples"]; !ok {
		t.Fatalf("no per-sample metadata: %s", data)
	}
	assertReviewSampleCoordinates(t, fields["samples"], opts.Samples)
	if _, ok := fields["cutoff"]; ok {
		t.Fatalf("aggregate cutoff must be omitted: %s", data)
	}
	if !rep.Cutoff.IsZero() || rep.Arms[0].ForwardVerdict != "" {
		t.Fatalf("misleading aggregate metadata: %+v", rep)
	}
}

// assertReviewSampleCoordinates checks both sample locations instead of accepting only the last.
func assertReviewSampleCoordinates(t *testing.T, raw json.RawMessage, want []replay.Sample) {
	t.Helper()
	var samples []replay.SampleScore
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || !samples[0].Cutoff.Equal(want[0].Cutoff) || !samples[1].Cutoff.Equal(want[1].Cutoff) {
		t.Fatalf("lost sample coordinates: %+v", samples)
	}
}

// TestReviewCanceledPreparationCleansUp pins cleanup independent of an expired caller.
func TestReviewCanceledPreparationCleansUp(t *testing.T) {
	repo, opts := reviewFixture(t, "wait")
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := replay.Replay(ctx, opts); err == nil {
		t.Fatal("canceled replay accepted")
	}
	listed := repo.git(time.Time{}, "worktree", "list", "--porcelain")
	if strings.Count(listed, "worktree ") != 1 {
		t.Fatalf("leaked worktree: %s", listed)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("leaked snapshots: %+v %v", entries, err)
	}
}

// TestReviewShiftedRepairCoordinates maps both message and blame matches into the cutoff tree.
func TestReviewShiftedRepairCoordinates(t *testing.T) {
	for _, message := range []string{"repair", "Fixes #3"} {
		t.Run(message, func(t *testing.T) {
			repo, opts := reviewFixture(t, "stable-reject")
			repo.git(time.Time{}, "reset", "--hard", opts.Samples[0].PR.Merge)
			shifted := strings.Repeat("padding\n", 100) + "base\nbug\n"
			repo.commitFiles(opts.Samples[0].Cutoff.Add(time.Minute), "shift", map[string]string{"f.txt": shifted})
			fixed := strings.Repeat("padding\n", 100) + "base\nfixed\n"
			repo.commitFiles(opts.Samples[0].Until, message, map[string]string{"f.txt": fixed})
			rep, err := replay.Replay(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			h := rep.GroundTruth[0].Hunks
			if len(h) != 1 || h[0].StartLine != 2 || h[0].EndLine != 2 || rep.Arms[0].RetainedDefects != 1 {
				t.Fatalf("wrong cutoff coordinates: %+v %+v", h, rep.Arms)
			}
		})
	}
}

// TestReviewAnnotatedMetrics distinguishes verified additions, fabrication and unmeasured samples.
func TestReviewAnnotatedMetrics(t *testing.T) {
	_, opts := reviewFixture(t, "stable-reject")
	opts.Samples[0].AddedVerified = []replay.Finding{{File: "f.txt", Line: 2}}
	opts.Samples[0].Fabrications = []replay.Finding{{File: "other", Line: 99}}
	rep, err := replay.Replay(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	score := rep.Arms[0]
	assertReviewMetric(t, score.AddedVerifiedFindings, 1)
	assertReviewMetric(t, score.Fabrication, 0)
	opts.Samples[0].AddedVerified = nil
	opts.Samples[0].Fabrications = []replay.Finding{{File: "f.txt", Line: 2}}
	rep, err = replay.Replay(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Arms[0].AddedVerifiedFindings != nil {
		t.Fatalf("unmeasured annotation reported a score: %+v", rep.Arms[0])
	}
	assertReviewMetric(t, rep.Arms[0].Fabrication, 1)
	if !strings.Contains(rep.Outcomes[0].Note, "fabrication=1") {
		t.Fatalf("outcome metrics absent: %+v", rep.Outcomes)
	}
}

// assertReviewMetric requires measured annotation counts rather than pointer presence alone.
func assertReviewMetric(t *testing.T, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("metric %v, want %d", got, want)
	}
}
