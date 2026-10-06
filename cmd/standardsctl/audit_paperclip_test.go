// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/paperclip"
)

// adoptedHarnessFixture is the audit fixture after a first adoption wrote its Paperclip harness
// and bound register.sources to it: the synthesis for the fixture's facts.
func adoptedHarnessFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newForceAdoptFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml",
		strings.TrimSuffix(fixtureManifest("acme", "widgets", false), fixtureRegisterSources())+declineDevContainer)
	removeFixturePath(t, f, ".paperclip")
	if err := adoptFixture(t, f, false); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return f
}

// TestAuditPaperclip_Positive_AdoptedSynthesisVerified (#321): the harness adoption wrote is the
// synthesis the audit renders from the same facts, so the gate says so; `praetorctl paperclip
// harness`, the remedy a drift names, writes the same bytes and the audit stays green.
func TestAuditPaperclip_Positive_AdoptedSynthesisVerified(t *testing.T) {
	f := adoptedHarnessFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit after adopt: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Paperclip agent runtime harness verified (acme/widgets, 6 rules; "+
		"synthesis for repository facts; rules.md renders it, caveman lint passed).")
	harness := readFixtureFile(t, f.dir, ".paperclip/harness.json")
	if err := runPaperclipHarness(t.Context(), []string{"--path=" + f.dir}); err != nil {
		t.Fatal(err)
	}
	if got := readFixtureFile(t, f.dir, ".paperclip/harness.json"); got != harness {
		t.Fatalf("paperclip harness rewrote adoption's synthesis:\n%s\nwant\n%s", got, harness)
	}
}

// TestAuditPaperclip_Negative_HandEditedRulesFails (#321): a hand-edited rules.md fails the gate
// naming the file, the line it lacks and the remedy, which restores a green audit. A rules.md
// rendering an operator-owned harness.json whose rows fail the caveman lint fails it too.
func TestAuditPaperclip_Negative_HandEditedRulesFails(t *testing.T) {
	f := adoptedHarnessFixture(t)
	rules := readFixtureFile(t, f.dir, ".paperclip/rules.md")
	edited := strings.Replace(rules, "Rule 0 Terminal Disposition", "Rule 0 terminal disposition, usually", 1)
	if edited == rules {
		t.Fatalf("fixture precondition: rules.md lacks the disposition row:\n%s", rules)
	}
	writeFixtureFile(t, f.dir, ".paperclip/rules.md", edited)
	_, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Paperclip rules out of sync: paperclip rules.md is not the rendering of harness.json: .paperclip/rules.md")
	mustErrContain(t, err, `first expected line it lacks: "- Rule 0 Terminal Disposition`)
	mustErrContain(t, err, "run 'praetorctl paperclip harness'")
	if err := runPaperclipHarness(t.Context(), []string{"--path=" + f.dir}); err != nil {
		t.Fatal(err)
	}
	if out, err := f.audit(t); err != nil {
		t.Fatalf("audit after the remedy: %v\n%s", err, out)
	}

	prose := newAuditFixture(t)
	h, err := paperclip.LoadHarness(filepath.Join(prose.dir, ".paperclip", "harness.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Above the lint's 40-word floor, at well over two articles per 100 words.
	h.OperatingContract = []string{
		"The agent should always make sure that the build is green before it opens a pull request for the change.",
		"The agent should then wait for a review of the change by a maintainer of the repository before the merge.",
	}
	if err := paperclip.WriteHarness(h, prose.dir); err != nil {
		t.Fatal(err)
	}
	_, err = prose.audit(t)
	mustErrContain(t, err, "[FAIL] Paperclip rules caveman lint: agent text fails the caveman lint: 1 finding(s): line 0 C1 article-density")
	mustErrContain(t, err, "praetorctl caveman check --kind=context .paperclip/rules.md")
}

// TestAuditPaperclip_Boundary_OwnedAbsentRulesAndUndeclaredForge (#321): an operator-owned
// harness.json without rules.md passes and says what it is; earlier output names the adopt
// remedy; a repository on a forge host other than github.com that declares no repository.forge
// fails the gate naming the key, since its harness has no push rows to compare.
func TestAuditPaperclip_Boundary_OwnedAbsentRulesAndUndeclaredForge(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Paperclip agent runtime harness verified (acme/widgets, 1 rules; "+
		"operator-owned harness.json, not this release's synthesis; adopt keeps it; rules.md absent).")

	stale := paperclipComparisonFailure(fmt.Errorf("%w: .paperclip/harness.json", paperclip.ErrHarnessStale))
	if !errors.Is(stale, paperclip.ErrHarnessStale) || !strings.Contains(stale.Error(), "run 'praetorctl adopt', which refreshes unmodified earlier output") {
		t.Fatalf("stale harness failure = %v", stale)
	}

	undeclared := newAuditFixture(t)
	writeFixtureFile(t, undeclared.dir, ".standards.yaml",
		strings.Replace(fixtureManifest("acme", "widgets", false), "  forge: \"github\"\n", "", 1))
	if out, err := runFixtureGit(t, undeclared.dir, undeclared.gitEnv, "remote", "add", "origin", "https://codeberg.org/acme/widgets.git"); err != nil {
		t.Fatalf("add origin: %v (%s)", err, out)
	}
	_, err = undeclared.audit(t)
	mustErrContain(t, err, "[FAIL] Paperclip harness synthesis: paperclip: harness push protocol: repository.forge required: "+
		"the origin remote names host codeberg.org")
	if _, statErr := os.Stat(filepath.Join(undeclared.dir, ".paperclip", "rules.md")); !os.IsNotExist(statErr) {
		t.Fatalf("the refused audit wrote rules.md: %v", statErr)
	}
}
