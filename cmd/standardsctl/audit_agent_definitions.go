package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// maxReportedAgentEntries bounds the unprojected .agents/agents entries one audit line names;
// the directory itself holds at most compiler.MaxAgentFiles.
const maxReportedAgentEntries = 10

// agentDefinitionsLayout is the layout compile-context projects, which the gate names when it
// finds another.
const agentDefinitionsLayout = "praetor reads one persona file per agent, " +
	compiler.CanonicalAgentsRel + "/<name>.md"

// auditAgentDefinitions counts the personas compile-context projects, read through the same
// walk (compiler.ReadAgentInventory), and fails on an entry of .agents/agents that can hold an
// agent definition compile-context skips, such as a <name>/AGENTS.md directory, naming each.
// An agent-definitions decline in adoption.decline is honoured, as audit honours the readme
// decline: the gate reports what the directory holds and fails on nothing but an unreadable
// directory or a refused persona.
func auditAgentDefinitions(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	declined, err := adopt.ManifestArtifactDeclined(manifest, "agent-definitions")
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve agent-definitions adoption decline: %w", err)
	}
	inventory, err := compiler.ReadAgentInventory(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Failed to inspect %s: %w", compiler.CanonicalAgentsRel, err)
	}
	if declined {
		fmt.Printf("[INFO] Agent definitions declined by adoption.decline%s.\n", describeAgentInventory(inventory))
		return nil
	}
	if !inventory.Present {
		return nil
	}
	if n := len(inventory.Unprojected); n > 0 {
		return fmt.Errorf("[FAIL] %s holds entries compile-context does not project (%d): %s; %s; move each "+
			"definition to that layout, or decline agent-definitions in adoption.decline to keep another",
			compiler.CanonicalAgentsRel, n, listUnprojectedAgents(inventory.Unprojected), agentDefinitionsLayout)
	}
	if len(inventory.Personas) == 0 {
		return fmt.Errorf("[FAIL] %s directory exists but contains zero agent definitions; %s",
			compiler.CanonicalAgentsRel, agentDefinitionsLayout)
	}
	fmt.Printf("[PASS] Agent definitions verified (%d agents registered).\n", len(inventory.Personas))
	return nil
}

// describeAgentInventory is the declined gate's account of .agents/agents, empty when the
// directory is absent.
func describeAgentInventory(inventory compiler.AgentInventory) string {
	if !inventory.Present {
		return ""
	}
	text := fmt.Sprintf("; %s holds %d persona file(s) compile-context projects",
		compiler.CanonicalAgentsRel, len(inventory.Personas))
	if n := len(inventory.Unprojected); n > 0 {
		text += fmt.Sprintf(" and entries it does not project (%d): %s", n, listUnprojectedAgents(inventory.Unprojected))
	}
	return text
}

// listUnprojectedAgents names at most maxReportedAgentEntries entries with what each holds,
// and says how many more there are.
func listUnprojectedAgents(entries []compiler.UnprojectedAgentEntry) string {
	parts := make([]string, 0, maxReportedAgentEntries+1)
	for i := 0; i < len(entries) && i < maxReportedAgentEntries; i++ {
		parts = append(parts, fmt.Sprintf("%s (%s)", entries[i].Name, entries[i].Detail))
	}
	if more := len(entries) - maxReportedAgentEntries; more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	return strings.Join(parts, ", ")
}
