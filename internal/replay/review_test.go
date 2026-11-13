// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"errors"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/router"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReviewVerdictDecoderBoundary rejects trailing output and excessive findings.
func TestReviewVerdictDecoderBoundary(t *testing.T) {
	for i, suffix := range []string{"", " \n", " {}", " noise"} {
		_, _, err := decodeArmVerdict([]byte(`{"verdict":"approve"}` + suffix))
		if (err == nil) != (i < 2) {
			t.Errorf("suffix %q: %v", suffix, err)
		}
	}
}

// TestReviewDistinctFindingPaths preserves repository-relative directories named a and b.
func TestReviewDistinctFindingPaths(t *testing.T) {
	h := FixHunk{File: "a/b/foo", StartLine: 2, EndLine: 2}
	if findingsHitHunk(h, []Finding{{File: "foo", Line: 2}}) {
		t.Fatal("distinct paths collided")
	}
	if !findingsHitHunk(h, []Finding{{File: "a/b/foo", Line: 2}}) {
		t.Fatal("same file missed")
	}
	if got := cleanDiffPath("a/b/foo"); got != "b/foo" {
		t.Fatalf("directory stripped: %s", got)
	}
}

// TestReviewDiffContentHeadersCountTowardLimit distinguishes hunk contents from file headers.
func TestReviewDiffContentHeadersCountTowardLimit(t *testing.T) {
	for n := MaxDiffLines; n <= MaxDiffLines+1; n++ {
		diff := fmt.Sprintf("--- /dev/null\n+++ b/real\n@@ -0,0 +1,%d @@\n", n) + strings.Repeat("+++ content\n", n)
		hunks, err := parseDiffHunksBounded([]byte(diff))
		if n == MaxDiffLines && (err != nil || len(hunks) != 1 || hunks[0].newFile != "real") {
			t.Fatalf("at cap: %+v %v", hunks, err)
		}
		if n > MaxDiffLines && !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("over cap accepted: %v", err)
		}
	}
}

// TestReviewAggregateHunkLimit prevents denominators exceeding the scoring loop bound.
func TestReviewAggregateHunkLimit(t *testing.T) {
	var hunks []FixHunk
	for i := 0; i < MaxDiffLines; i++ {
		hunks = append(hunks, FixHunk{File: "f", StartLine: i + 1, EndLine: i + 1})
	}
	same, err := appendUniqueHunks(hunks, []FixHunk{hunks[0]})
	if err != nil || len(same) != MaxDiffLines {
		t.Fatalf("duplicate at cap: %d %v", len(same), err)
	}
	got, err := appendUniqueHunks(hunks, []FixHunk{{File: "f", StartLine: MaxDiffLines + 1, EndLine: MaxDiffLines + 1}})
	if !errors.Is(err, ErrLimitExceeded) || len(got) > MaxDiffLines {
		t.Fatalf("aggregate exceeded scoring bound: %d", len(got))
	}
}

// TestReviewBlameFinalCoordinates keeps original and current line numbers separate.
func TestReviewBlameFinalCoordinates(t *testing.T) {
	sha := strings.Repeat("a", 40)
	low, high, ok := extractPRBlameSpan(sha+" 10 110 1\n", map[string]bool{sha: true})
	if !ok || low != 110 || high != 110 {
		t.Fatalf("mixed coordinates: %d %d %t", low, high, ok)
	}
}

// TestReviewSyntheticDraftWritesRespectCancellation prevents writes after the caller expires.
func TestReviewSyntheticDraftWritesRespectCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := writeSyntheticDrafts(ctx, dir, "commit", []byte("patch")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled writes changed directory: %+v %v", entries, err)
	}
	drafts, err := writeSyntheticDrafts(t.Context(), dir, "commit", []byte("patch"))
	if err != nil || len(drafts) != 2 {
		t.Fatalf("valid drafts: %+v %v", drafts, err)
	}
}

// TestReviewOutcomeValidationLeavesExistingLogIntact refuses a later invalid row atomically.
func TestReviewOutcomeValidationLeavesExistingLogIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	score := ArmScore{Arm: "valid", Suite: "suite", Cutoff: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), VerdictMatch: true, SampleCount: 1}
	row := buildReplayOutcome(score, Arm{Name: "valid", Command: []string{"cmd"}}, sha256Prefixed("ctx"))
	if err := router.AppendOutcome(t.Context(), path, row); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := row
	bad.Target = ""
	if err := publishReplayOutcomes(t.Context(), path, []router.Outcome{row, bad}); err == nil {
		t.Fatal("invalid row accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed publication changed existing log: %v", err)
	}
	if err := publishReplayOutcomes(t.Context(), path, []router.Outcome{row, row}); err != nil {
		t.Fatal(err)
	}
	rows, err := router.ReadOutcomes(t.Context(), path)
	if err != nil || len(rows) != 3 {
		t.Fatalf("transaction readback: %d %v", len(rows), err)
	}
}

// TestReviewOutcomeLogPastSnapshotBound appends replay rows to a log larger than the 1 MiB snapshot
// bound: the outcome log keeps its own record bound, so publication must not reread the whole file.
func TestReviewOutcomeLogPastSnapshotBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	score := ArmScore{Arm: "valid", Suite: "suite", Cutoff: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), VerdictMatch: true, SampleCount: 1}
	row := buildReplayOutcome(score, Arm{Name: "valid", Command: []string{"cmd"}}, sha256Prefixed("ctx"))
	big := make([]router.Outcome, router.MaxOutcomeBatch)
	for i := range big {
		big[i] = row
	}
	for written := 0; written < 64; written++ {
		if err := router.AppendOutcomes(t.Context(), path, big); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
			break
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 1<<20 {
		t.Fatalf("fixture log not past 1 MiB: %v", err)
	}
	if err := publishReplayOutcomes(t.Context(), path, []router.Outcome{row}); err != nil {
		t.Fatalf("replay rows refused on a log past 1 MiB: %v", err)
	}
}

// TestReviewAnnotationBoundaries rejects malformed oracles and preserves order semantics at the cap.
func TestReviewAnnotationBoundaries(t *testing.T) {
	if err := validateSampleAnnotations(Sample{AddedVerified: []Finding{{File: "f", Line: 0}}}); err == nil {
		t.Fatal("invalid oracle accepted")
	}
	annotations := make([]Finding, MaxFindings)
	for i := 0; i < len(annotations) && i < MaxFindings; i++ {
		annotations[i] = Finding{File: "f", Line: i + 1}
	}
	if err := validateSampleAnnotations(Sample{AddedVerified: annotations}); err != nil {
		t.Fatal(err)
	}
	if err := validateSampleAnnotations(Sample{AddedVerified: append(annotations, Finding{File: "f", Line: 257})}); err == nil {
		t.Fatal("over-limit oracle accepted")
	}
	if got := countAnnotations(annotations, annotations, nil, true); got == nil || *got != 0 {
		t.Fatalf("one order counted as retained: %v", got)
	}
	if got := countAnnotations(annotations, annotations, nil, false); got == nil || *got != MaxFindings {
		t.Fatalf("one order fabrication omitted: %v", got)
	}
}

// TestReviewRepairNewFilesHaveNoCutoffSpan avoids crediting files first introduced by a repair.
func TestReviewRepairNewFilesHaveNoCutoffSpan(t *testing.T) {
	h := parsedHunk{newFile: "new.txt", newStart: 1, newCount: 1}
	spans := mapCutoffSpan(h.toFixHunk(), nil)
	if len(spans) != 0 {
		t.Fatalf("repair-only file credited in cutoff: %+v", spans)
	}
}
