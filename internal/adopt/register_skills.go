// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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
// and compile-context --verify and audit check those copies. compile-context names a skill in the
// block only where the repository carries it (compiler.LoadRegisterBlock), so a declined
// agent-harness step installs nothing and leaves a block that names no skill.

// priorSkillDigests are the digests (priorRendering) of every text a Praetor release shipped at
// each canonical skill path of the bundle, keyed by path and then to the release that shipped
// it; the current texts are among them. A skill holding one of them is Praetor's unedited text
// and follows the bundle without --force; any other text is the repository's and is kept,
// --force included. testdata/skills reproduces each digest, and
// TestPriorSkillDigests_Boundary_CurrentSourcesRecorded fails until a changed skill is recorded
// here (register_skills_test.go).
var priorSkillDigests = map[string]map[string]string{
	canonicalSkillRel("social-text"): {
		"7f034f3a4a956d8c93dd661d985d65ce647674a8b248d691a88e271663286cbd": "first shipped, REUSE header (#235)",
	},
	canonicalSkillRel("caveman"): {
		"9b134e15dd3bf3de620ae8001af85c772c83ef190074a528361ca5bd9fec189b": "first shipped, REUSE header (#235)",
	},
	canonicalSkillRel("adhd-format"): {
		"312e9934100fe302003588482d633bcba0a93cb963ab2ae9e43604c4b4991179": "first shipped, REUSE header (#235)",
	},
}

// maxRegisterSkills bounds every walk over the skill bundle (HISS-02).
const maxRegisterSkills = 8

// canonicalSkillRel is the slash path of the SKILL.md of skill name under .agents/skills.
func canonicalSkillRel(name string) string {
	return compiler.CanonicalSkillsRel + "/" + name + "/" + compiler.SkillEntryName
}

// skillSource is one skill of the bundle as the verified source bundle holds it.
type skillSource struct {
	name string
	data []byte
}

// installRegisterSkills writes each skill of the bundle from the verified source bundle into
// .agents/skills: created when absent, refreshed when it holds an earlier Praetor text of it
// (priorSkillDigests), and otherwise kept and reported, --force included, since audit never
// compares a canonical skill with the bundle. Without a source bundle, or with one that lacks a
// skill of the bundle, nothing is installed, and the warning names every register skill the
// block will then leave out (warnAbsentRegisterSkills). It runs first in the agent-harness step,
// so the register block the step renders names the skills the run leaves behind.
func (s *adoptSession) installRegisterSkills(ctx context.Context) error {
	if s.opts.LockSourceRoot == "" {
		return s.warnAbsentRegisterSkills(ctx, "no verified source bundle was selected (--lock-source-root)")
	}
	sources, err := readRegisterSkillSources(ctx, s.opts.LockSourceRoot)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return s.warnAbsentRegisterSkills(ctx, err.Error())
	}
	for i := 0; i < len(sources) && i < maxRegisterSkills; i++ {
		rel := canonicalSkillRel(sources[i].name)
		if _, err := s.scaffoldFile(ctx, scaffold{
			rel: rel, perm: filePerm, content: sources[i].data, confined: true, prior: priorSkillDigests[rel],
			created:   "Installed the " + sources[i].name + " skill of the text register from the verified source bundle",
			verified:  "Existing " + sources[i].name + " skill verified identical to the verified source bundle",
			refreshed: "Refreshed the unedited earlier Praetor " + sources[i].name + " skill to the verified source bundle",
		}); err != nil {
			return err
		}
	}
	return nil
}

// readRegisterSkillSources reads every skill of the bundle from the source bundle at root without
// following a symlink (contextopt.ObserveSnapshotIn). The bundle is one unit: a source that lacks
// one skill installs none, since social-text without adhd-format, or a block naming one skill of
// two, is half a register.
func readRegisterSkillSources(ctx context.Context, root string) ([]skillSource, error) {
	names := config.RegisterSkillBundle()
	sources := make([]skillSource, 0, len(names))
	for i := 0; i < len(names) && i < maxRegisterSkills; i++ {
		rel := canonicalSkillRel(names[i])
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(rel))
		if err != nil {
			return nil, fmt.Errorf("read %s from the source bundle: %w", rel, err)
		}
		if !exists {
			return nil, fmt.Errorf("the source bundle %s holds no %s", root, rel)
		}
		sources = append(sources, skillSource{name: names[i], data: data})
	}
	return sources, nil
}

// warnAbsentRegisterSkills warns, when the run installs no skill for reason, about every register
// skill the repository will still lack: the block then states that register's form without the
// skill's name. A repository that carries them all is told nothing.
func (s *adoptSession) warnAbsentRegisterSkills(ctx context.Context, reason string) error {
	var absent []string
	names := config.RegisterSkills()
	for i := 0; i < len(names) && i < maxRegisterSkills; i++ {
		rel := canonicalSkillRel(names[i])
		_, present, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel))
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if !present {
			absent = append(absent, names[i])
		}
	}
	if len(absent) > 0 {
		s.report.addWarning("register skills not installed: %s; the text register block names no %s skill until %s "+
			"carries it (praetorctl adopt --lock-source-root=<praetor checkout> installs it)",
			reason, strings.Join(absent, " or "), compiler.CanonicalSkillsRel)
	}
	return nil
}

// pendingSkills returns, in a dry run, the skills of the bundle the run planned to install or
// refresh (planDryRunWrite), keyed by name, which a real run has on disk by the time it renders
// the register block and projects the client copies; a real run gets none and reads the disk.
func (s *adoptSession) pendingSkills() map[string][]byte {
	if !s.opts.DryRun {
		return nil
	}
	pending := make(map[string][]byte)
	names := config.RegisterSkillBundle()
	for i := 0; i < len(names) && i < maxRegisterSkills; i++ {
		if content := s.dryRunWrites[canonicalSkillRel(names[i])]; content != nil {
			pending[names[i]] = content
		}
	}
	return pending
}

// registerBlock renders the text register block of the repository as the run leaves it: over the
// skills a dry run planned to install (pendingSkills).
func (s *adoptSession) registerBlock(ctx context.Context) (string, error) {
	pending := slices.Sorted(maps.Keys(s.pendingSkills()))
	_, block, err := compiler.LoadRegisterBlockOver(ctx, s.repoPath, pending)
	if err != nil {
		return "", fmt.Errorf("resolve the text register block for the harness: %w", err)
	}
	return block, nil
}

// preflightRegisterSkills runs, before the first step writes, the writer's refusals over every
// skill file the run writes: the canonical skills the agent-harness step installs from a source
// bundle (installedRegisterSkills) and, unless agent-definitions is declined, the client copies
// that step projects of every bundle skill the repository then carries
// (compiler.CheckSkillTargets). A declined step's files are not checked.
func preflightRegisterSkills(ctx context.Context, s *adoptSession, declined map[string]bool) error {
	added := s.installedRegisterSkills(declined)
	if !declined["agent-definitions"] {
		if err := compiler.CheckSkillTargets(ctx, s.repoPath, added); err != nil {
			return fmt.Errorf("skill files cannot be written: %w", err)
		}
		return nil
	}
	for i := 0; i < len(added) && i < maxRegisterSkills; i++ {
		rel := canonicalSkillRel(added[i])
		if _, _, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel)); err != nil {
			return fmt.Errorf("skill %s cannot be written: %w", rel, err)
		}
	}
	return nil
}

// installedRegisterSkills returns the skills the agent-harness step installs: the whole bundle
// when the step runs with a source bundle, and none otherwise.
func (s *adoptSession) installedRegisterSkills(declined map[string]bool) []string {
	if declined["agent-harness"] || s.opts.LockSourceRoot == "" {
		return nil
	}
	return config.RegisterSkillBundle()
}
