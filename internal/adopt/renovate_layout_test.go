// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientjson"
)

// tabRenovate is a tab-indented Renovate configuration as a formatter set to tabs writes it, the
// planted case of #1251.
const tabRenovate = "{\n\t\"$schema\": \"https://docs.renovatebot.com/renovate-schema.json\",\n" +
	"\t\"extends\": [\n\t\t\"config:recommended\"\n\t],\n\t\"minimumReleaseAge\": \"3 days\"\n}\n"

// tabRenovateWithRules adds an adopter rule written on one line to tabRenovate.
const tabRenovateWithRules = "{\n\t\"extends\": [\n\t\t\"config:recommended\"\n\t],\n" +
	"\t\"packageRules\": [\n\t\t{\"matchManagers\": [\"github-actions\"], \"groupName\": \"GitHub Actions\"}\n\t]\n}\n"

// addedBlock returns the lines out adds to before as one block, after checking that out is
// before with that block inserted and, as JSON needs, a comma ended onto the line above it.
func addedBlock(t *testing.T, before, out string) []string {
	t.Helper()
	was, now := strings.Split(before, "\n"), strings.Split(out, "\n")
	head := 0
	for head < len(was) && head < len(now) && was[head] == now[head] {
		head++
	}
	tail := 0
	for tail < len(was)-head && tail < len(now)-head && was[len(was)-1-tail] == now[len(now)-1-tail] {
		tail++
	}
	changed, added := was[head:len(was)-tail], now[head:len(now)-tail]
	if len(changed) != 1 || len(added) < 2 || added[0] != changed[0]+"," {
		t.Fatalf("the rewrite changed more than one comma and one added block:\nbefore\n%s\nafter\n%s", before, out)
	}
	return added[1:]
}

// Positive: the planted case. Adoption adds its packageRules entry to a tab-indented
// configuration; the file keeps its tabs and the diff holds only the added entry (#1251).
func TestMergeRenovateRuleKeepsTabIndentation(t *testing.T) {
	out, _, changed, err := mergeRenovateRule(t.Context(), []byte(tabRenovate), []string{".github/workflows/reuse.yml"})
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}
	block := addedBlock(t, tabRenovate, string(out))
	for _, line := range block {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimLeft(line, "\t"), " ") {
			t.Errorf("added line %q is not tab-indented", line)
		}
	}
	if block[0] != "\t\"packageRules\": [" || block[len(block)-1] != "\t]" {
		t.Errorf("the added block is not the packageRules member:\n%s", strings.Join(block, "\n"))
	}
	if err := clientjson.Validate(t.Context(), out); err != nil {
		t.Fatalf("the rewritten configuration does not load with the duplicate-refusing reader: %v", err)
	}
	if _, managed, _ := renovateTestRules(t, string(out)); len(managed) != 1 || !slices.Equal(managed[0].MatchFileNames, []string{".github/workflows/reuse.yml"}) {
		t.Fatalf("the managed entry is not the one written: %+v", managed)
	}
}

// Negative: an adopter rule written on one line is not rewritten, neither when the managed
// entry is appended after it nor when its paths change; four-space files keep four spaces.
func TestMergeRenovateRuleLeavesAdopterRulesAsWritten(t *testing.T) {
	ctx := t.Context()
	adopterRule := "\t\t{\"matchManagers\": [\"github-actions\"], \"groupName\": \"GitHub Actions\"}"
	out, _, _, err := mergeRenovateRule(ctx, []byte(tabRenovateWithRules), []string{"a.yml"})
	if err != nil {
		t.Fatal(err)
	}
	block := addedBlock(t, tabRenovateWithRules, string(out))
	if block[0] != "\t\t{" || !strings.Contains(string(out), adopterRule+",\n") {
		t.Fatalf("the managed entry was not appended after the untouched adopter rule:\n%s", out)
	}
	moved, _, changed, err := mergeRenovateRule(ctx, out, []string{"b.yml"})
	if err != nil || !changed || !strings.Contains(string(moved), adopterRule+",\n") ||
		strings.Count(string(moved), "\n") != strings.Count(string(out), "\n") {
		t.Fatalf("a path change rewrote more than the managed entry: changed=%v err=%v\n%s", changed, err, moved)
	}
	spaced := strings.ReplaceAll(tabRenovate, "\t", "    ")
	out, _, _, err = mergeRenovateRule(ctx, []byte(spaced), []string{"a.yml"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range addedBlock(t, spaced, string(out)) {
		if strings.Contains(line, "\t") || (len(line)-len(strings.TrimLeft(line, " ")))%4 != 0 {
			t.Errorf("added line %q is not in the file's four-space indentation", line)
		}
	}
}

// Boundary: removing the managed entry again gives back the configuration byte for byte, and a
// CRLF configuration keeps CRLF on every line, the added ones included.
func TestMergeRenovateRuleRoundTripsAndKeepsLineEndings(t *testing.T) {
	ctx := t.Context()
	added, _, _, err := mergeRenovateRule(ctx, []byte(tabRenovateWithRules), []string{"a.yml"})
	if err != nil {
		t.Fatal(err)
	}
	back, _, changed, err := mergeRenovateRule(ctx, added, nil)
	if err != nil || !changed || string(back) != tabRenovateWithRules {
		t.Fatalf("removing the managed entry = %q, changed=%v err=%v; want the original", back, changed, err)
	}
	crlf := strings.ReplaceAll(tabRenovate, "\n", "\r\n")
	out, _, _, err := mergeRenovateRule(ctx, []byte(crlf), []string{"a.yml"})
	if err != nil || strings.Count(string(out), "\r\n") != strings.Count(string(out), "\n") ||
		!strings.HasPrefix(string(out), strings.TrimSuffix(crlf, "\r\n}\r\n")) {
		t.Fatalf("CRLF configuration = %q, %v", out, err)
	}
}
