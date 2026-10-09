package compiler

import (
	"context"
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

// CheckPersonaTargets runs the writer's refusals over every file below root that
// CompileAgentSurfaces reads or writes once the canonical personas named in added exist, writing
// nothing: each persona under .agents/agents, those already there and those in added, its copy
// in every persona directory agent_clients selects and, when the repository ships the plugin,
// its plugin copy and the plugin copy of every canonical skill. A symlinked .agents, persona or
// plugin directory, a canonical persona that is not a regular text file, and a canonical set
// above CompileAgentSurfaces' cap are refused here, before adoption writes the personas in added.
func CheckPersonaTargets(ctx context.Context, root string, added []string) error {
	selected, _, err := SelectPersonaDirs(ctx, root)
	if err != nil {
		return err
	}
	dirs := slices.Concat(selected, pluginPersonaDirs(root))
	names, err := canonicalAgentNames(ctx, root, added)
	if err != nil {
		return err
	}
	skills, err := pluginSkillProjections(ctx, root)
	if err != nil {
		return err
	}
	files := make([]projectionFile, 0, len(names)*(len(dirs)+1)+len(skills))
	for _, name := range names {
		files = append(files, projectionFile{rel: CanonicalAgentsRel + "/" + name})
		for _, dir := range dirs {
			files = append(files, projectionFile{rel: dir + "/" + name})
		}
	}
	return checkProjectionFiles(ctx, root, append(files, skills...))
}

// CheckSkillTargets runs the writer's refusals over the skill files a caller and
// CompileAgentSurfaces write once the skills Praetor ships named in added are installed, writing
// nothing: the SKILL.md and LICENSE under .agents/skills of each skill in added, and the copy of
// every such skill the repository carries or added names (SKILL.md and client LICENSE copies)
// in the skill directory of each agent client agent_clients selects (clientSkillProjections).
// Adoption runs it before its first write, so a symlinked .claude/skills fails adoption with
// nothing written. A name in added that is not a skill Praetor ships is refused.
func CheckSkillTargets(ctx context.Context, root string, added []string) error {
	dirs, _, err := SelectSkillDirs(ctx, root)
	if err != nil {
		return err
	}
	pending := PendingSources{Skills: make(map[string][]byte, len(added)), Licenses: make(map[string][]byte, len(added))}
	files := make([]projectionFile, 0, len(added)*2)
	for i := 0; i < len(added) && i < maxSkillProjections; i++ {
		pending.Skills[added[i]], pending.Licenses[added[i]] = nil, nil
		files = append(files, projectionFile{rel: CanonicalSkillRel(added[i])},
			projectionFile{rel: CanonicalSkillLicenseRel(added[i])})
	}
	copies, err := clientSkillProjections(ctx, root, dirs, pending)
	if err != nil {
		return err
	}
	stale, err := staleClientLicenses(ctx, root, dirs, pending)
	if err != nil {
		return err
	}
	return checkProjectionFiles(ctx, root, slices.Concat(files, copies, removalFiles(stale)))
}
