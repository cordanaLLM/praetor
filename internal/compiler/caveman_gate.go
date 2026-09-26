package compiler

import (
	"context"
	"path/filepath"
)

// LintCanonicalPersonas runs the caveman lint plus AgentTextCeiling over every
// canonical persona under .agents/agents. Personas sit under config.SurfaceContext by its
// own doc comment ("AGENTS.md, the compiled vendor files, personas and skills";
// internal/config/register.go), so they carry the same fixed, no-opt-out rule as AGENTS.md
// itself (ADR-0010 decision 11) rather than an emission surface a manifest can opt out of.
// It returns the number of personas linted.
func LintCanonicalPersonas(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(CanonicalAgentsRel, names[i])
		if _, err := LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// LintCanonicalSkillFiles runs the caveman lint plus AgentTextCeiling over every
// canonical skill's SKILL.md under .agents/skills, on the same SurfaceContext basis as
// LintCanonicalPersonas. It returns the number of skills linted.
func LintCanonicalSkillFiles(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalSkills(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, err := readCanonicalSkill(ctx, rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(CanonicalSkillsRel, names[i], SkillEntryName)
		if _, err := LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}
