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
	for _, path := range managedArtifacts() {
		got[path] = true
	}
	for _, required := range []string{
		manifestFile, lockFile, baselineFile, agentsFile, devcontainerFile,
		lefthookFile, evasionHookFile, rulesetFile, labelsFile, paperclipFile,
		auditorAgentFile, gatekeeperFile,
	} {
		if !got[required] {
			t.Errorf("managed artifact %q is written and compared but never declared to a formatter", required)
		}
	}
}

// Boundary: the set is deterministic, so the emitted block does not churn between runs.
func TestManagedArtifactsAreSortedAndUnique(t *testing.T) {
	paths := managedArtifacts()
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

// Positive: an empty file gets the block and nothing else.
func TestMergeManagedIgnoreCreatesTheBlock(t *testing.T) {
	merged, err := mergeManagedIgnore("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged, managedIgnoreBegin) || !strings.Contains(merged, managedIgnoreEnd) {
		t.Fatalf("the block must be delimited, got:\n%s", merged)
	}
	for _, path := range managedArtifacts() {
		if !strings.Contains(merged, path) {
			t.Errorf("merged content must name %q", path)
		}
	}
}

// Positive: the adopter's own entries survive, which is the whole reason for the markers.
func TestMergeManagedIgnorePreservesAdopterEntries(t *testing.T) {
	existing := "node_modules/\ndist/\n# my own note\ncoverage/\n"
	merged, err := mergeManagedIgnore(existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, own := range []string{"node_modules/", "dist/", "# my own note", "coverage/"} {
		if !strings.Contains(merged, own) {
			t.Errorf("adopter entry %q must be preserved", own)
		}
	}
}

// The property that matters: re-running must converge, not append. A second block would grow the
// file on every adoption and leave the adopter unable to tell which one is authoritative.
func TestMergeManagedIgnoreIsIdempotent(t *testing.T) {
	first, err := mergeManagedIgnore("node_modules/\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := mergeManagedIgnore(first)
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
	merged, err := mergeManagedIgnore(stale)
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
	merged, err := mergeManagedIgnore(existing)
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

// Negative: a block left unterminated by a truncation or a hand edit must not leave stray
// artifact paths the adopter never wrote and cannot attribute.
func TestMergeManagedIgnoreRewritesAnUnterminatedBlock(t *testing.T) {
	broken := "mine/\n" + managedIgnoreBegin + "\n.standards.lock\n"
	merged, err := mergeManagedIgnore(broken)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(merged, managedIgnoreBegin) != 1 || !strings.Contains(merged, managedIgnoreEnd) {
		t.Errorf("an unterminated block must be rewritten whole, got:\n%s", merged)
	}
	// The adopter's own entry preceded the broken marker and is theirs, not praetor's. Losing it
	// to repair praetor's block would be destroying their data to fix our own file.
	if !strings.Contains(merged, "mine/") {
		t.Errorf("an adopter entry before an unterminated marker must survive, got:\n%s", merged)
	}
	// Nothing from inside the broken block may leak back out unattributed.
	if strings.Count(merged, ".standards.lock") != 1 {
		t.Errorf("the stale in-block content must not survive alongside the rewritten block, got:\n%s", merged)
	}
}

// Negative: an unbounded ignore file is refused rather than read entirely (HISS-02).
func TestMergeManagedIgnoreRefusesAnOversizedFile(t *testing.T) {
	flood := strings.Repeat("entry/\n", maxIgnoreLines+10)
	if _, err := mergeManagedIgnore(flood); err == nil {
		t.Error("an ignore file beyond the bound must be refused")
	}
}
