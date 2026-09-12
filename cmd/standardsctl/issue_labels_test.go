// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestIssueLabelIndex_Positive_MergePreservesOtherLabels(t *testing.T) {
	idx := newIssueLabelIndex()
	idx.record("cordanaLLM/praetor", forge.IssueSpec{
		ID:     42,
		Labels: []string{"status/blocked", "type/bug", "area/hiss", "priority/P1"},
	})

	merged, known := idx.mergedReadyLabels("cordanaLLM/praetor", 42)
	if !known {
		t.Fatal("expected the recorded issue to be known")
	}
	joined := strings.Join(merged, ",")
	for _, keep := range []string{"type/bug", "area/hiss", "priority/P1", readyLabel} {
		if !strings.Contains(joined, keep) {
			t.Errorf("label %q must survive the transition, got %v", keep, merged)
		}
	}
	if strings.Contains(joined, "status/blocked") {
		t.Errorf("status/blocked must be removed, got %v", merged)
	}
}

func TestIssueLabelIndex_Negative_UnknownIssueIsNotTransitioned(t *testing.T) {
	idx := newIssueLabelIndex()

	if _, known := idx.mergedReadyLabels("cordanaLLM/praetor", 7); known {
		t.Error("an unobserved issue must not be reported as known, because a label PATCH replaces the whole set")
	}

	var nilIdx issueLabelIndex
	nilIdx.record("cordanaLLM/praetor", forge.IssueSpec{ID: 1}) // must not panic
	if _, known := nilIdx.mergedReadyLabels("cordanaLLM/praetor", 1); known {
		t.Error("a nil index must report nothing as known")
	}
}

func TestIssueLabelIndex_Boundary_EmptyAndAlreadyReady(t *testing.T) {
	idx := newIssueLabelIndex()
	idx.record("a/b", forge.IssueSpec{ID: 1, Labels: nil})
	idx.record("a/b", forge.IssueSpec{ID: 2, Labels: []string{"BLOCKED", readyLabel}})

	merged, known := idx.mergedReadyLabels("a/b", 1)
	if !known || len(merged) != 1 || merged[0] != readyLabel {
		t.Errorf("an issue with no labels must end up with exactly the ready label, got %v", merged)
	}

	merged, known = idx.mergedReadyLabels("a/b", 2)
	if !known || len(merged) != 1 || merged[0] != readyLabel {
		t.Errorf("case-insensitive blocked markers and a duplicate ready label must collapse, got %v", merged)
	}
}

func TestIsBlockedLabel_3D(t *testing.T) {
	if !isBlockedLabel("status/blocked") || !isBlockedLabel("BLOCKED") {
		t.Error("expected blocked markers to match case-insensitively")
	}
	if isBlockedLabel("type/bug") {
		t.Error("unrelated labels must not be treated as blocked markers")
	}
	if isBlockedLabel("") {
		t.Error("an empty label must not be treated as a blocked marker")
	}
}

func TestParseTargetRepos_3D(t *testing.T) {
	got := parseTargetRepos("praetor, golusoris/golusoris ,", "cordanaLLM")
	if len(got) != 2 || got[0] != "cordanaLLM/praetor" || got[1] != "golusoris/golusoris" {
		t.Fatalf("unexpected repo list: %v", got)
	}
	if len(parseTargetRepos("", "cordanaLLM")) != 0 {
		t.Error("an empty repos flag must yield no targets")
	}

	long := strings.Repeat("a/b,", maxReconciledRepos+50)
	if got := len(parseTargetRepos(long, "cordanaLLM")); got > maxReconciledRepos {
		t.Errorf("repo fan-out must respect the %d bound, got %d", maxReconciledRepos, got)
	}
}
