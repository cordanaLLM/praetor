// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// Adoption ships the skills the text register block names, and adhd-format, which social-text
// inherits from (#235).

// priorSkillFixtures holds, per skill of the bundle, every text a Praetor release shipped at its
// canonical path, in a directory named after the skill.
const priorSkillFixtures = "testdata/skills"

// claudeSkillRel is the copy of skill name in the skill directory Claude Code reads.
func claudeSkillRel(name string) string {
	return ".claude/skills/" + name + "/" + compiler.SkillEntryName
}

// sourceSkillText is the SKILL.md of skill name this repository ships.
func sourceSkillText(t *testing.T, name string) string {
	t.Helper()
	return mustRead(t, filepath.Join(sourceCheckout, filepath.FromSlash(canonicalSkillRel(name))))
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
		assertPriorDigestsReproduced(t, filepath.Join(priorSkillFixtures, name), priorSkillDigests[canonicalSkillRel(name)])
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
		if !isPriorRendering(data, priorSkillDigests[canonicalSkillRel(name)]) {
			t.Errorf("the shipped %s (%s) is not in priorSkillDigests", name, fixtureDigest(t, name, data))
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
		if got := repoText(t, repoPath, canonicalSkillRel(name)); got != want {
			t.Errorf("%s differs from the shipped skill", canonicalSkillRel(name))
		}
		if got := repoText(t, repoPath, claudeSkillRel(name)); got != want {
			t.Errorf("%s differs from the shipped skill", claudeSkillRel(name))
		}
		if !strings.Contains(want, "SPDX-License-Identifier: EUPL-1.2") {
			t.Errorf("%s ships without its REUSE licence header", name)
		}
		if !contains(rep.CreatedFiles, canonicalSkillRel(name)) || !contains(rep.CreatedFiles, claudeSkillRel(name)) {
			t.Errorf("%s and its client copy not reported created: %v", name, rep.CreatedFiles)
		}
	}
	if !strings.Contains(repoText(t, repoPath, canonicalSkillRel("caveman")), "derived_from: \"https://github.com/JuliusBrussee/caveman (MIT)\"") {
		t.Error("caveman ships without its upstream credit")
	}
	agents := repoText(t, repoPath, agentsFile)
	for _, named := range []string{"`social-text` skill:", "`caveman` skill:", "`caveman` brief shape"} {
		if !strings.Contains(agents, named) {
			t.Errorf("AGENTS.md does not name %q", named)
		}
	}
	source := filepath.Join(repoPath, agentsFile)
	if err := compiler.VerifyCompiledContext(t.Context(), io.Discard, compiler.NewTranspiler(), source, repoPath); err != nil {
		t.Fatalf("compile-context --verify after adoption: %v", err)
	}
}

// Negative: an edited skill is the repository's: a forced run keeps it and reports it, and its
// client copy follows the kept text. A hand-edited client copy is compiled output and is
// replaced from the canonical skill with a backup, as a persona copy is.
func TestAdopt_Negative_EditedSkillIsKeptAndReported(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-edited")
	edited := sourceSkillText(t, "caveman") + "\n## Local\n\n- keep repository terms\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(canonicalSkillRel("caveman"))), edited)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(claudeSkillRel("social-text"))), "# hand copy\n")
	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), true)
	if got := repoText(t, repoPath, canonicalSkillRel("caveman")); got != edited {
		t.Fatal("--force replaced an edited caveman skill")
	}
	if warningNaming(rep, canonicalSkillRel("caveman")) == "" {
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

// Boundary: a declined agent-harness step installs no skill, and the register block then names
// none; a selection without Claude Code gets the canonical skills and no client copy, with
// .claude/skills reported not applicable; a dry run lists the skills and copies it would write
// and writes none.
func TestAdopt_Boundary_DeclinedSelectedAndPreviewed(t *testing.T) {
	t.Run("declined agent-harness", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-declined")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [agent-harness]\n")
		adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		for _, name := range config.RegisterSkillBundle() {
			assertAbsent(t, repoPath, canonicalSkillRel(name))
			assertAbsent(t, repoPath, claudeSkillRel(name))
		}
		_, block, err := compiler.LoadRegisterBlock(t.Context(), repoPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(block, "`social-text`") || strings.Contains(block, "`caveman`") {
			t.Fatalf("the block names a skill the declined step never installed:\n%s", block)
		}
	})
	t.Run("agent_clients without Claude Code", func(t *testing.T) {
		repoPath := newTestRepo(t, "register-skills-codex")
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nagent_clients: [codex]\n")
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if got := repoText(t, repoPath, canonicalSkillRel("caveman")); got != sourceSkillText(t, "caveman") {
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
			if !contains(rep.CreatedFiles, canonicalSkillRel(name)) || !contains(rep.CreatedFiles, claudeSkillRel(name)) {
				t.Errorf("the preview does not list %s and its client copy: %v", name, rep.CreatedFiles)
			}
			assertAbsent(t, repoPath, canonicalSkillRel(name))
		}
		assertAbsent(t, repoPath, ".claude")
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
	if err := os.Remove(filepath.Join(partial, filepath.FromSlash(canonicalSkillRel("adhd-format")))); err != nil {
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
