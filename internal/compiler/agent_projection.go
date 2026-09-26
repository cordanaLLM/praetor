package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		dirs = append(dirs, PluginAgentsRel)
	}
	return dirs, nil
}

// notApplicablePersonaDirs lists the persona directories agent_clients leaves out. It is nil
// when the repository defines no canonical persona, since nothing is then left out.
func notApplicablePersonaDirs(ctx context.Context, rootDir string) ([]string, error) {
	names, err := listCanonicalAgents(rootDir)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	_, excluded, err := SelectPersonaDirs(ctx, rootDir)
	return excluded, err
}

// listCanonicalAgents returns the persona file names under .agents/agents, or nil when
// the directory is absent. A directory above the cap, or a persona entry that is not a
// regular file, is an error: skipping either would verify less than compile-context writes.
func listCanonicalAgents(rootDir string) ([]string, error) {
	dir := filepath.Join(rootDir, filepath.FromSlash(CanonicalAgentsRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if util.DirectoryAbsent(dir, err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > maxAgentProjections {
		return nil, fmt.Errorf("%s holds more than %d files", CanonicalAgentsRel, maxAgentProjections)
	}
	return personaNames(CanonicalAgentsRel, entries)
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
	names, err := listCanonicalAgents(rootDir)
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
	if err := rejectOrphanProjections(rootDir, dirs, names); err != nil {
		return verified, err
	}
	return verified, nil
}

// rejectOrphanProjections fails when a projection directory holds a persona the canonical set
// does not define.
func rejectOrphanProjections(rootDir string, dirs, names []string) error {
	canonical := make(map[string]bool, len(names))
	for i := 0; i < len(names); i++ {
		canonical[names[i]] = true
	}
	for j := 0; j < len(dirs); j++ {
		found, err := listProjectedAgents(rootDir, dirs[j])
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

// listProjectedAgents returns the persona files present in one projection directory.
func listProjectedAgents(rootDir, dir string) (_ []string, err error) {
	path, err := util.ConfinePath(rootDir, filepath.FromSlash(dir))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > maxAgentProjections {
		return nil, fmt.Errorf("%s holds more than %d files", dir, maxAgentProjections)
	}
	names, err := personaNames(dir, entries)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
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

// ProjectPluginAgents copies the canonical personas into the plugin agents directory when
// the repository ships the praetor plugin. It returns the number of files written.
func ProjectPluginAgents(ctx context.Context, rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(PluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalAgents(rootDir)
	if err != nil {
		return 0, err
	}
	targetDir, err := util.ConfinePath(rootDir, filepath.FromSlash(PluginAgentsRel))
	if err != nil {
		return 0, err
	}
	if err := util.MkdirSecure(targetDir, projectedDirPerm); err != nil {
		return 0, err
	}
	written := 0
	for i := 0; i < len(names); i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return written, err
		}
		if err := writeVendorAgent(ctx, filepath.Join(targetDir, names[i]), string(data)); err != nil {
			return written, fmt.Errorf("write plugin persona %s: %w", names[i], err)
		}
		written++
	}
	return written, nil
}
