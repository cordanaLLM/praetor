package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// CanonicalAgentsRel holds the single source of truth for agent personas.
	CanonicalAgentsRel = ".agents/agents"
	// PluginManifestRel marks a repository that ships the personas as a plugin; only then
	// is PluginAgentsRel a projection target.
	PluginManifestRel = ".agents/plugins/praetor/plugin.json"
	// PluginAgentsRel is the plugin copy of the personas.
	PluginAgentsRel = ".agents/plugins/praetor/agents"
	// maxAgentProjections bounds the persona loops (HISS-02). It is CompileAgents' own cap, so
	// a persona set that verify and audit accept is one compile-context can write.
	maxAgentProjections = MaxAgentFiles
	// projectedDirPerm is the mode of tracked, reviewer-readable projection directories.
	projectedDirPerm os.FileMode = 0o755
)

// ErrAgentProjectionDrift reports a persona copy that no longer matches its source.
var ErrAgentProjectionDrift = errors.New("agent persona projection differs from its canonical source")

// errPersonaNotRegular refuses a persona entry, canonical or projected, that is a symlink or
// any other non-regular file. Skipping it instead let verify pass on a persona compile-context
// then refused to write.
var errPersonaNotRegular = errors.New("persona must be a regular file, never a symlink or directory")

// agentProjectionDirs lists every directory a persona is projected into under rootDir: the
// persona directory of each agent client the manifest selects, resolved by
// SelectPersonaDirs exactly as CompileAgents resolves it when writing, plus
// the plugin copy when the repository ships the plugin.
func agentProjectionDirs(ctx context.Context, rootDir string) ([]string, error) {
	dirs, _, err := SelectPersonaDirs(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	return append(dirs, pluginPersonaDirs(rootDir)...), nil
}

// shipsPlugin reports whether the repository ships the praetor plugin (PluginManifestRel).
func shipsPlugin(rootDir string) bool {
	return util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel)))
}

// pluginPersonaDirs is the plugin persona directory when the repository ships the plugin, and
// nothing otherwise.
func pluginPersonaDirs(rootDir string) []string {
	if !shipsPlugin(rootDir) {
		return nil
	}
	return []string{PluginAgentsRel}
}

// personaProjections reads every canonical persona once (readCanonicalAgent) and returns its
// copy in each of dirs, persona by persona. It writes nothing; the copies are checked and
// written by the caller, all of them checked before the first is written.
func personaProjections(ctx context.Context, rootDir string, dirs []string) ([]projectionFile, error) {
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	files := make([]projectionFile, 0, len(names)*len(dirs))
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		for j := 0; j < len(dirs); j++ {
			files = append(files, projectionFile{rel: dirs[j] + "/" + names[i], data: data})
		}
	}
	return files, nil
}

// notApplicablePersonaDirs lists the persona directories agent_clients leaves out. It is nil
// when the repository defines no canonical persona, since nothing is then left out.
func notApplicablePersonaDirs(ctx context.Context, rootDir string) ([]string, error) {
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	_, excluded, err := SelectPersonaDirs(ctx, rootDir)
	return excluded, err
}

// listCanonicalAgents returns the persona file names under .agents/agents, or nil when
// the directory is absent. A directory above the cap, a symlinked or misplaced component on the
// way to it, or a persona entry that is not a regular file, is an error: skipping any of them
// would verify less than compile-context writes, and compile-context and verify both list here.
func listCanonicalAgents(ctx context.Context, rootDir string) ([]string, error) {
	return listPersonaDir(ctx, rootDir, CanonicalAgentsRel)
}

// listPersonaDir returns the persona file names in the directory dir below rootDir, by name,
// through readPersonaDir. An absent directory holds none.
func listPersonaDir(ctx context.Context, rootDir, dir string) ([]string, error) {
	entries, present, err := readPersonaDir(ctx, rootDir, dir)
	if err != nil || !present {
		return nil, err
	}
	return personaNames(dir, entries)
}

// readPersonaDir lists the persona directory dir below rootDir through readConfinedDir, and
// reports whether it exists. A directory above the cap is an error.
func readPersonaDir(ctx context.Context, rootDir, dir string) ([]os.DirEntry, bool, error) {
	entries, err := readConfinedDir(ctx, rootDir, dir, maxAgentProjections)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > maxAgentProjections {
		return nil, false, fmt.Errorf("%s holds more than %d files", dir, maxAgentProjections)
	}
	return entries, true, nil
}

// agentDirectoryEntry is the file a one-directory-per-agent layout keeps its definition in,
// .agents/agents/<name>/AGENTS.md. compile-context projects no such file.
const agentDirectoryEntry = "AGENTS.md"

// AgentInventory is what .agents/agents holds, read by the walk compile-context projects from
// (readPersonaDir, personaNames), so a count taken from it is the count compile-context
// projects.
type AgentInventory struct {
	// Present reports whether .agents/agents exists.
	Present bool
	// Personas are the persona files compile-context projects, by name.
	Personas []string
	// Unprojected are the entries that can hold an agent definition but that compile-context
	// skips: every subdirectory, and every symlink or other non-regular entry without the .md
	// suffix (one with it is refused, errPersonaNotRegular), by name.
	Unprojected []UnprojectedAgentEntry
}

// UnprojectedAgentEntry names one entry of .agents/agents compile-context does not project.
// Name ends in a slash for a directory; Detail says what the entry holds.
type UnprojectedAgentEntry struct {
	Name   string
	Detail string
}

// ReadAgentInventory reads .agents/agents below rootDir once: the persona files
// compile-context projects (listCanonicalAgents reads the same ones, with the same refusals)
// and the entries it skips. An absent directory is an empty inventory with Present false.
func ReadAgentInventory(ctx context.Context, rootDir string) (AgentInventory, error) {
	entries, present, err := readPersonaDir(ctx, rootDir, CanonicalAgentsRel)
	if err != nil || !present {
		return AgentInventory{}, err
	}
	names, err := personaNames(CanonicalAgentsRel, entries)
	if err != nil {
		return AgentInventory{}, err
	}
	unprojected, err := unprojectedAgentEntries(ctx, rootDir, entries)
	if err != nil {
		return AgentInventory{}, err
	}
	return AgentInventory{Present: true, Personas: names, Unprojected: unprojected}, nil
}

// unprojectedAgentEntries returns the entries of .agents/agents personaNames skips that can
// hold an agent definition. A regular file without the .md suffix holds none and is left out.
func unprojectedAgentEntries(ctx context.Context, rootDir string, entries []os.DirEntry) ([]UnprojectedAgentEntry, error) {
	var out []UnprojectedAgentEntry
	for i := 0; i < len(entries) && i < maxAgentProjections; i++ {
		name := entries[i].Name()
		switch {
		case entries[i].IsDir():
			detail, err := agentDirectoryDetail(ctx, rootDir, name)
			if err != nil {
				return nil, err
			}
			out = append(out, UnprojectedAgentEntry{Name: name + "/", Detail: detail})
		case !entries[i].Type().IsRegular() && !strings.HasSuffix(name, ".md"):
			out = append(out, UnprojectedAgentEntry{Name: name, Detail: "not a regular file or directory"})
		}
	}
	return out, nil
}

// agentDirectoryDetail says whether the subdirectory name of .agents/agents holds an
// AGENTS.md, read through readConfinedDir so a symlink below it is refused, not followed.
func agentDirectoryDetail(ctx context.Context, rootDir, name string) (string, error) {
	rel := CanonicalAgentsRel + "/" + name
	entries, err := readConfinedDir(ctx, rootDir, rel, maxAgentProjections)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rel, err)
	}
	holdsConfig := false
	for i := 0; i < len(entries) && i <= maxAgentProjections; i++ {
		if entries[i].Name() == agentDirectoryEntry && entries[i].Type().IsRegular() {
			return "holds " + agentDirectoryEntry, nil
		}
		holdsConfig = holdsConfig || entries[i].Name() == "agent.json"
	}
	switch {
	case len(entries) > maxAgentProjections:
		return fmt.Sprintf("holds more than %d entries", maxAgentProjections), nil
	case holdsConfig:
		return "holds agent.json but no " + agentDirectoryEntry, nil
	}
	return "holds no " + agentDirectoryEntry, nil
}

// personaNames returns the .md entries of one persona directory, refusing any that is not a
// regular file. dir is the declared slash path the error names.
func personaNames(dir string, entries []os.DirEntry) ([]string, error) {
	names := make([]string, 0, len(entries))
	for i := 0; i < len(entries) && i < maxAgentProjections; i++ {
		if !strings.HasSuffix(entries[i].Name(), ".md") {
			continue
		}
		if !entries[i].Type().IsRegular() {
			return nil, fmt.Errorf("%s/%s: %w", dir, entries[i].Name(), errPersonaNotRegular)
		}
		names = append(names, entries[i].Name())
	}
	return names, nil
}

// readCanonicalAgent reads one persona source without following a symlink anywhere below
// rootDir (readConfinedText).
func readCanonicalAgent(ctx context.Context, rootDir, name string) ([]byte, error) {
	rel := CanonicalAgentsRel + "/" + name
	data, err := readConfinedText(ctx, rootDir, rel)
	if err != nil {
		return nil, fmt.Errorf("read persona %s: %w", rel, err)
	}
	return data, nil
}

// VerifyAgentProjections checks that every projection of every canonical persona exists
// and matches its source up to leading and trailing whitespace. It returns the number of
// verified copies. A persona directory agent_clients leaves out is neither required nor read.
func VerifyAgentProjections(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	dirs, err := agentProjectionDirs(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	verified := 0
	for i := 0; i < len(names); i++ {
		want, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return verified, err
		}
		for j := 0; j < len(dirs); j++ {
			if err := verifyProjection(ctx, rootDir, dirs[j]+"/"+names[i], want); err != nil {
				return verified, err
			}
			verified++
		}
	}
	// The loop above proves every canonical persona has a projection. On its own that is only
	// half a check: a file in a projection directory that matches no canonical persona is never
	// read, so it is never compared, so it can say anything and drift forever. Six such orphans
	// accumulated here, and one had gone stale holding an absolute developer path in a plugin
	// that ships to other machines.
	if err := rejectOrphanProjections(ctx, rootDir, dirs, names); err != nil {
		return verified, err
	}
	return verified, nil
}

// rejectOrphanProjections fails when a projection directory holds a persona the canonical set
// does not define.
func rejectOrphanProjections(ctx context.Context, rootDir string, dirs, names []string) error {
	canonical := make(map[string]bool, len(names))
	for i := 0; i < len(names); i++ {
		canonical[names[i]] = true
	}
	for j := 0; j < len(dirs); j++ {
		found, err := listPersonaDir(ctx, rootDir, dirs[j])
		if err != nil {
			return err
		}
		for k := 0; k < len(found); k++ {
			if canonical[found[k]] {
				continue
			}
			return fmt.Errorf("%s/%s projects no canonical persona; every file in a projection "+
				"directory is compiled output and must correspond to one in %s",
				dirs[j], found[k], CanonicalAgentsRel)
		}
	}
	return nil
}

// verifyProjection compares one projection with the canonical content, ignoring leading and
// trailing whitespace as VerifyCompiled does for the vendor files. rel is the declared slash
// path, which the error names the same way on every platform; readConfinedText maps it to the
// host path and follows no symlink below rootDir, as the writer does not.
func verifyProjection(ctx context.Context, rootDir, rel string, want []byte) error {
	got, err := readConfinedText(ctx, rootDir, rel)
	if err != nil {
		return fmt.Errorf("projection %s missing or unreadable (run 'praetorctl compile-context'): %w", rel, err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		return fmt.Errorf("%w: %s (run 'praetorctl compile-context' to regenerate it)", ErrAgentProjectionDrift, rel)
	}
	return nil
}
