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

// Positive: the written synthesis matches, and so does its uniformly CRLF checkout.
func TestCompareGenerated_Positive_SynthesisAndCRLFCheckout(t *testing.T) {
	repo := identifiedRepo(t)
	h, _, rules := writtenRules(t, repo)
	got, err := CompareGenerated(t.Context(), repo, h)
	if err != nil || got != rules {
		t.Fatalf("written synthesis: %v (rules %q)", err, got)
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
	if _, err := CompareGenerated(t.Context(), repo, h); err != nil {
		t.Fatalf("a CRLF checkout of the synthesis must match: %v", err)
	}
}

// Negative: a hand-edited rules.md or harness.json, and one edited to mixed line endings, is
// drift naming the file and the synthesized line it lacks.
func TestCompareGenerated_Negative_EditedFilesDrift(t *testing.T) {
	cases := map[string]struct {
		file, from, to, want string
	}{
		"edited rules":   {rulesFile, "Rule 0 Terminal", "Rule Zero Terminal", "rules.md"},
		"edited harness": {harnessFile, "HISS-15: positive", "HISS-15: some", "harness.json"},
		"mixed endings":  {rulesFile, "## Operating Contract\n", "## Operating Contract\r\n", "compared byte for byte"},
	}
	for name, tc := range cases {
		repo := identifiedRepo(t)
		h, _, _ := writtenRules(t, repo)
		path := filepath.Join(repo, paperclipDir, tc.file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), tc.from) {
			t.Fatalf("%s: fixture lacks %q", name, tc.from)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), tc.from, tc.to, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = CompareGenerated(t.Context(), repo, h)
		if !errors.Is(err, ErrHarnessDrift) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v, want ErrHarnessDrift naming %q", name, err, tc.want)
		}
	}
}

// Boundary: an absent rules.md passes, since adoption keeps a removed one removed; a missing
// harness.json is an error, not a pass; a nil expected harness is refused.
func TestCompareGenerated_Boundary_AbsentRulesAndMissingHarness(t *testing.T) {
	repo := identifiedRepo(t)
	h, _, _ := writtenRules(t, repo)
	if err := os.Remove(filepath.Join(repo, paperclipDir, rulesFile)); err != nil {
		t.Fatal(err)
	}
	if rules, err := CompareGenerated(t.Context(), repo, h); err != nil || rules != "" {
		t.Fatalf("absent rules.md: %q, %v", rules, err)
	}
	if err := os.Remove(filepath.Join(repo, paperclipDir, harnessFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := CompareGenerated(t.Context(), repo, h); err == nil {
		t.Fatal("a missing harness.json must not pass")
	}
	if _, err := CompareGenerated(t.Context(), repo, nil); err == nil {
		t.Fatal("a nil expected harness must be refused")
	}
}
