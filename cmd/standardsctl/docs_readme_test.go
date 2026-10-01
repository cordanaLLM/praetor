// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

// staleReadme carries the governance block with a stale claim between hand-written text.
const staleReadme = "# Widgets\n\nHand-written intro.\n\n" + readmegovernance.Start + "\nstale claim\n" + readmegovernance.End + "\n\nHand-written outro.\n"

// readmeFixture writes a manifest and README into a fresh directory.
func readmeFixture(t *testing.T, manifest, readme string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", manifest)
	if readme != "" {
		writeFixtureFile(t, dir, "README.md", readme)
	}
	return dir
}

// docsReadme runs docs readme and returns its output and error.
func docsReadme(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("docs", append([]string{"readme"}, args...)) })
}

// Positive (#696): docs readme renders a stale block from the manifest and the baseline, keeps
// the hand-written text around it, and renders the block audit verifies, so --check then passes.
func TestDocsReadme_Positive_RendersTheAuditedBlock(t *testing.T) {
	dir := readmeFixture(t, "version: 1\nrepository:\n  owner: \"acme\"\n  name: \"widgets\"\n", staleReadme)
	out, err := docsReadme(t, dir)
	if err != nil {
		t.Fatalf("docs readme: %v\n%s", err, out)
	}
	mustContain(t, out, "Rendered the README.md governance block.")
	rendered := readFixtureFile(t, dir, "README.md")
	if strings.Contains(rendered, "stale claim") || !strings.Contains(rendered, "Hand-written intro.") || !strings.Contains(rendered, "Hand-written outro.") {
		t.Fatalf("rendered README:\n%s", rendered)
	}
	manifest, err := config.LoadManifest(filepath.Join(dir, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := baseline.LoadBaseline(filepath.Join(dir, docsReadmeBaseline))
	if err != nil {
		t.Fatal(err)
	}
	state, err := readmeGovernanceState(manifest, base, !base.Absent, rendered)
	if err != nil || readmegovernance.Verify(rendered, state) != nil {
		t.Fatalf("the rendered block must pass the audit's verification: %v, %v", err, readmegovernance.Verify(rendered, state))
	}
	if out, err = docsReadme(t, dir, "--check"); err != nil {
		t.Fatalf("--check after rendering: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] README.md governance block is current.")
}

// Negative (#696): --check fails on a stale block without writing, a missing manifest is an
// error, and a manifest the loader refuses is an error.
func TestDocsReadme_Negative_StaleMissingAndInvalid(t *testing.T) {
	dir := readmeFixture(t, "version: 1\n", staleReadme)
	if out, err := docsReadme(t, dir, "--check"); err == nil || !strings.Contains(err.Error(), "governance block is stale; run 'praetorctl docs readme'") {
		t.Fatalf("--check on a stale block: %v\n%s", err, out)
	}
	if got := readFixtureFile(t, dir, "README.md"); got != staleReadme {
		t.Fatalf("--check wrote the README:\n%s", got)
	}
	if _, err := docsReadme(t, t.TempDir()); err == nil || !strings.Contains(err.Error(), "docs readme renders from the manifest") {
		t.Fatalf("a missing manifest: %v", err)
	}
	invalid := readmeFixture(t, "version: 1\nunknown_key: true\n", staleReadme)
	if _, err := docsReadme(t, invalid); err == nil {
		t.Fatal("a manifest the loader refuses must be an error")
	}
}

// Boundary (#696): a README without the block, an absent README and a declined block are left
// alone; unbalanced markers are reported, not skipped; two paths are refused.
func TestDocsReadme_Boundary_NotApplicableDeclinedAndMarkers(t *testing.T) {
	plain := readmeFixture(t, "version: 1\n", "# No block\n")
	if out, err := docsReadme(t, plain); err != nil || !strings.Contains(out, "not applicable") || readFixtureFile(t, plain, "README.md") != "# No block\n" {
		t.Fatalf("a README without the block: %v\n%s", err, out)
	}
	if out, err := docsReadme(t, readmeFixture(t, "version: 1\n", "")); err != nil || !strings.Contains(out, "not applicable") {
		t.Fatalf("an absent README: %v\n%s", err, out)
	}
	declined := readmeFixture(t, "version: 1\nadoption:\n  decline:\n    - \"readme\"\n", staleReadme)
	if out, err := docsReadme(t, declined); err != nil || !strings.Contains(out, "declined by adoption.decline") || readFixtureFile(t, declined, "README.md") != staleReadme {
		t.Fatalf("a declined block: %v\n%s", err, out)
	}
	unbalanced := readmeFixture(t, "version: 1\n", "# Widgets\n"+readmegovernance.Start+"\n")
	if _, err := docsReadme(t, unbalanced); err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("unbalanced markers: %v", err)
	}
	if _, err := docsReadme(t, plain, plain); err == nil || !strings.Contains(err.Error(), "at most one repository path") {
		t.Fatalf("two paths: %v", err)
	}
}
