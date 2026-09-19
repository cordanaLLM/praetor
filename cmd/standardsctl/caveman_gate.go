package main

import (
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// lintCanonicalPersonas runs the caveman lint plus compiler.AgentTextCeiling over every
// canonical persona under .agents/agents. Personas sit under config.SurfaceContext by its
// own doc comment ("AGENTS.md, the compiled vendor files, personas and skills";
// internal/config/register.go), so they carry the same fixed, no-opt-out rule as AGENTS.md
// itself (ADR-0010 decision 11) rather than an emission surface a manifest can opt out of.
// It returns the number of personas linted.
func lintCanonicalPersonas(rootDir string) (int, error) {
	names, err := listCanonicalAgents(rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(canonicalAgentsRel, names[i])
		if _, err := compiler.LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// lintCanonicalSkillFiles runs the caveman lint plus compiler.AgentTextCeiling over every
// canonical skill's SKILL.md under .agents/skills, on the same SurfaceContext basis as
// lintCanonicalPersonas. It returns the number of skills linted.
func lintCanonicalSkillFiles(rootDir string) (int, error) {
	names, err := listCanonicalSkills(rootDir)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, err := readCanonicalSkill(rootDir, names[i])
		if err != nil {
			return 0, err
		}
		label := filepath.Join(canonicalSkillsRel, names[i], skillEntryName)
		if _, err := compiler.LintAgentText(label, string(data)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}
