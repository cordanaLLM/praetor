// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

// Positive: every artifact adoption later compares byte for byte must be named, or a formatter
// is free to rewrite the one that was left out (#116).
func TestManagedArtifactsNameEveryComparedFile(t *testing.T) {
	got := make(map[string]bool)
	for _, path := range managedArtifacts(true) {
		got[path] = true
	}
	for _, required := range []string{
		manifestFile, lockFile, baselineFile, agentsFile, devcontainerFile,
		lefthookFile, evasionHookFile, rulesetFile, labelsFile, paperclipFile,
		auditorAgentFile, gatekeeperFile, DocumentationWorkflowFile,
	} {
		if !got[required] {
			t.Errorf("managed artifact %q is written and compared but never declared to a formatter", required)
		}
	}
	for _, name := range []string{
		"package.json", "package-lock.json", "markdownlint-cli2.yaml", "verify.mjs", "no-private-scratch-links.mjs",
	} {
		path := "tools/markdownlint/" + name
		if !got[path] {
			t.Errorf("documentation gate asset %q is not protected from formatter rewrites", path)
		}
	}
}

// Boundary: the set is deterministic, so the emitted block does not churn between runs.
func TestManagedArtifactsAreSortedAndUnique(t *testing.T) {
	paths := managedArtifacts(true)
	seen := make(map[string]bool, len(paths))
	for i, path := range paths {
		if seen[path] {
			t.Errorf("duplicate managed artifact %q", path)
		}
		seen[path] = true
		if i > 0 && paths[i-1] > path {
			t.Errorf("managed artifacts must be sorted: %q precedes %q", paths[i-1], path)
		}
	}
}

func TestManagedArtifactsDocumentationFacetConverges(t *testing.T) {
	enabled, err := mergeManagedIgnore("operator-output/\n", true)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := mergeManagedIgnore(enabled, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(disabled, "operator-output/") {
		t.Fatal("formatter reconciliation removed operator content")
	}
	for _, path := range DocumentationAssetPaths() {
		if strings.Contains(disabled, path) {
			t.Fatalf("disabled formatter inventory retained %s:\n%s", path, disabled)
		}
	}
	reenabled, err := mergeManagedIgnore(disabled, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range DocumentationAssetPaths() {
		if !strings.Contains(reenabled, path) {
			t.Fatalf("re-enabled formatter inventory lacks %s:\n%s", path, reenabled)
		}
	}
}

// Positive: an empty file gets the block and nothing else.
func TestMergeManagedIgnoreCreatesTheBlock(t *testing.T) {
	merged, err := mergeManagedIgnore("", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged, managedIgnoreBegin) || !strings.Contains(merged, managedIgnoreEnd) {
		t.Fatalf("the block must be delimited, got:\n%s", merged)
	}
	for _, path := range managedArtifacts(true) {
		if !strings.Contains(merged, path) {
			t.Errorf("merged content must name %q", path)
		}
	}
}

// Positive: the adopter's own entries survive, which is the whole reason for the markers.
func TestMergeManagedIgnorePreservesAdopterEntries(t *testing.T) {
	existing := "node_modules/\ndist/\n# my own note\ncoverage/\n"
	merged, err := mergeManagedIgnore(existing, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, own := range []string{"node_modules/", "dist/", "# my own note", "coverage/"} {
		if !strings.Contains(merged, own) {
			t.Errorf("adopter entry %q must be preserved", own)
		}
	}
}

func TestMergeManagedIgnorePreservesCRLF(t *testing.T) {
	merged, err := mergeManagedIgnore("node_modules/\r\ndist/\r\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(merged, "\n") != strings.Count(merged, "\r\n") {
		t.Fatalf("formatter ignore gained mixed line endings: %q", merged)
	}
	second, err := mergeManagedIgnore(merged, true)
	if err != nil {
		t.Fatal(err)
	}
	if second != merged {
		t.Fatal("CRLF formatter ignore did not converge")
	}
}

func TestMergeManagedIgnoreRejectsInconsistentLineEndings(t *testing.T) {
	for name, input := range map[string]string{
		"mixed":   "operator/\r\ncache/\n",
		"lone CR": "operator/\rcache/",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeManagedIgnore(input, true); err == nil {
				t.Fatal("inconsistent .prettierignore line endings accepted")
			}
		})
	}
}

// The property that matters: re-running must converge, not append. A second block would grow the
// file on every adoption and leave the adopter unable to tell which one is authoritative.
func TestMergeManagedIgnoreIsIdempotent(t *testing.T) {
	first, err := mergeManagedIgnore("node_modules/\n", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mergeManagedIgnore(first, true)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("a re-run must converge:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Count(second, managedIgnoreBegin) != 1 {
		t.Errorf("exactly one managed block must exist, found %d", strings.Count(second, managedIgnoreBegin))
	}
}

// Boundary: a stale block is replaced rather than left beside the new one, so a path removed
// from the managed set stops being ignored.
func TestMergeManagedIgnoreReplacesAStaleBlock(t *testing.T) {
	stale := "keep-me/\n" + managedIgnoreBegin + "\nold/removed/artifact.json\n" + managedIgnoreEnd + "\n"
	merged, err := mergeManagedIgnore(stale, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(merged, "old/removed/artifact.json") {
		t.Error("a path no longer managed must not survive in the block")
	}
	if !strings.Contains(merged, "keep-me/") {
		t.Error("adopter entries outside the block must survive")
	}
}

// Boundary: an adopter who appends entries *after* the managed block must keep them. If the
// closing marker were not recognised, everything past the block would be swallowed -- and that
// stays invisible as long as the block is the last thing in the file, which is why this case
// puts content on both sides of it.
func TestMergeManagedIgnorePreservesEntriesOnBothSidesOfTheBlock(t *testing.T) {
	existing := "before/\n" + managedIgnoreBegin + "\n.standards.lock\n" + managedIgnoreEnd + "\nafter/\nalso-after/\n"
	merged, err := mergeManagedIgnore(existing, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, own := range []string{"before/", "after/", "also-after/"} {
		if !strings.Contains(merged, own) {
			t.Errorf("adopter entry %q must survive on either side of the managed block, got:\n%s", own, merged)
		}
	}
	if strings.Count(merged, managedIgnoreBegin) != 1 {
		t.Errorf("exactly one managed block must remain, got:\n%s", merged)
	}
}

// Negative: a block left unterminated by a truncation or a hand edit is ambiguous. Rewriting it
// would silently discard everything after the opening marker, including possible operator
// content, so adoption must refuse the file unchanged.
func TestMergeManagedIgnoreRejectsAnUnterminatedBlock(t *testing.T) {
	broken := "mine/\n" + managedIgnoreBegin + "\n.standards.lock\n"
	if _, err := mergeManagedIgnore(broken, true); err == nil {
		t.Fatal("an unterminated managed block must be rejected")
	}
}

func TestMergeManagedIgnoreRejectsAmbiguousMarkers(t *testing.T) {
	tests := map[string]string{
		"unmatched end": managedIgnoreEnd + "\noperator/\n",
		"nested begin":  managedIgnoreBegin + "\n" + managedIgnoreBegin + "\n" + managedIgnoreEnd + "\n",
		"duplicate blocks": managedIgnoreBegin + "\n" + managedIgnoreEnd + "\noperator/\n" +
			managedIgnoreBegin + "\n" + managedIgnoreEnd + "\n",
		"indented begin": " " + managedIgnoreBegin + "\n" + managedIgnoreEnd + "\n",
		"indented end":   managedIgnoreBegin + "\n " + managedIgnoreEnd + "\n",
	}
	for name, existing := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeManagedIgnore(existing, true); err == nil {
				t.Fatalf("ambiguous formatter markers must be rejected:\n%s", existing)
			}
		})
	}
}

// Negative: an unbounded ignore file is refused rather than read entirely (HISS-02).
func TestMergeManagedIgnoreRefusesAnOversizedFile(t *testing.T) {
	flood := strings.Repeat("entry/\n", maxIgnoreLines+10)
	if _, err := mergeManagedIgnore(flood, true); err == nil {
		t.Error("an ignore file beyond the bound must be refused")
	}
}

// historicalFormatterBlock reproduces, byte for byte, the block adoption wrote before the
// documentation facet joined the inventory: the same markers and paths under the old comment.
func historicalFormatterBlock() string {
	return managedIgnoreBegin + "\n" +
		"# praetorctl audit compares these byte for byte. A formatter that rewrites\n" +
		"# them fails the gate with an error that reads like a hand edit.\n" +
		strings.Join(managedArtifacts(false), "\n") + "\n" + managedIgnoreEnd + "\n"
}

// Positive: an earlier adopter's .prettierignore differs from the current rendering only in the
// explanatory comment, so audit accepts it as current instead of failing every such repository.
func TestVerifyManagedFormatterIgnoreAcceptsHistoricalHeader(t *testing.T) {
	for name, existing := range map[string]string{
		"block only":          historicalFormatterBlock(),
		"operator rules kept": "node_modules/\ndist/\n\n" + historicalFormatterBlock(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyManagedFormatterIgnore(existing, false); err != nil {
				t.Fatalf("historical formatter inventory rejected as stale: %v", err)
			}
			merged, err := mergeManagedIgnore(existing, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(merged, "compares these byte for byte") || VerifyManagedFormatterIgnore(merged, false) != nil {
				t.Fatalf("adoption did not converge the historical header to the current one:\n%s", merged)
			}
		})
	}
}

// Negative: the historical comment excuses nothing else. A missing path, and the documentation
// facet whose paths the historical inventory never named, stay stale.
func TestVerifyManagedFormatterIgnoreHistoricalHeaderKeepsInventoryStrict(t *testing.T) {
	if err := VerifyManagedFormatterIgnore(historicalFormatterBlock(), true); err == nil {
		t.Fatal("historical inventory without documentation paths passed an enabled facet")
	}
	missing := strings.Replace(historicalFormatterBlock(), manifestFile+"\n", "", 1)
	if err := VerifyManagedFormatterIgnore(missing, false); err == nil {
		t.Fatal("historical header excused a missing managed path")
	}
}

// Boundary: only the exact historical comment is recognised; an edited or partial one is stale.
func TestVerifyManagedFormatterIgnoreHistoricalHeaderBoundary(t *testing.T) {
	for name, existing := range map[string]string{
		"edited word":   strings.Replace(historicalFormatterBlock(), "byte for byte", "byte-for-byte", 1),
		"dropped line":  strings.Replace(historicalFormatterBlock(), "# them fails the gate with an error that reads like a hand edit.\n", "", 1),
		"trailing text": strings.Replace(historicalFormatterBlock(), "hand edit.\n", "hand edit. \n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyManagedFormatterIgnore(existing, false); err == nil {
				t.Fatalf("non-historical header accepted:\n%s", existing)
			}
		})
	}
}
