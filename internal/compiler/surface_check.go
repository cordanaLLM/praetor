package compiler

import (
	"context"
	"fmt"
	"slices"
)

// CheckVendorTargets runs the writer's refusals (checkProjectionFiles) over the vendor file of
// every client agent_clients in the manifest at root selects, writing nothing. Adoption runs it
// before its first write, so a symlinked .github or .cursor fails adoption with nothing written
// instead of at the step that compiles the vendor files.
func CheckVendorTargets(ctx context.Context, root string) error {
	files, err := NewTranspiler().vendorTargetFiles(ctx, root)
	if err != nil {
		return err
	}
	return checkProjectionFiles(ctx, root, files)
}

// CheckPersonaTargets runs the writer's refusals over every persona file below root that
// CompileAgents reads or writes once the canonical personas named in added exist, writing
// nothing: each persona under .agents/agents, those already there and those in added, and its
// copy in every persona directory agent_clients selects. A symlinked .agents or persona
// directory, a canonical persona that is not a regular text file, and a canonical set above
// CompileAgents' cap are refused here, before adoption writes the personas in added.
func CheckPersonaTargets(ctx context.Context, root string, added []string) error {
	dirs, _, err := SelectPersonaDirs(ctx, root)
	if err != nil {
		return err
	}
	existing, err := listCanonicalAgents(ctx, root)
	if err != nil {
		return err
	}
	// Both sets by name, once each: a persona in added may already exist.
	names := append(slices.Clone(existing), added...)
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) > maxAgentProjections {
		return fmt.Errorf("%s would hold more than %d files", CanonicalAgentsRel, maxAgentProjections)
	}
	files := make([]projectionFile, 0, len(names)*(len(dirs)+1))
	for _, name := range names {
		files = append(files, projectionFile{rel: CanonicalAgentsRel + "/" + name})
		for _, dir := range dirs {
			files = append(files, projectionFile{rel: dir + "/" + name})
		}
	}
	return checkProjectionFiles(ctx, root, files)
}
