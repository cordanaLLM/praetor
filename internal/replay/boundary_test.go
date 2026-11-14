// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/replay"
)

// TestReplayZeroLaterCommitsRefused pins refusing a sample with zero post-merge commits.
func TestReplayZeroLaterCommitsRefused(t *testing.T) {
	repo := newFixtureRepo(t)
	t0 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	repo.commitFiles(t0, "seed", map[string]string{"main.go": "package main\n"})
	cutoff := t0.Add(time.Hour)
	prSHA := repo.commitFiles(cutoff, "Add feature (#9)", map[string]string{"main.go": "package main\nfunc F() {}\n"})

	gt, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 9, Merge: prSHA, Base: "main"}, cutoff, cutoff.Add(time.Hour))
	if err != nil {
		t.Fatalf("GroundTruth with zero later commits: %v", err)
	}
	if gt.LaterCommits != 0 || len(gt.Fixes) != 0 {
		t.Fatalf("expected 0 later commits and 0 fixes, got %+v", gt)
	}
	arm, err := replay.ParseArm("arm=" + armCommand(t, "stable-reject", "main.go", 2))
	if err != nil {
		t.Fatalf("ParseArm: %v", err)
	}
	_, err = replay.Replay(t.Context(), replay.Options{
		RepoDir:    repo.dir,
		Base:       "main",
		OutcomeLog: filepath.Join(t.TempDir(), "outcomes.jsonl"),
		Samples:    []replay.Sample{{PR: replay.PRSpec{Number: 9, Merge: prSHA}, Cutoff: cutoff, Until: cutoff.Add(time.Hour)}},
		Arms:       []replay.Arm{arm},
	})
	if !errors.Is(err, replay.ErrRefused) || !errors.Is(err, replay.ErrNoGroundTruth) {
		t.Fatalf("expected ErrNoGroundTruth refusal, got %v", err)
	}
}

// TestGroundTruthCommitCountCapBoundary pins MaxCommitScan passing and MaxCommitScan+1 refusing.
func TestGroundTruthCommitCountCapBoundary(t *testing.T) {
	baseTime := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	atCapRepo, atCapPR, atCapUntil := repoWithLaterCommits(t, baseTime, replay.MaxCommitScan)
	gt, err := replay.GroundTruth(t.Context(), atCapRepo.dir, replay.PRSpec{Number: 5, Merge: atCapPR, Base: "main"}, baseTime.Add(time.Second), atCapUntil)
	if err != nil || gt.LaterCommits != replay.MaxCommitScan || len(gt.Fixes) != 1 {
		t.Fatalf("at MaxCommitScan (%d) must pass, got gt=%+v err=%v", replay.MaxCommitScan, gt, err)
	}

	overCapRepo, overCapPR, overCapUntil := repoWithLaterCommits(t, baseTime, replay.MaxCommitScan+1)
	_, err = replay.GroundTruth(t.Context(), overCapRepo.dir, replay.PRSpec{Number: 5, Merge: overCapPR, Base: "main"}, baseTime.Add(time.Second), overCapUntil)
	if !errors.Is(err, replay.ErrRefused) || !errors.Is(err, replay.ErrLimitExceeded) {
		t.Fatalf("MaxCommitScan+1 (%d) must refuse with ErrLimitExceeded, got %v", replay.MaxCommitScan+1, err)
	}
}

// repoWithLaterCommits imports a seed commit, a PR commit, and laterCount post-merge commits.
func repoWithLaterCommits(t *testing.T, start time.Time, laterCount int) (*fixtureRepo, string, time.Time) {
	t.Helper()
	repo := newFixtureRepo(t)
	var b strings.Builder
	writeFastImportCommit(&b, 1, 0, start, "seed", "f.txt", "seed\n")
	writeFastImportCommit(&b, 2, 1, start.Add(time.Second), "PR #5", "f.txt", "seed\npr-line\n")
	for i := 1; i <= laterCount && i <= 20000; i++ {
		msg := fmt.Sprintf("unrelated %d", i)
		content := fmt.Sprintf("seed\npr-line\nextra-%d\n", i)
		if i == laterCount {
			msg = "Fixes #5"
		}
		writeFastImportCommit(&b, i+2, i+1, start.Add(time.Duration(i+1)*time.Second), msg, "other.txt", content)
	}
	repo.fastImport(b.String())
	prSHA := repo.git(time.Time{}, "rev-parse", fmt.Sprintf("main~%d", laterCount))
	return repo, prSHA, start.Add(time.Duration(laterCount+2) * time.Second)
}

// writeFastImportCommit emits one deterministic commit into a bounded fixture stream.
func writeFastImportCommit(b *strings.Builder, mark, fromMark int, when time.Time, msg, path, data string) {
	fmt.Fprintf(b, "commit refs/heads/main\nmark :%d\n", mark)
	fmt.Fprintf(b, "author praetor-test <test@example.invalid> %d +0000\n", when.Unix())
	fmt.Fprintf(b, "committer praetor-test <test@example.invalid> %d +0000\n", when.Unix())
	fmt.Fprintf(b, "data %d\n%s\n", len(msg), msg)
	if fromMark > 0 {
		fmt.Fprintf(b, "from :%d\n", fromMark)
	}
	fmt.Fprintf(b, "M 100644 inline %s\ndata %d\n%s\n", path, len(data), data)
}

// TestGroundTruthDiffLineCapBoundary pins MaxDiffLines passing and MaxDiffLines+1 refusing.
func TestGroundTruthDiffLineCapBoundary(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	cutoff := t0.Add(time.Minute)
	until := cutoff.Add(time.Minute)

	atCapRepo, atCapPR := repoWithFixLines(t, t0, cutoff, until, replay.MaxDiffLines)
	if _, err := replay.GroundTruth(t.Context(), atCapRepo.dir, replay.PRSpec{Number: 8, Merge: atCapPR, Base: "main"}, cutoff, until); err != nil {
		t.Fatalf("at MaxDiffLines (%d) must pass, got %v", replay.MaxDiffLines, err)
	}

	overCapRepo, overCapPR := repoWithFixLines(t, t0, cutoff, until, replay.MaxDiffLines+1)
	_, err := replay.GroundTruth(t.Context(), overCapRepo.dir, replay.PRSpec{Number: 8, Merge: overCapPR, Base: "main"}, cutoff, until)
	if !errors.Is(err, replay.ErrRefused) || !errors.Is(err, replay.ErrLimitExceeded) {
		t.Fatalf("MaxDiffLines+1 (%d) must refuse with ErrLimitExceeded, got %v", replay.MaxDiffLines+1, err)
	}
}

// repoWithFixLines creates a PR followed by a repair with a chosen changed-line count.
func repoWithFixLines(t *testing.T, t0, cutoff, until time.Time, addedLines int) (*fixtureRepo, string) {
	t.Helper()
	repo := newFixtureRepo(t)
	repo.commitFiles(t0, "seed", map[string]string{"a.txt": "base\n"})
	prSHA := repo.commitFiles(cutoff, "PR #8", map[string]string{"a.txt": "base\npr\n"})
	lines := strings.Repeat("line\n", addedLines)
	repo.commitFiles(until, "Fixes #8", map[string]string{"fix.txt": lines})
	return repo, prSHA
}

// TestParseArm3D pins positive, negative, and boundary validation on arm specifications.
func TestParseArm3D(t *testing.T) {
	arm, err := replay.ParseArm(`combiner="my-bin" --tree {tree} {drafts}`)
	if err != nil || arm.Name != "combiner" || len(arm.Command) != 4 {
		t.Fatalf("ParseArm valid failed: %+v %v", arm, err)
	}
	for _, bad := range []string{"", "noequals", "=cmd", "bad name=cmd", "arm=", `arm="unclosed`, "arm=bin {unknown}"} {
		if _, err := replay.ParseArm(bad); err == nil {
			t.Errorf("ParseArm(%q) unexpectedly succeeded", bad)
		}
	}
}

// TestNormalizeVerdictAndReplayName3D pins verdict normalization and replay identifier validation.
func TestNormalizeVerdictAndReplayName3D(t *testing.T) {
	if v, err := replay.NormalizeVerdict(" FAIL "); err != nil || v != replay.VerdictChangesRequested {
		t.Fatalf("NormalizeVerdict FAIL = %q, %v", v, err)
	}
	if v, err := replay.NormalizeVerdict("Approve"); err != nil || v != replay.VerdictApprove {
		t.Fatalf("NormalizeVerdict Approve = %q, %v", v, err)
	}
	if _, err := replay.NormalizeVerdict("maybe"); err == nil {
		t.Fatal("NormalizeVerdict(maybe) must fail")
	}
	if !replay.ValidReplayName("arm-1") || replay.ValidReplayName("") || replay.ValidReplayName("bad name") {
		t.Fatal("ValidReplayName 3D check failed")
	}
}

// TestReviewParseArmLimits pins byte and argument limits on both sides of each boundary.
func TestReviewParseArmLimits(t *testing.T) {
	cases := []struct {
		spec  string
		valid bool
	}{
		{"a=" + strings.Repeat("x", 4094), true},
		{"a=" + strings.Repeat("x", 4095), false},
		{"a=x" + strings.Repeat(" x", 63), true},
		{"a=x" + strings.Repeat(" x", 64), false},
	}
	for i := 0; i < len(cases) && i < 4; i++ {
		_, err := replay.ParseArm(cases[i].spec)
		if (err == nil) != cases[i].valid {
			t.Errorf("case %d valid=%t: %v", i, cases[i].valid, err)
		}
	}
}

// TestReviewRecentCutoffLongHistory permits old history beyond the relevant cutoff window.
func TestReviewRecentCutoffLongHistory(t *testing.T) {
	start := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	repo, _, until := repoWithLaterCommits(t, start, replay.MaxCommitScan+1)
	want := repo.git(time.Time{}, "rev-parse", "HEAD")
	got, err := replay.Cutoff(t.Context(), repo.dir, "main", until)
	if err != nil || got != want {
		t.Errorf("recent cutoff: %s %v", got, err)
	}
	if err := replay.VerifyTree(t.Context(), repo.dir, until); err != nil {
		t.Errorf("old history: %v", err)
	}
}

// TestReviewMergeMessageLimit rejects merge ranges whose messages would otherwise be truncated.
func TestReviewMergeMessageLimit(t *testing.T) {
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	repo, pr, until := repoWithLaterCommits(t, start, replay.MaxCommitScan+1)
	repo.git(time.Time{}, "branch", "repairs")
	repo.git(time.Time{}, "reset", "--hard", pr)
	repo.git(until, "merge", "--quiet", "--no-ff", "-m", "merge repairs", "repairs")
	_, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 5, Merge: pr, Base: "main"}, start.Add(time.Second), until)
	if !errors.Is(err, replay.ErrLimitExceeded) {
		t.Fatalf("truncated messages accepted: %v", err)
	}
}

// TestReviewRepairWindowIgnoresLongFutureHistory bounds queried output before parsing.
func TestReviewRepairWindowIgnoresLongFutureHistory(t *testing.T) {
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	repo, pr, _ := repoWithLaterCommits(t, start, 16385)
	gt, err := replay.GroundTruth(t.Context(), repo.dir, replay.PRSpec{Number: 5, Merge: pr, Base: "main"}, start.Add(time.Second), start.Add(2*time.Second))
	if err != nil || gt.LaterCommits != 1 {
		t.Fatalf("irrelevant future history affected window: %+v %v", gt, err)
	}
}
