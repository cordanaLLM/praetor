// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Adoption ships the skills the text register block names, and adhd-format, which social-text
// inherits from (#235).

// priorSkillFixtures holds, per skill of the bundle, every text a Praetor release shipped at its
// canonical path, in a directory named after the skill.
const priorSkillFixtures = "testdata/skills"

// priorSkillLicenseFixtures holds, per skill of the bundle, every licence text a Praetor release shipped at its
// canonical licence path, in a directory named after the skill.
const priorSkillLicenseFixtures = "testdata/skill-licenses"

// claudeSkillRel is the copy of skill name in the skill directory Claude Code reads.
func claudeSkillRel(name string) string {
	return compiler.SkillEntryRel(".claude/skills", name)
}

// sourceSkillText is the SKILL.md of skill name this repository ships.
func sourceSkillText(t *testing.T, name string) string {
	t.Helper()
	return mustRead(t, filepath.Join(sourceCheckout, filepath.FromSlash(compiler.CanonicalSkillRel(name))))
}

// sourceSkillLicenseText is the LICENSE of skill name this repository ships.
func sourceSkillLicenseText(t *testing.T, name string) string {
	t.Helper()
	return mustRead(t, filepath.Join(sourceCheckout, filepath.FromSlash(compiler.CanonicalSkillLicenseRel(name))))
}

// repoText reads the slash path rel below repoPath.
func repoText(t *testing.T, repoPath, rel string) string {
	t.Helper()
	return mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel)))
}

// assertAbsent fails when rel exists below repoPath.
func assertAbsent(t *testing.T, repoPath, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists (lstat err=%v)", rel, err)
	}
}

// verifyAdoptedContext runs compile-context --verify over repoPath, which adoption skips when an
// agent step is declined (verifyAgentContext).
func verifyAdoptedContext(t *testing.T, repoPath string) {
	t.Helper()
	source := filepath.Join(repoPath, agentsFile)
	if err := compiler.VerifyCompiledContext(t.Context(), io.Discard, compiler.NewTranspiler(), source, repoPath); err != nil {
		t.Fatalf("compile-context --verify after adoption: %v", err)
	}
}

// assertHarnessDirective fails unless the Paperclip harness adoption wrote carries the internal
// register directive naming the caveman skill (named) or stating the form alone.
func assertHarnessDirective(t *testing.T, repoPath string, named bool) {
	t.Helper()
	harness := repoText(t, repoPath, paperclipFile)
	want := config.RegisterDirectiveWithout(config.TextRegisterInternal, config.RegisterSkills())
	if named {
		want = config.RegisterDirective(config.TextRegisterInternal)
	}
	if !strings.Contains(harness, want) || named != strings.Contains(harness, "`caveman`") {
		t.Errorf("%s does not carry %q alone:\n%s", paperclipFile, want, harness)
	}
}

// warningNaming returns the first warning of rep that names text, or "".
func warningNaming(rep *AdoptReport, text string) string {
	for _, warning := range rep.Warnings {
		if strings.Contains(warning, text) {
			return warning
		}
	}
	return ""
}

// Each skill's digest set is replayable in both directions against its fixtures.
func TestPriorSkillDigests_Positive_ReproducedByFixtures(t *testing.T) {
	for _, name := range config.RegisterSkillBundle() {
		assertPriorDigestsReproduced(t, filepath.Join(priorSkillFixtures, name), priorSkillDigests[compiler.CanonicalSkillRel(name)])
	}
}

// Boundary: the skills this repository ships are recorded, so the release that changes one still
// refreshes the unedited copy an adopter holds. After changing a skill, copy it under
// testdata/skills/<name> and add its digest to priorSkillDigests.
func TestPriorSkillDigests_Boundary_CurrentSourcesRecorded(t *testing.T) {
	bundle := config.RegisterSkillBundle()
	if len(priorSkillDigests) != len(bundle) {
		t.Fatalf("priorSkillDigests covers %d paths, want the %d skills of the bundle", len(priorSkillDigests), len(bundle))
	}
	for _, name := range bundle {
		data := []byte(sourceSkillText(t, name))
		if !isPriorRendering(data, priorSkillDigests[compiler.CanonicalSkillRel(name)]) {
			t.Errorf("the shipped %s (%s) is not in priorSkillDigests", name, fixtureDigest(t, name, data))
		}
	}
}

// Each skill licence's digest set is replayable in both directions against its fixtures.
func TestPriorSkillLicenseDigests_Positive_ReproducedByFixtures(t *testing.T) {
	for _, name := range config.RegisterSkillBundle() {
		assertPriorDigestsReproduced(t, filepath.Join(priorSkillLicenseFixtures, name), priorSkillLicenseDigests[compiler.CanonicalSkillLicenseRel(name)])
	}
}

// Boundary: the skill licences this repository ships are recorded, so the release that changes one still
// refreshes the unedited copy an adopter holds. After changing a licence, copy it under
// testdata/skill-licenses/<name> and add its digest to priorSkillLicenseDigests.
func TestPriorSkillLicenseDigests_Boundary_CurrentSourcesRecorded(t *testing.T) {
	bundle := config.RegisterSkillBundle()
	if len(priorSkillLicenseDigests) != len(bundle) {
		t.Fatalf("priorSkillLicenseDigests covers %d paths, want the %d skills of the bundle", len(priorSkillLicenseDigests), len(bundle))
	}
	for _, name := range bundle {
		data := []byte(sourceSkillLicenseText(t, name))
		if !isPriorRendering(data, priorSkillLicenseDigests[compiler.CanonicalSkillLicenseRel(name)]) {
			t.Errorf("the shipped licence for %s (%s) is not in priorSkillLicenseDigests", name, fixtureDigest(t, name, data))
		}
	}
}

// assertLicenseInstalled checks that a skill whose upstream licence travels with copies (text is
// its SKILL.md) is installed with the shipped LICENSE beside it, in .agents/skills and in the
// .claude/skills copy, and that both are reported created.
func assertLicenseInstalled(t *testing.T, repoPath string, rep *AdoptReport, name, text string) {
	t.Helper()
	if !skillNeedsLicense(t, text) {
		return
	}
	shipped := sourceSkillLicenseText(t, name)
	for _, dir := range []string{compiler.CanonicalSkillsRel, ".claude/skills"} {
		rel := compiler.SkillLicenseRel(dir, name)
		if got := repoText(t, repoPath, rel); got != shipped {
			t.Errorf("%s differs from the shipped licence", rel)
		}
		if !contains(rep.CreatedFiles, rel) {
			t.Errorf("%s not reported created: %v", rel, rep.CreatedFiles)
		}
	}
}

// Positive: a fresh adoption installs every skill of the bundle with its licence header and
// upstream credit, copies each into .claude/skills, renders a block naming both register skills,
// and compile-context --verify accepts the result.
func TestAdopt_Positive_FreshAdoptionShipsTheRegisterSkills(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-fresh")
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	for _, name := range config.RegisterSkillBundle() {
		want := sourceSkillText(t, name)
		if got := repoText(t, repoPath, compiler.CanonicalSkillRel(name)); got != want {
			t.Errorf("%s differs from the shipped skill", compiler.CanonicalSkillRel(name))
		}
		if got := repoText(t, repoPath, claudeSkillRel(name)); got != want {
			t.Errorf("%s differs from the shipped skill", claudeSkillRel(name))
		}
		// The tag is split so REUSE lint does not read this literal as the file's licence.
		if !strings.Contains(want, "SPDX-License-"+"Identifier: EUPL-1.2") {
			t.Errorf("%s ships without its REUSE licence header", name)
		}
		if !contains(rep.CreatedFiles, compiler.CanonicalSkillRel(name)) || !contains(rep.CreatedFiles, claudeSkillRel(name)) {
			t.Errorf("%s and its client copy not reported created: %v", name, rep.CreatedFiles)
		}
		assertLicenseInstalled(t, repoPath, rep, name, want)
	}
	if !strings.Contains(repoText(t, repoPath, compiler.CanonicalSkillRel("caveman")), "derived_from: \"https://github.com/JuliusBrussee/caveman (MIT)\"") {
		t.Error("caveman ships without its upstream credit")
	}
	agents := repoText(t, repoPath, agentsFile)
	for _, named := range []string{"`social-text` skill:", "`caveman` skill:", "`caveman` brief shape"} {
		if !strings.Contains(agents, named) {
			t.Errorf("AGENTS.md does not name %q", named)
		}
	}
	assertHarnessDirective(t, repoPath, true)
	verifyAdoptedContext(t, repoPath)
}

// Negative: an edited skill is the repository's: a forced run keeps it and reports it, and its
// client copy follows the kept text. A hand-edited client copy is compiled output and is
// replaced from the canonical skill with a backup, as a persona copy is.
func TestAdopt_Negative_EditedSkillIsKeptAndReported(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-edited")
	edited := sourceSkillText(t, "caveman") + "\n## Local\n\n- keep repository terms\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(compiler.CanonicalSkillRel("caveman"))), edited)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(claudeSkillRel("social-text"))), "# hand copy\n")
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), true)
	if got := repoText(t, repoPath, compiler.CanonicalSkillRel("caveman")); got != edited {
		t.Fatal("--force replaced an edited caveman skill")
	}
	if warningNaming(rep, compiler.CanonicalSkillRel("caveman")) == "" {
		t.Errorf("the kept caveman skill is not reported: %v", rep.Warnings)
	}
	if got := repoText(t, repoPath, claudeSkillRel("caveman")); got != edited {
		t.Error("the client copy does not follow the kept caveman skill")
	}
	if got := repoText(t, repoPath, claudeSkillRel("social-text")); got != sourceSkillText(t, "social-text") {
		t.Error("the hand-edited client copy was not projected from the canonical skill")
	}
	if !hasAction(rep, claudeSkillRel("social-text"), actionReplace) {
		t.Errorf("the replaced client copy is not reported replaced: %+v", rep.ActionDetails)
	}
}

// Boundary: a declined agent-harness step installs no skill, and neither the register block nor
// the Paperclip harness then names one; a selection without Claude Code gets the canonical skills
// and no client copy, with .claude/skills reported not applicable; a dry run lists the skills and
// copies it would write and writes none.
func TestAdopt_Boundary_DeclinedSelectedAndPreviewed(t *testing.T) {
	t.Run("declined agent-harness", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-declined")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [agent-harness]\n")
		adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		for _, name := range config.RegisterSkillBundle() {
			assertAbsent(t, repoPath, compiler.CanonicalSkillRel(name))
			assertAbsent(t, repoPath, claudeSkillRel(name))
		}
		_, block, err := compiler.LoadRegisterBlock(t.Context(), repoPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(block, "`social-text`") || strings.Contains(block, "`caveman`") {
			t.Fatalf("the block names a skill the declined step never installed:\n%s", block)
		}
		assertHarnessDirective(t, repoPath, false)
	})
	t.Run("agent_clients without Claude Code", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-codex")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nagent_clients: [codex]\n")
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if got := repoText(t, repoPath, compiler.CanonicalSkillRel("caveman")); got != sourceSkillText(t, "caveman") {
			t.Error("caveman not installed for Codex, which reads .agents/skills")
		}
		assertAbsent(t, repoPath, ".claude/skills")
		if !hasAction(rep, ".claude/skills", actionSkip) {
			t.Errorf(".claude/skills not reported not applicable: %+v", rep.ActionDetails)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-preview")
		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range config.RegisterSkillBundle() {
			if !contains(rep.CreatedFiles, compiler.CanonicalSkillRel(name)) || !contains(rep.CreatedFiles, claudeSkillRel(name)) {
				t.Errorf("the preview does not list %s and its client copy: %v", name, rep.CreatedFiles)
			}
			assertAbsent(t, repoPath, compiler.CanonicalSkillRel(name))
		}
		assertAbsent(t, repoPath, ".claude")
	})
}

// Negative: agent-definitions alone writes the .claude/skills copies compile-context --verify
// requires of an installed skill, so with that step declined and Claude Code selected the
// agent-harness step installs no skill, warns why, and leaves a repository that verifies, with a
// block and a Paperclip harness naming no skill. Boundary: a selection without a client skill
// directory needs no copy, so the skills are installed and the repository still verifies.
func TestAdopt_DeclinedAgentDefinitionsLeavesAVerifiedRepository(t *testing.T) {
	t.Run("Claude Code selected", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-no-definitions")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [agent-definitions]\n")
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		for _, name := range config.RegisterSkillBundle() {
			assertAbsent(t, repoPath, compiler.CanonicalSkillRel(name))
		}
		assertAbsent(t, repoPath, ".claude/skills")
		if warning := warningNaming(rep, "agent-definitions is declined"); !strings.Contains(warning, ".claude/skills") {
			t.Errorf("no warning names the declined step and the copies it writes: %v", rep.Warnings)
		}
		if strings.Contains(repoText(t, repoPath, agentsFile), "`caveman`") {
			t.Error("AGENTS.md names the caveman skill the run did not install")
		}
		assertHarnessDirective(t, repoPath, false)
		verifyAdoptedContext(t, repoPath)
	})
	t.Run("no client skill directory", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-codex-no-definitions")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nagent_clients: [codex]\nadoption:\n  decline: [agent-definitions]\n")
		adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		for _, name := range config.RegisterSkillBundle() {
			if got := repoText(t, repoPath, compiler.CanonicalSkillRel(name)); got != sourceSkillText(t, name) {
				t.Errorf("%s not installed for a selection without a client skill directory", name)
			}
		}
		assertHarnessDirective(t, repoPath, true)
		verifyAdoptedContext(t, repoPath)
	})
}

// Negative: without a source bundle, or with one that lacks a skill of the bundle, nothing is
// installed and the warning names the register skills the block leaves out. Boundary: a
// repository that already carries both register skills is told nothing.
func TestInstallRegisterSkills_SourceBundle(t *testing.T) {
	session := func(t *testing.T, source string) *adoptSession {
		return &adoptSession{repoPath: t.TempDir(), opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}}
	}
	partial := t.TempDir()
	writeRegisterSkillSources(t, partial)
	if err := os.Remove(filepath.Join(partial, filepath.FromSlash(compiler.CanonicalSkillRel("adhd-format")))); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"no source bundle": "", "source without adhd-format": partial} {
		s := session(t, source)
		if err := s.installRegisterSkills(t.Context()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if warning := warningNaming(s.report, "register skills not installed"); !strings.Contains(warning, "social-text or caveman") {
			t.Errorf("%s: warning %q does not name the missing register skills", name, warning)
		}
		assertAbsent(t, s.repoPath, compiler.CanonicalSkillsRel)
	}
	carried := session(t, "")
	writeRegisterSkillSources(t, carried.repoPath)
	if err := carried.installRegisterSkills(t.Context()); err != nil || len(carried.report.Warnings) != 0 {
		t.Fatalf("a repository carrying the skills: err=%v warnings=%v", err, carried.report.Warnings)
	}
}

// skillNeedsLicense reports whether the skill text declares an upstream licence that requires its
// text to travel with copies (compiler.SkillRequiresLicense).
func skillNeedsLicense(t *testing.T, text string) bool {
	t.Helper()
	required, err := compiler.SkillRequiresLicense([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return required
}

// Positive: each shipped register skill names its credit (upstream author, upstream URL, licence
// terms) in one line in its text, and has the upstream licence its declared upstream requires
// next to it.
func TestShippedSkills_CreditAndLicense(t *testing.T) {
	creditPattern := regexp.MustCompile(`(?m)^Adapted from .*https://.*MIT (?:licence|terms at adaptation \(2026-09-18\))\.\r?$`)
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		text := sourceSkillText(t, name)
		if !creditPattern.MatchString(text) {
			t.Errorf("skill %s does not name its credit (author, URL, licence) in one line:\n%s", name, text)
		}
		if !skillNeedsLicense(t, text) {
			t.Errorf("skill %s declares no upstream licence that travels with copies", name)
			continue
		}
		license := sourceSkillLicenseText(t, name)
		if !strings.Contains(license, "Copyright (c) 2026 ") || !strings.Contains(license, "Permission is hereby granted") {
			t.Errorf("%s does not hold the copyright line and the permission notice:\n%s", compiler.CanonicalSkillLicenseRel(name), license)
		}
	}
}

// Negative: a source bundle that lacks the LICENSE of a derived skill installs nothing and the
// warning names the missing file, so no skill ships without the licence its upstream requires.
func TestInstallRegisterSkills_Negative_MissingLicenseRefused(t *testing.T) {
	partial := t.TempDir()
	writeRegisterSkillSources(t, partial)
	if err := os.Remove(filepath.Join(partial, filepath.FromSlash(compiler.CanonicalSkillLicenseRel("caveman")))); err != nil {
		t.Fatal(err)
	}
	session := &adoptSession{repoPath: t.TempDir(), opts: AdoptOptions{LockSourceRoot: partial}, report: &AdoptReport{}}
	if err := session.installRegisterSkills(t.Context()); err != nil {
		t.Fatalf("unexpected fatal err: %v", err)
	}
	if warning := warningNaming(session.report, compiler.CanonicalSkillLicenseRel("caveman")); warning == "" {
		t.Errorf("the warning does not name the missing licence: %v", session.report.Warnings)
	}
	assertAbsent(t, session.repoPath, compiler.CanonicalSkillsRel)
}

// Negative: a source bundle with a skill referencing docs/credits.md fails the path check
// and is refused without being installed (Rule 13 planted negative).
func TestInstallRegisterSkills_Negative_PlantedCreditsReferenceRefused(t *testing.T) {
	partial := t.TempDir()
	writeRegisterSkillSources(t, partial)
	cavemanPath := filepath.Join(partial, filepath.FromSlash(compiler.CanonicalSkillRel("caveman")))
	cavemanPlanted := sourceSkillText(t, "caveman") + "\nCredit: docs/credits.md\n"
	mustWrite(t, cavemanPath, cavemanPlanted)

	session := &adoptSession{repoPath: t.TempDir(), opts: AdoptOptions{LockSourceRoot: partial}, report: &AdoptReport{}}
	if err := session.installRegisterSkills(t.Context()); err != nil {
		t.Fatalf("unexpected fatal err: %v", err)
	}
	if warning := warningNaming(session.report, "docs/credits.md"); warning == "" {
		t.Errorf("warning does not name planted docs/credits.md: %v", session.report.Warnings)
	}
	assertAbsent(t, session.repoPath, compiler.CanonicalSkillsRel)
}

// Positive: unedited earlier skill texts held by an adopter refresh on a plain adoption run
// without --force.
func TestAdopt_Positive_PriorSkillRefreshedOnPlainRun(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-prior-refresh")
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		rel := compiler.CanonicalSkillRel(name)
		priorText := "---\nname: " + name + "\ndescription: earlier unedited fixture\n---\n\n# Prior text\n"
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), priorText)
		digest, _, err := util.CanonicalTextDigest([]byte(priorText))
		if err != nil {
			t.Fatal(err)
		}
		if priorSkillDigests[rel] == nil {
			priorSkillDigests[rel] = make(map[string]string)
		}
		priorSkillDigests[rel][digest] = "earlier unedited fixture"
		t.Cleanup(func() {
			delete(priorSkillDigests[rel], digest)
		})
		if licenseRel := compiler.CanonicalSkillLicenseRel(name); skillNeedsLicense(t, sourceSkillText(t, name)) {
			licensePath := filepath.Join(sourceCheckout, filepath.FromSlash(licenseRel))
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(licenseRel)), mustRead(t, licensePath))
		}
	}
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		rel := compiler.CanonicalSkillRel(name)
		if got := repoText(t, repoPath, rel); got != sourceSkillText(t, name) {
			t.Errorf("%s was not refreshed to current text", rel)
		}
		if got := findActionDetail(rep.ActionDetails, rel); !strings.Contains(got, "Refreshed the unedited earlier") {
			t.Errorf("%s action detail %q, want refresh", rel, got)
		}
	}
}

// Positive: an adopter repository holding the real ship-235 skill texts (#850 scenario)
// refreshes the 3 skills to current text without --force, installs and projects their LICENSE
// files into .claude/skills, and passes verifyAdoptedContext.
func TestAdopt_Positive_Ship235PriorSkillsRefreshedAndLicensesProjected(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-ship-235-refresh")
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		fixturePath := filepath.Join(priorSkillFixtures, name, "ship-235.SKILL.md.prior")
		ship235Text := mustRead(t, fixturePath)
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(compiler.CanonicalSkillRel(name))), ship235Text)
	}

	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)

	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		rel := compiler.CanonicalSkillRel(name)
		if got := repoText(t, repoPath, rel); got != sourceSkillText(t, name) {
			t.Errorf("%s was not refreshed to current text", rel)
		}
		if got := findActionDetail(rep.ActionDetails, rel); !strings.Contains(got, "Refreshed the unedited earlier") {
			t.Errorf("%s action detail %q, want refresh", rel, got)
		}
		if skillNeedsLicense(t, sourceSkillText(t, name)) {
			wantLicense := sourceSkillLicenseText(t, name)
			canonicalLicRel := compiler.CanonicalSkillLicenseRel(name)
			if got := repoText(t, repoPath, canonicalLicRel); got != wantLicense {
				t.Errorf("%s differs from shipped licence", canonicalLicRel)
			}
			claudeLicRel := compiler.SkillLicenseRel(".claude/skills", name)
			if got := repoText(t, repoPath, claudeLicRel); got != wantLicense {
				t.Errorf("%s differs from shipped licence", claudeLicRel)
			}
		}
	}
	verifyAdoptedContext(t, repoPath)
}

// Negative: a hand-edited skill in an adopter repository is preserved (refused overwrite)
// on a plain run without --force and reported with a warning.
func TestAdopt_Negative_HandEditedSkillRefusedOnPlainRun(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-hand-edited")
	edited := sourceSkillText(t, "caveman") + "\n# custom adopter instructions\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(compiler.CanonicalSkillRel("caveman"))), edited)
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	if got := repoText(t, repoPath, compiler.CanonicalSkillRel("caveman")); got != edited {
		t.Fatal("plain run replaced a hand-edited caveman skill")
	}
	if warningNaming(rep, compiler.CanonicalSkillRel("caveman")) == "" {
		t.Errorf("the kept hand-edited caveman skill is not reported in warnings: %v", rep.Warnings)
	}
	if contains(rep.CreatedFiles, compiler.CanonicalSkillRel("caveman")) || hasAction(rep, compiler.CanonicalSkillRel("caveman"), actionReplace) {
		t.Errorf("hand-edited skill reported created or replaced: created=%v actions=%+v", rep.CreatedFiles, rep.ActionDetails)
	}
}

// Positive: unedited earlier skill licence texts held by an adopter refresh on a plain adoption run
// without --force.
func TestAdopt_Positive_PriorLicenseRefreshedOnPlainRun(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-prior-license-refresh")
	bundle := config.RegisterSkillBundle()
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		rel := compiler.CanonicalSkillRel(name)
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), sourceSkillText(t, name))

		licenseRel := compiler.CanonicalSkillLicenseRel(name)
		priorLicenseText := "MIT License\n\nCopyright (c) 2026 Test Prior Author\n\nearlier unedited licence fixture\n"
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(licenseRel)), priorLicenseText)
		digest, _, err := util.CanonicalTextDigest([]byte(priorLicenseText))
		if err != nil {
			t.Fatal(err)
		}
		if priorSkillLicenseDigests[licenseRel] == nil {
			priorSkillLicenseDigests[licenseRel] = make(map[string]string)
		}
		priorSkillLicenseDigests[licenseRel][digest] = "earlier unedited licence fixture"
		t.Cleanup(func() {
			delete(priorSkillLicenseDigests[licenseRel], digest)
		})
	}
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	for i := 0; i < len(bundle) && i < maxRegisterSkills; i++ {
		name := bundle[i]
		licenseRel := compiler.CanonicalSkillLicenseRel(name)
		if got := repoText(t, repoPath, licenseRel); got != sourceSkillLicenseText(t, name) {
			t.Errorf("%s was not refreshed to current licence text", licenseRel)
		}
		if got := findActionDetail(rep.ActionDetails, licenseRel); !strings.Contains(got, "Refreshed the unedited earlier Praetor licence") {
			t.Errorf("%s action detail %q, want licence refresh", licenseRel, got)
		}
	}
}

// Negative: a hand-edited skill licence in an adopter repository is preserved (refused overwrite)
// on a plain run without --force and reported with a warning.
func TestAdopt_Negative_HandEditedLicenseRefusedOnPlainRun(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-hand-edited-license")
	licenseRel := compiler.CanonicalSkillLicenseRel("caveman")
	edited := sourceSkillLicenseText(t, "caveman") + "\n# custom adopter licence modification\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(licenseRel)), edited)
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	if got := repoText(t, repoPath, licenseRel); got != edited {
		t.Fatal("plain run replaced a hand-edited caveman licence")
	}
	if warningNaming(rep, licenseRel) == "" {
		t.Errorf("the kept hand-edited caveman licence is not reported in warnings: %v", rep.Warnings)
	}
	if contains(rep.CreatedFiles, licenseRel) || hasAction(rep, licenseRel, actionReplace) {
		t.Errorf("hand-edited licence reported created or replaced: created=%v actions=%+v", rep.CreatedFiles, rep.ActionDetails)
	}
}

// reuseLintSkills copies the register skill files adoption installed in repoPath, plus a licence
// file for the EUPL-1.2 their headers name, into a fresh directory and runs the REUSE tool over
// it. Only the skills are linted: the rest of an adopted tree is the adopter's to annotate.
func reuseLintSkills(t *testing.T, reuse, repoPath, extraRel string) error {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "LICENSES", "EUPL-1.2.txt"), mustRead(t, filepath.Join(sourceCheckout, "LICENSES", "EUPL-1.2.txt")))
	for _, dir := range []string{compiler.CanonicalSkillsRel, ".claude/skills"} {
		for _, name := range config.RegisterSkillBundle() {
			for _, rel := range []string{compiler.SkillEntryRel(dir, name), compiler.SkillLicenseRel(dir, name)} {
				mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), repoText(t, repoPath, rel))
			}
		}
	}
	if extraRel != "" {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(extraRel)), repoText(t, repoPath, compiler.CanonicalSkillLicenseRel("caveman")))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, reuse, "--root", root, "lint").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reuse lint: %w\n%s", err, out)
	}
	return nil
}

// Positive and negative (Rule 13): the skills adoption installs pass the REUSE lint of an adopter
// that annotates nothing, because the licence text is a file named LICENSE, which the tool reads
// as a licence file. The same text under the name NOTICE has no licence information and fails, so
// the check is proven to see the defect it guards. It skips where the reuse tool is not installed,
// naming the reason.
func TestAdopt_Positive_InstalledSkillsPassAdopterReuseLint(t *testing.T) {
	reuse, err := exec.LookPath("reuse")
	if err != nil {
		t.Skipf("the reuse tool is not installed (%v); the check cannot run here", err)
	}
	repoPath := newTestRepo(t, "register-skills-reuse")
	adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	if err := reuseLintSkills(t, reuse, repoPath, ""); err != nil {
		t.Fatalf("the installed register skills fail an adopter's REUSE lint: %v", err)
	}
	if err := reuseLintSkills(t, reuse, repoPath, compiler.CanonicalSkillsRel+"/caveman/NOTICE"); err == nil {
		t.Fatal("a licence text named NOTICE passed REUSE lint, so the check does not see the defect")
	}
}
