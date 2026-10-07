// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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

// withVerification rewrites the audit fixture's manifest to declare the verification section.
func withVerification(t *testing.T, f *auditFixture, section string) {
	t.Helper()
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+"verification:\n"+section)
}

// TestAuditPaperclip_VerificationBoundFromManifest (#321): the hooks and CI jobs adoption writes
// run the audit with no --verification-max-* flag, so the Paperclip gate's facts walk reads the
// bound the manifest's verification section declares. Negative: a declared bound the checkout
// exceeds fails the gate naming the key, the remedy those runs can take. Positive: a raised
// declaration passes with no flag, and a flag still raises a declared bound for its own run.
// Boundary: a declaration past the ceiling fails the manifest audit naming the key and range.
func TestAuditPaperclip_VerificationBoundFromManifest(t *testing.T) {
	f := newAuditFixture(t)
	withVerification(t, f, "  max_entries: 1\n")
	_, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Paperclip harness facts: detect repository languages: verification discovery exceeds 1 entries")
	mustErrContain(t, err, "or declare verification.max_entries in .standards.yaml so every run, the audit's hooks and CI included, reads it")
	if out, err := f.audit(t, "--verification-max-entries=4096"); err != nil {
		t.Fatalf("a flag raising the declared bound is ignored: %v\n%s", err, out)
	}
	withVerification(t, f, "  max_entries: 4096\n")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit under the declared bound: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Paperclip agent runtime harness verified (acme/widgets")
	withVerification(t, f, "  max_entries: 200001\n")
	_, err = f.audit(t)
	mustErrContain(t, err, "verification.max_entries must be an integer from 1 to 200000; got 200001")
}

// withHarnessDirective rewrites the register directive of the fixture's harness.json, from to
// to, as `praetorctl paperclip harness` writes a harness: rules.md renders the result.
func withHarnessDirective(t *testing.T, f *auditFixture, from, to string) *paperclip.Harness {
	t.Helper()
	h, err := paperclip.LoadHarness(filepath.Join(f.dir, ".paperclip", "harness.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := slices.Index(h.OperatingContract, from)
	if index < 0 {
		t.Fatalf("fixture precondition: harness contract lacks %q: %v", from, h.OperatingContract)
	}
	h.OperatingContract[index] = to
	if err := paperclip.WriteHarness(h, f.dir); err != nil {
		t.Fatal(err)
	}
	return h
}

// TestAuditPaperclip_Migration_HarnessNamingAbsentCaveman (#235, #321): every release before
// #235 wrote the register directive naming the `caveman` skill, whether or not the repository
// carries it, and bound register.sources to that harness. Since the audit compares the harness
// with this release's synthesis, which names only a carried skill, such a harness in a repository
// without .agents/skills/caveman is unmodified earlier output. Negative: the audit fails it with
// the adopt remedy, though every gate before it, the register block's included, passes.
// Positive: the remedy, praetorctl adopt, refreshes it to the directive without the skill name
// and re-binds register.sources, and the full audit passes.
func TestAuditPaperclip_Migration_HarnessNamingAbsentCaveman(t *testing.T) {
	f := adoptedHarnessFixture(t)
	if _, err := os.Stat(filepath.Join(f.dir, ".agents", "skills", "caveman")); !os.IsNotExist(err) {
		t.Fatalf("fixture precondition: .agents/skills/caveman exists or is unreadable: %v", err)
	}
	plain := config.RegisterDirectiveWithout(config.TextRegisterInternal, config.RegisterSkills())
	withHarnessDirective(t, f, plain, config.RegisterDirective(config.TextRegisterInternal))
	earlier := readFixtureFile(t, f.dir, ".paperclip/harness.json")
	writeFixtureFile(t, f.dir, ".standards.yaml", strings.TrimSuffix(fixtureManifest("acme", "widgets", false),
		fixtureRegisterSources())+declineDevContainer+fixtureRegisterSourcesOver([]byte(earlier)))

	_, err := f.audit(t)
	if !errors.Is(err, paperclip.ErrHarnessStale) {
		t.Fatalf("audit of a harness naming an absent caveman skill = %v, want ErrHarnessStale", err)
	}
	mustErrContain(t, err, "[FAIL] Paperclip harness out of date: ")
	mustErrContain(t, err, "run 'praetorctl adopt', which refreshes unmodified earlier output without --force")

	if err := adoptFixture(t, f, false); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit after adopt: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Paperclip agent runtime harness verified (acme/widgets, 6 rules; synthesis for repository facts;")
	if harness := readFixtureFile(t, f.dir, ".paperclip/harness.json"); !strings.Contains(harness, plain) {
		t.Fatalf("adopt left the harness naming the absent skill:\n%s", harness)
	}
}

// TestAuditPaperclip_RegisterSkillSubstitution (#235): the audit compares the harness with the
// synthesis the repository's register skills give. Positive: a readable caveman skill gives the
// directive naming it, which a harness naming it passes with no warning. Boundary: a directory
// at its SKILL.md, which the confined read refuses, gives the directive without a skill name;
// the harness stating it passes, and the audit prints that substitution as a [WARN] line naming
// the refused skill, never silently.
func TestAuditPaperclip_RegisterSkillSubstitution(t *testing.T) {
	plain := config.RegisterDirectiveWithout(config.TextRegisterInternal, config.RegisterSkills())
	named := config.RegisterDirective(config.TextRegisterInternal)
	const warn = "[WARN] Paperclip harness synthesis: the register directive names no skill, since a register skill could not be read: "
	for _, readable := range []bool{true, false} {
		f := adoptedHarnessFixture(t)
		skill := filepath.Join(f.dir, ".agents", "skills", "caveman", "SKILL.md")
		want := plain
		if readable {
			writeFixtureFile(t, f.dir, ".agents/skills/caveman/SKILL.md", "---\nname: caveman\ndescription: fixture\n---\n")
			want = named
		} else if err := os.MkdirAll(skill, 0o700); err != nil {
			t.Fatal(err)
		}
		loaded := withHarnessDirective(t, f, plain, want)
		var verdict string
		out, err := captureStdout(t, func() error {
			var auditErr error
			verdict, auditErr = auditPaperclipSynthesis(t.Context(), f.dir, nil, loaded)
			return auditErr
		})
		if err != nil || !strings.HasPrefix(verdict, "synthesis for repository facts") {
			t.Fatalf("readable=%v: verdict %q, err %v\n%s", readable, verdict, err, out)
		}
		warned := strings.Count(out, warn)
		if readable && warned != 0 || !readable && (warned != 1 || !strings.Contains(out, "read canonical skill caveman")) {
			t.Fatalf("readable=%v: output\n%s\nwant one [WARN] naming the refused caveman skill only when it is unreadable", readable, out)
		}
	}
}
