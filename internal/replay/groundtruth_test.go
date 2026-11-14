// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"strings"
	"testing"
)

// TestMatchCommitMessageSubject pins normalization, minimum length, and generic-prefix refusal.
func TestMatchCommitMessageSubject(t *testing.T) {
	cases := []struct {
		name, subject, message, want string
	}{
		{"later repair", "feat(replay): derive replay ground truth (#1932)", "fix(replay): handle empty bundles after derive replay ground truth", "subject"},
		{"full subject", "feat(replay): derive replay ground truth (#1932)", "fix(replay): handle empty bundles after feat(replay): derive replay ground truth (#1932)", "subject"},
		{"case and space", " FEAT(replay)!: Derive\tReplay   Ground Truth (#1932) ", "follow up: DERIVE  REPLAY\nGROUND\tTRUTH", "subject"},
		{"bare PR suffix", "feat: derive replay ground truth #1932", "after derive replay ground truth", "subject"},
		{"no prefix", "derive replay ground truth", "after derive replay ground truth", "subject"},
		{"only type mentioned", "fix(replay): derive replay ground truth", "fix(replay):", ""},
		{"only type subject", "fix(replay):", "fix(replay): handle empty bundles", ""},
		{"long type only", "fix(very-long-replay-scope):", "fix(very-long-replay-scope): handle empty bundles", ""},
		{"short subject", "fix(replay): empty bundles (#1932)", "handle empty bundles", ""},
		{"below minimum", strings.Repeat("x", 15), strings.Repeat("x", 15), ""},
		{"at minimum", strings.Repeat("x", 16), "after " + strings.Repeat("x", 16), "subject"},
		{"unicode below minimum", strings.Repeat("界", 15), strings.Repeat("界", 15), ""},
		{"unicode at minimum", strings.Repeat("界", 16), strings.Repeat("界", 16), "subject"},
		{"empty subject", "", "fix(replay): handle empty bundles", ""},
		{"unrelated", "feat: derive replay ground truth", "fix: handle empty bundles", ""},
	}
	for i := 0; i < len(cases) && i < MaxCommitScan; i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			tc := cases[i]
			if got := matchCommitMessage(tc.message, 0, nil, []string{tc.subject}); got != tc.want {
				t.Errorf("matchCommitMessage(%q, %q) = %q, want %q", tc.message, tc.subject, got, tc.want)
			}
		})
	}
}

// TestMatchCommitMessageExistingRules preserves number, prefix, and exact Revert semantics.
func TestMatchCommitMessageExistingRules(t *testing.T) {
	cases := []struct{ message, want string }{
		{"Fixes #42", "pr #42"},
		{"Follow-up PR 42", "pr #42"},
		{"Fixes #420", ""},
		{"repair abcdef0", "commit abcdef0"},
		{"repair abcdef", ""},
		{`Revert "fix: short"`, "revert subject"},
		{`Revert "feat: derive replay ground truth"`, "revert subject"},
		{`revert "fix: short"`, ""},
	}
	for i := 0; i < len(cases) && i < MaxCommitScan; i++ {
		tc := cases[i]
		got := matchCommitMessage(tc.message, 42, []string{"abcdef0123456789abcdef0123456789abcdef01"}, []string{"fix: short", "feat: derive replay ground truth"})
		if got != tc.want {
			t.Errorf("matchCommitMessage(%q) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

// TestMatchCommitMessageSubjectCap checks the last permitted subject and excludes the next.
func TestMatchCommitMessageSubjectCap(t *testing.T) {
	subjects := make([]string, MaxCommitScan+1)
	subjects[MaxCommitScan] = "derive replay ground truth"
	if got := matchCommitMessage(subjects[MaxCommitScan], 0, nil, subjects); got != "" {
		t.Fatalf("subject beyond MaxCommitScan matched: %q", got)
	}
	subjects[MaxCommitScan-1] = subjects[MaxCommitScan]
	if got := matchCommitMessage(subjects[MaxCommitScan], 0, nil, subjects); got != "subject" {
		t.Fatalf("subject at MaxCommitScan = %q, want subject", got)
	}
}
