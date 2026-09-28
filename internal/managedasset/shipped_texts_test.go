// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every text a family ever shipped at a managed path is recorded in its ledger under
// testdata/shipped, one "<sha256>  <path>" line per text, oldest first. The last line of a path
// is its current text; every earlier line must be in the family's Prior and every Prior entry
// must be an earlier line. A change to a managed text, including one Renovate makes to the
// hosted workflow's action pins, therefore fails TestShippedTextLedger twice over: first
// because the ledger's last line no longer matches, then, once PRAETOR_UPDATE_SHIPPED_TEXTS=1
// has appended the new digest, because the outgoing text is not yet in Prior. Adoption only
// refreshes an unedited earlier text without --force when it is in Prior, so the ledger keeps
// that promise from lapsing on the next text change.
const (
	shippedTextsDir       = "testdata/shipped"
	updateShippedTextsEnv = "PRAETOR_UPDATE_SHIPPED_TEXTS"
	// maxLedgerLines bounds one ledger: every Prior text plus a current text per managed path,
	// with room for comments.
	maxLedgerLines = 4 * MaxPriorTexts
	// maxLedgerProblems bounds what one check reports.
	maxLedgerProblems = 32
)

// shippedText is one ledger line: the LF-text digest of a text shipped at rel.
type shippedText struct {
	digest string
	rel    string
}

// ledgerPath names the ledger of f: its name in lower case, spaces as dashes.
func ledgerPath(f Family) string {
	return filepath.Join(shippedTextsDir, strings.ReplaceAll(strings.ToLower(f.Name), " ", "-")+".sha256")
}

// parseLedger reads "<digest>  <path>" lines, skipping blank lines and # comments.
func parseLedger(text string) ([]shippedText, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > maxLedgerLines {
		return nil, fmt.Errorf("ledger holds %d lines, want at most %d", len(lines), maxLedgerLines)
	}
	entries := make([]shippedText, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !isTextDigest(fields[0]) {
			return nil, fmt.Errorf("ledger line %d is not \"<sha256>  <path>\": %q", index+1, line)
		}
		entries = append(entries, shippedText{digest: fields[0], rel: fields[1]})
	}
	return entries, nil
}

// currentTextDigests maps every managed path of f to the digest of its current text.
func currentTextDigests(f Family) (map[string]string, error) {
	current := map[string]string{}
	for _, rel := range f.ManagedPaths() {
		data, _, err := f.Canonical(rel)
		if err != nil {
			return nil, err
		}
		current[rel] = textDigest(string(data))
	}
	return current, nil
}

// splitLedger returns the last digest of each path and every earlier entry as digest -> path,
// with a problem for a path outside the family or a digest listed twice.
func splitLedger(f Family, entries []shippedText) (last, earlier map[string]string, problems []string) {
	last, earlier = map[string]string{}, map[string]string{}
	seen := map[string]bool{}
	managed := f.ManagedPaths()
	for _, entry := range entries {
		switch {
		case !slices.Contains(managed, entry.rel):
			problems = append(problems, fmt.Sprintf("%s is not a managed path of %s", entry.rel, f.Name))
			continue
		case seen[entry.digest]:
			problems = append(problems, fmt.Sprintf("%s is listed twice; a text that returned to an earlier one keeps only its last line", entry.digest))
		}
		seen[entry.digest] = true
		if previous, ok := last[entry.rel]; ok {
			earlier[previous] = entry.rel
		}
		last[entry.rel] = entry.digest
	}
	return last, earlier, problems
}

// textHolder names the source file whose history holds the texts of rel: Source for the
// hosted workflow, which is a Go constant there, and the asset itself otherwise.
func textHolder(f Family, rel string) string {
	if rel == f.WorkflowFile {
		return f.Source
	}
	return rel
}

// ledgerProblems reports every way entries break the prior-text contract of f, sorted.
func ledgerProblems(f Family, entries []shippedText, current map[string]string) []string {
	last, earlier, problems := splitLedger(f, entries)
	for _, rel := range f.ManagedPaths() {
		if last[rel] != current[rel] {
			problems = append(problems, fmt.Sprintf("the text of %s is %s but the ledger ends at %q: append it with %s=1, then add the outgoing text to Prior", rel, current[rel], last[rel], updateShippedTextsEnv))
		}
	}
	for digest, rel := range earlier {
		if f.Prior[digest] != rel {
			problems = append(problems, fmt.Sprintf("the earlier text %s of %s is not in Prior: add it to Prior and keep the text as a fixture (git log -p -- %s holds it)", digest, rel, textHolder(f, rel)))
		}
	}
	for digest, rel := range f.Prior {
		if earlier[digest] != rel {
			problems = append(problems, fmt.Sprintf("Prior lists %s for %s but the ledger records no such earlier text", digest, rel))
		}
	}
	slices.Sort(problems)
	return problems[:min(len(problems), maxLedgerProblems)]
}

// appendChangedTexts appends a line for every managed path whose current text is not its last
// ledger line; it never rewrites or drops an existing line.
func appendChangedTexts(f Family, text string, entries []shippedText, current map[string]string) string {
	last, _, _ := splitLedger(f, entries)
	for _, rel := range f.ManagedPaths() {
		if last[rel] != current[rel] {
			text += current[rel] + "  " + rel + "\n"
		}
	}
	return text
}

func readLedger(t *testing.T, f Family) (string, []shippedText, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(ledgerPath(f))
	if err != nil {
		t.Fatalf("family %s has no shipped-text ledger: %v", f.Name, err)
	}
	return string(raw), mustParseLedger(t, string(raw)), mustCurrentDigests(t, f)
}

func mustParseLedger(t *testing.T, text string) []shippedText {
	t.Helper()
	entries, err := parseLedger(text)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func mustCurrentDigests(t *testing.T, f Family) map[string]string {
	t.Helper()
	current, err := currentTextDigests(f)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

// Positive: every family's ledger ends at its current texts and its earlier lines are exactly
// its Prior. With PRAETOR_UPDATE_SHIPPED_TEXTS=1 the changed texts are appended first.
func TestShippedTextLedger(t *testing.T) {
	for _, family := range Families() {
		text, entries, current := readLedger(t, family)
		if os.Getenv(updateShippedTextsEnv) == "1" {
			text = appendChangedTexts(family, text, entries, current)
			if err := os.WriteFile(ledgerPath(family), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		for _, problem := range ledgerProblems(family, entries, current) {
			t.Errorf("%s: %s", ledgerPath(family), problem)
		}
	}
}

// Negative: a changed managed text fails the ledger, appending its digest still fails until
// the outgoing text is in Prior, and a Prior entry or a path the ledger does not know fails.
func TestShippedTextLedgerNegative(t *testing.T) {
	family := Families()[0]
	text, entries, _ := readLedger(t, family)
	family.Workflow += "# a pin moved\n"
	current := mustCurrentDigests(t, family)
	assertLedgerProblem(t, ledgerProblems(family, entries, current), "but the ledger ends at")
	appended := mustParseLedger(t, appendChangedTexts(family, text, entries, current))
	assertLedgerProblem(t, ledgerProblems(family, appended, current), "is not in Prior")
	family = Families()[0]
	current = mustCurrentDigests(t, family)
	family.Prior[textDigest("never shipped\n")] = family.WorkflowFile
	assertLedgerProblem(t, ledgerProblems(family, entries, current), "records no such earlier text")
	stray := append(slices.Clone(entries), shippedText{digest: textDigest("x"), rel: "README.md"})
	assertLedgerProblem(t, ledgerProblems(Families()[0], stray, current), "is not a managed path")
	for _, malformed := range []string{"abc  " + family.WorkflowFile, textDigest("x"), textDigest("x") + "  a  b"} {
		if _, err := parseLedger(malformed); err == nil {
			t.Fatalf("malformed ledger line %q parsed", malformed)
		}
	}
}

// Boundary: CRLF, comments and blank lines parse like LF lines; recording the outgoing text in
// Prior after the append satisfies the ledger; a text that returns to an earlier one is listed
// once; and a ledger past its line bound is refused.
func TestShippedTextLedgerBoundary(t *testing.T) {
	family := Families()[0]
	text, entries, current := readLedger(t, family)
	crlf, err := parseLedger("# comment\r\n\r\n" + strings.ReplaceAll(text, "\n", "\r\n"))
	if err != nil || !slices.Equal(crlf, entries) {
		t.Fatalf("CRLF ledger parsed to %d entries (%v), want %d", len(crlf), err, len(entries))
	}
	outgoing := current[family.WorkflowFile]
	family.Workflow += "# a pin moved\n"
	moved := mustCurrentDigests(t, family)
	appended := mustParseLedger(t, appendChangedTexts(family, text, entries, moved))
	family.Prior[outgoing] = family.WorkflowFile
	if problems := ledgerProblems(family, appended, moved); len(problems) != 0 {
		t.Fatalf("a recorded outgoing text still fails the ledger: %v", problems)
	}
	back := append(slices.Clone(appended), shippedText{digest: outgoing, rel: family.WorkflowFile})
	assertLedgerProblem(t, ledgerProblems(family, back, current), "is listed twice")
	if _, err := parseLedger(strings.Repeat("\n", maxLedgerLines)); err == nil {
		t.Fatal("a ledger past its line bound parsed")
	}
	if _, err := parseLedger(strings.Repeat("\n", maxLedgerLines-1)); err != nil {
		t.Fatalf("a ledger at its line bound was refused: %v", err)
	}
}

func assertLedgerProblem(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return
		}
	}
	t.Fatalf("no ledger problem contains %q: %v", want, problems)
}
