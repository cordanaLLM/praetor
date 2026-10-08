// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// The agent-harness step ships the skills the text register block names (#235). The block tells
// every agent to write forge text with the `social-text` skill and agent traffic with the
// `caveman` skill, and adoption used to install neither, so the names pointed at nothing. The
// step now installs the register skill bundle (config.RegisterSkillBundle: social-text, caveman,
// and adhd-format, which social-text inherits from) from the verified source bundle
// (--lock-source-root) into .agents/skills, the way it installs the checkpoint bundle. Each
// SKILL.md carries its REUSE licence header, and caveman its metadata.derived_from upstream
// credit, in the bytes it is installed with. The agent-definitions step then projects each one
// into the skill directory of every agent client that does not read .agents/skills
// (compiler.CompileAgentSurfaces, .claude/skills for Claude Code), as it projects the personas,
// and compile-context --verify and audit check those copies. A declined agent-definitions step
// writes no copy, so the harness step then installs a skill only where agent_clients selects no
// such directory (planRegisterSkills). compile-context names a skill in the block, and adoption in
// the Paperclip harness directive, only where the repository carries it
// (compiler.AbsentRegisterSkills), so a run that installs nothing leaves no skill name behind.

// priorSkillDigests are the digests (priorRendering) of every text a Praetor release shipped at
// each canonical skill path of the bundle, keyed by path and then to the release that shipped
// it; the current texts are among them. A skill holding one of them is Praetor's unedited text
// and follows the bundle without --force; any other text is the repository's and is kept,
// --force included. testdata/skills reproduces each digest, and
// TestPriorSkillDigests_Boundary_CurrentSourcesRecorded fails until a changed skill is recorded
// here (register_skills_test.go).
var priorSkillDigests = map[string]map[string]string{
	compiler.CanonicalSkillRel("social-text"): {
		"a128a86ad170bfaf89012567e1405693006627e260cd18c420f3abb500dd6d9e": "first shipped, REUSE header and upstream credit (#235)",
		"66c09c5a6102bdd5b8bf81859fd4896f9627d29e3795b54b0623320dccd11a38": "inline credit, no repository path an adopter lacks (#850)",
	},
	compiler.CanonicalSkillRel("caveman"): {
		"c0ab6d15d42d9dab2640eb53abf7865495ab99979931cd463540646974d59b49": "first shipped, REUSE header and upstream credit (#235)",
		"8709bb0106e84d6ce2ead7d708b23d0335fb6a072ddce893837faee8ebcbd7bd": "inline credit, no repository path an adopter lacks (#850)",
	},
	compiler.CanonicalSkillRel("adhd-format"): {
		"1db3a6e0ec137143cb81002491bab5f5ae6a9331333126423de199a1c66edb44": "first shipped, REUSE header and upstream credit (#235)",
		"1d5efc9bf3e177ddb016daf50663cd7554bac8495b408fa5e784ebfc6cfdd8ac": "inline credit, no repository path an adopter lacks (#850)",
	},
}

// priorSkillLicenseDigests are the digests of every LICENSE text a Praetor release shipped at each
// canonical skill licence path of the bundle, the current texts among them: the upstream MIT text
// of the skill's origin, byte for byte. testdata/skill-licenses reproduces each digest, and
// TestPriorSkillLicenseDigests_Boundary_CurrentSourcesRecorded fails until a changed licence is
// recorded here. An earlier text no release shipped is replaced here, never kept beside the new
// one.
var priorSkillLicenseDigests = map[string]map[string]string{
	compiler.CanonicalSkillLicenseRel("social-text"): {
		"8acbb618089b738404f76ac60fca1aaa995d9a87995eddf95f0b777acd7f7d2c": "first shipped, upstream MIT text of i-have-adhd (#850)",
	},
	compiler.CanonicalSkillLicenseRel("caveman"): {
		"94fe75d355887f84ee7eefca68e06d8d082a23dad47a8e1f58a94d98900edf5b": "first shipped, upstream MIT text of Caveman at 8dffbb260 (#850)",
	},
	compiler.CanonicalSkillLicenseRel("adhd-format"): {
		"8acbb618089b738404f76ac60fca1aaa995d9a87995eddf95f0b777acd7f7d2c": "first shipped, upstream MIT text of i-have-adhd (#850)",
	},
}

// maxRegisterSkills bounds every walk over the skill bundle (HISS-02).
const maxRegisterSkills = 8

// skillSource is one skill of the bundle as the verified source bundle holds it.
type skillSource struct {
	name    string
	data    []byte
	license []byte
}

// registerSkillPlan is what the agent-harness step installs, resolved once per run
// (registerSkillSources): the preflight, the manifest step's Paperclip harness, the register
// block and the install itself then agree on it.
type registerSkillPlan struct {
	resolved bool
	sources  []skillSource
	// skipped says why the run installs none, for the warning; empty when it installs the
	// bundle or the agent-harness step is declined.
	skipped string
}

// registerSkillSources returns the skills the agent-harness step installs and, when it installs
// none, why (planRegisterSkills), resolved on first use and kept for the run.
func (s *adoptSession) registerSkillSources(ctx context.Context) ([]skillSource, string, error) {
	if s.registerSkills.resolved {
		return s.registerSkills.sources, s.registerSkills.skipped, nil
	}
	sources, skipped, err := s.planRegisterSkills(ctx)
	if err != nil {
		return nil, "", err
	}
	s.registerSkills = registerSkillPlan{resolved: true, sources: sources, skipped: skipped}
	return sources, skipped, nil
}

// planRegisterSkills decides what the agent-harness step installs: nothing when the step is
// declined, when no source bundle was selected, or when agent-definitions is declined while
// agent_clients selects a client skill directory, since only that step writes the copies
// compile-context --verify then requires there; otherwise the whole bundle, read from the
// source bundle (readRegisterSkillSources), or nothing when the source lacks a skill of it.
func (s *adoptSession) planRegisterSkills(ctx context.Context) ([]skillSource, string, error) {
	if s.declines("agent-harness") {
		return nil, "", nil
	}
	if s.opts.LockSourceRoot == "" {
		return nil, "no verified source bundle was selected (praetorctl adopt --lock-source-root=<praetor checkout> installs them)", nil
	}
	if s.declines("agent-definitions") {
		dirs, _, err := compiler.SelectSkillDirs(ctx, s.repoPath)
		if err != nil {
			return nil, "", fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
		}
		if len(dirs) > 0 {
			return nil, fmt.Sprintf("agent-definitions is declined, and only that step writes the copies in %s that "+
				"compile-context --verify requires of an installed skill (stop declining it, or leave those clients "+
				"out of agent_clients)", strings.Join(dirs, ", ")), nil
		}
	}
	sources, err := readRegisterSkillSources(ctx, s.opts.LockSourceRoot)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", ctxErr
		}
		return nil, err.Error(), nil
	}
	return sources, "", nil
}

// plannedRegisterSkills names the skills the agent-harness step installs (registerSkillSources),
// which count as carried wherever the run renders text that names them before or after it
// installs them: the manifest step's Paperclip harness and, in a dry run, the register block.
func (s *adoptSession) plannedRegisterSkills(ctx context.Context) ([]string, error) {
	sources, _, err := s.registerSkillSources(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sources))
	for i := 0; i < len(sources) && i < maxRegisterSkills; i++ {
		names = append(names, sources[i].name)
	}
	return names, nil
}

// installRegisterSkills writes each skill of the bundle from the verified source bundle into
// .agents/skills: created when absent, refreshed when it holds an earlier Praetor text of it
// (priorSkillDigests), and otherwise kept and reported, --force included, since audit never
// compares a canonical skill with the bundle. When the run installs none (planRegisterSkills),
// the warning names every register skill the block will then leave out
// (warnAbsentRegisterSkills). It runs first in the agent-harness step, so the register block the
// step renders names the skills the run leaves behind.
func (s *adoptSession) installRegisterSkills(ctx context.Context) error {
	sources, skipped, err := s.registerSkillSources(ctx)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return s.warnAbsentRegisterSkills(ctx, skipped)
	}
	for i := 0; i < len(sources) && i < maxRegisterSkills; i++ {
		rel := compiler.CanonicalSkillRel(sources[i].name)
		if _, err := s.scaffoldFile(ctx, scaffold{
			rel: rel, perm: filePerm, content: sources[i].data, confined: true, prior: priorSkillDigests[rel],
			created:   "Installed the " + sources[i].name + " skill of the text register from the verified source bundle",
			verified:  "Existing " + sources[i].name + " skill verified identical to the verified source bundle",
			refreshed: "Refreshed the unedited earlier Praetor " + sources[i].name + " skill to the verified source bundle",
		}); err != nil {
			return err
		}
		if len(sources[i].license) > 0 {
			licenseRel := compiler.CanonicalSkillLicenseRel(sources[i].name)
			if _, err := s.scaffoldFile(ctx, scaffold{
				rel: licenseRel, perm: filePerm, content: sources[i].license, confined: true, prior: priorSkillLicenseDigests[licenseRel],
				created:   "Installed the upstream licence of the " + sources[i].name + " skill from the verified source bundle",
				verified:  "Existing upstream licence of the " + sources[i].name + " skill verified identical to the verified source bundle",
				refreshed: "Refreshed the unedited earlier Praetor licence of the " + sources[i].name + " skill to the verified source bundle",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// readRegisterSkillSources reads every skill of the bundle from the source bundle at root through
// the reader compile-context reads a canonical skill with (compiler.ReadCanonicalSkill), so a
// symlinked or oversized source skill is refused the same way. The bundle is one unit: a source
// that lacks one skill installs none, since social-text without adhd-format, or a block naming
// one skill of two, is half a register.
func readRegisterSkillSources(ctx context.Context, root string) ([]skillSource, error) {
	names := config.RegisterSkillBundle()
	sources := make([]skillSource, 0, len(names))
	for i := 0; i < len(names) && i < maxRegisterSkills; i++ {
		data, exists, err := compiler.ReadCanonicalSkill(ctx, root, names[i])
		if err != nil {
			return nil, fmt.Errorf("the source bundle %s: %w", root, err)
		}
		if !exists {
			return nil, fmt.Errorf("the source bundle %s holds no %s", root, compiler.CanonicalSkillRel(names[i]))
		}
		if err := compiler.CheckShippedSkillReferences(names[i], data); err != nil {
			return nil, fmt.Errorf("the source bundle %s: %w", root, err)
		}
		license, err := readSkillLicense(ctx, root, names[i], data)
		if err != nil {
			return nil, err
		}
		sources = append(sources, skillSource{name: names[i], data: data, license: license})
	}
	return sources, nil
}

// readSkillLicense reads the LICENSE the source bundle holds beside skill name, whose SKILL.md is
// data. A skill whose declared upstream licence requires its text to travel with copies
// (compiler.SkillRequiresLicense) must have one; any other skill installs the file only when the
// source holds it.
func readSkillLicense(ctx context.Context, root, name string, data []byte) ([]byte, error) {
	required, err := compiler.SkillRequiresLicense(data)
	if err != nil {
		return nil, fmt.Errorf("the source bundle %s: skill %s: %w", root, name, err)
	}
	license, exists, err := compiler.ReadCanonicalSkillLicense(ctx, root, name)
	if err != nil {
		return nil, fmt.Errorf("the source bundle %s: %w", root, err)
	}
	if required && !exists {
		return nil, fmt.Errorf("the source bundle %s holds no %s for %s", root, compiler.CanonicalSkillLicenseRel(name), name)
	}
	return license, nil
}

// warnAbsentRegisterSkills warns, when the run installs no skill for reason, about every register
// skill the repository will still lack (compiler.AbsentRegisterSkills): the block then states that
// register's form without the skill's name. A repository that carries them all, and a run whose
// agent-harness step is declined (an empty reason), are told nothing.
func (s *adoptSession) warnAbsentRegisterSkills(ctx context.Context, reason string) error {
	if reason == "" {
		return nil
	}
	absent, err := compiler.AbsentRegisterSkills(ctx, s.repoPath, nil)
	if err != nil {
		return err
	}
	if len(absent) > 0 {
		s.report.addWarning("register skills not installed: %s; the text register block names no %s skill until %s "+
			"carries it", reason, strings.Join(absent, " or "), compiler.CanonicalSkillsRel)
	}
	return nil
}

// pendingSkills returns, in a dry run, the skills of the bundle the run planned to install or
// refresh (planDryRunWrite), keyed by name, which a real run has on disk by the time it projects
// the client copies; a real run gets none and reads the disk. A kept edited skill is not among
// them: its copies follow the text on disk.
func (s *adoptSession) pendingSkills() map[string][]byte {
	return s.pendingBundleFiles(compiler.CanonicalSkillRel)
}

// pendingLicenses is pendingSkills for the LICENSE beside each skill.
func (s *adoptSession) pendingLicenses() map[string][]byte {
	return s.pendingBundleFiles(compiler.CanonicalSkillLicenseRel)
}

// pendingBundleFiles returns, in a dry run, the file relOf names for each skill of the bundle that
// the run planned to write (planDryRunWrite), keyed by skill name; a real run gets none.
func (s *adoptSession) pendingBundleFiles(relOf func(name string) string) map[string][]byte {
	if !s.opts.DryRun {
		return nil
	}
	pending := make(map[string][]byte)
	names := config.RegisterSkillBundle()
	for i := 0; i < len(names) && i < maxRegisterSkills; i++ {
		if content := s.dryRunWrites[relOf(names[i])]; content != nil {
			pending[names[i]] = content
		}
	}
	return pending
}

// registerBlock renders the text register block of the repository as the run leaves it: with the
// skills the run installs counted as carried (plannedRegisterSkills), which a dry run never
// writes.
func (s *adoptSession) registerBlock(ctx context.Context) (string, error) {
	pending, err := s.plannedRegisterSkills(ctx)
	if err != nil {
		return "", err
	}
	_, block, err := compiler.LoadRegisterBlockOver(ctx, s.repoPath, pending)
	if err != nil {
		return "", fmt.Errorf("resolve the text register block for the harness: %w", err)
	}
	return block, nil
}

// preflightRegisterSkills runs, before the first step writes, the writer's refusals over every
// skill file the run writes (compiler.CheckSkillTargets): the canonical skills the agent-harness
// step installs (registerSkillSources) and, unless agent-definitions is declined, the client
// copies that step projects of every bundle skill the repository then carries. With
// agent-definitions declined the harness step installs a skill only where agent_clients selects no
// client skill directory (planRegisterSkills), so the check then covers the canonical skills
// alone; with nothing installed either, a declined step's files are not checked.
func preflightRegisterSkills(ctx context.Context, s *adoptSession, declined map[string]bool) error {
	added, err := s.plannedRegisterSkills(ctx)
	if err != nil {
		return err
	}
	if declined["agent-definitions"] && len(added) == 0 {
		return nil
	}
	if err := compiler.CheckSkillTargets(ctx, s.repoPath, added); err != nil {
		return fmt.Errorf("skill files cannot be written: %w", err)
	}
	return nil
}
