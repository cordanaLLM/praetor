// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadedHarness loads repo's harness.json as the audit does.
func loadedHarness(t *testing.T, repo string) *Harness {
	t.Helper()
	h, err := LoadHarnessContext(t.Context(), filepath.Join(repo, paperclipDir, harnessFile))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// editHarnessFile replaces from with to in repo's .paperclip/name.
func editHarnessFile(t *testing.T, repo, name, from, to string) {
	t.Helper()
	path := filepath.Join(repo, paperclipDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s lacks %q", name, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Positive: the written synthesis is current, not operator-owned, with rules.md returned for the
// register gate, and so is its uniformly CRLF checkout. A harness.json edited by hand beside the
// rules.md rendering it is operator-owned and passes, as adoption keeps it (#502).
func TestCompareGenerated_Positive_SynthesisCRLFAndOwnedHarness(t *testing.T) {
	repo := identifiedRepo(t)
	h, _, rules := writtenRules(t, repo)
	got, err := CompareGenerated(t.Context(), repo, loadedHarness(t, repo), h)
	if err != nil || got.Owned || !got.RulesExist || got.Rules != rules {
		t.Fatalf("written synthesis: %+v, %v", got, err)
	}
	for _, name := range []string{harnessFile, rulesFile} {
		path := filepath.Join(repo, paperclipDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "\n", "\r\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := CompareGenerated(t.Context(), repo, loadedHarness(t, repo), h); err != nil || got.Owned {
		t.Fatalf("a CRLF checkout of the synthesis must be current: %+v, %v", got, err)
	}
	owned := identifiedRepo(t)
	edited, _, _ := writtenRules(t, owned)
	edited.OperatingContract[0] = "Branch push != shipping. Open PR plus green CI required."
	if err := WriteHarness(edited, owned); err != nil {
		t.Fatal(err)
	}
	if got, err := CompareGenerated(t.Context(), owned, loadedHarness(t, owned), h); err != nil || !got.Owned {
		t.Fatalf("an edited harness.json with its rendering must pass as operator-owned: %+v, %v", got, err)
	}
}

// Negative: a hand-edited rules.md is drift naming the file and the rendered line it lacks, also
// beside an operator-owned harness.json; one edited to mixed line endings is compared byte for
// byte; a harness.json of this release under other facts is stale earlier output, never owned.
func TestCompareGenerated_Negative_EditedRulesAndStaleHarness(t *testing.T) {
	cases := map[string]struct {
		file, from, to string
		want           error
		named          string
	}{
		"edited rules": {rulesFile, "Rule 0 Terminal", "Rule Zero Terminal", ErrRulesDrift, "rules.md"},
		"edited invariant in rules": {rulesFile, "HISS-15: positive", "HISS-15: some", ErrRulesDrift,
			`first expected line it lacks: "- HISS-15: positive`},
		"mixed endings":     {rulesFile, "## Operating Contract\n", "## Operating Contract\r\n", ErrRulesDrift, "compared byte for byte"},
		"owned json, rules": {harnessFile, "HISS-15: positive", "HISS-15: some", ErrRulesDrift, "rules.md"},
	}
	for name, tc := range cases {
		repo := identifiedRepo(t)
		h, _, _ := writtenRules(t, repo)
		editHarnessFile(t, repo, tc.file, tc.from, tc.to)
		_, err := CompareGenerated(t.Context(), repo, loadedHarness(t, repo), h)
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.named) {
			t.Fatalf("%s: %v, want %v naming %q", name, err, tc.want, tc.named)
		}
	}
	repo, current := writtenWidget(t, "", pinnedReceipt, languageFacts(0), languageFacts(0))
	_, err := CompareGenerated(t.Context(), repo, loadedHarness(t, repo), current)
	if !errors.Is(err, ErrHarnessStale) || !strings.Contains(err.Error(), ".paperclip/harness.json") {
		t.Fatalf("earlier output under the unpinned receipt row: %v, want ErrHarnessStale naming harness.json", err)
	}
}

// Boundary: an absent rules.md passes, since adoption keeps a removed one removed; a missing
// harness.json is an error, not a pass; a nil loaded or expected harness is refused.
func TestCompareGenerated_Boundary_AbsentRulesAndMissingHarness(t *testing.T) {
	repo := identifiedRepo(t)
	h, _, _ := writtenRules(t, repo)
	loaded := loadedHarness(t, repo)
	if err := os.Remove(filepath.Join(repo, paperclipDir, rulesFile)); err != nil {
		t.Fatal(err)
	}
	if got, err := CompareGenerated(t.Context(), repo, loaded, h); err != nil || got.RulesExist || got.Rules != "" {
		t.Fatalf("absent rules.md: %+v, %v", got, err)
	}
	if err := os.Remove(filepath.Join(repo, paperclipDir, harnessFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := CompareGenerated(t.Context(), repo, loaded, h); err == nil {
		t.Fatal("a missing harness.json must not pass")
	}
	if _, err := CompareGenerated(t.Context(), repo, loaded, nil); err == nil {
		t.Fatal("a nil expected harness must be refused")
	}
	if _, err := CompareGenerated(t.Context(), repo, nil, h); err == nil {
		t.Fatal("a nil loaded harness must be refused")
	}
}
