package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// canonicalAgentsRel holds the single source of truth for agent personas.
	canonicalAgentsRel = ".agents/agents"
	// pluginManifestRel marks a repository that ships the personas as a plugin; only then
	// is pluginAgentsRel a projection target.
	pluginManifestRel = ".agents/plugins/praetor/plugin.json"
	// pluginAgentsRel is the plugin copy of the personas.
	pluginAgentsRel = ".agents/plugins/praetor/agents"
	// maxAgentProjections bounds the persona loops (HISS-02).
	maxAgentProjections = 64
	// projectedDirPerm and projectedFilePerm are the modes of tracked, reviewer-readable
	// projections.
	projectedDirPerm  os.FileMode = 0o755
	projectedFilePerm os.FileMode = 0o644
)

// ErrAgentProjectionDrift reports a persona copy that no longer matches its source.
var ErrAgentProjectionDrift = errors.New("agent persona projection differs from its canonical source")

// agentProjectionDirs lists every directory a persona is projected into under rootDir: the
// persona directory of each agent client the manifest selects, resolved by
// compiler.SelectPersonaDirs exactly as compiler.CompileAgents resolves it when writing, plus
// the plugin copy when the repository ships the plugin.
func agentProjectionDirs(ctx context.Context, rootDir string) ([]string, error) {
	dirs, _, err := compiler.SelectPersonaDirs(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	if util.FileExists(filepath.Join(rootDir, filepath.FromSlash(pluginManifestRel))) {
		dirs = append(dirs, pluginAgentsRel)
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
	_, excluded, err := compiler.SelectPersonaDirs(ctx, rootDir)
	return excluded, err
}

// listCanonicalAgents returns the persona file names under .agents/agents, or nil when
// the directory is absent.
func listCanonicalAgents(rootDir string) ([]string, error) {
	dir := filepath.Join(rootDir, filepath.FromSlash(canonicalAgentsRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if util.DirectoryAbsent(dir, err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for i := 0; i < len(entries) && i < maxAgentProjections; i++ {
		if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".md") {
			names = append(names, entries[i].Name())
		}
	}
	return names, nil
}

// readCanonicalAgent reads one persona source confined to the canonical directory.
func readCanonicalAgent(rootDir, name string) ([]byte, error) {
	path, err := util.ConfinePath(rootDir, filepath.Join(filepath.FromSlash(canonicalAgentsRel), name))
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path was confined to <root>/.agents/agents by ConfinePath.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read persona %s: %w", path, err)
	}
	return data, nil
}

// verifyAgentProjections checks that every projection of every canonical persona exists
// and is byte-identical to its source. It returns the number of verified copies. A persona
// directory agent_clients leaves out is neither required nor read.
func verifyAgentProjections(ctx context.Context, rootDir string) (int, error) {
	names, err := listCanonicalAgents(rootDir)
	if err != nil {
		return 0, err
	}
	dirs, err := agentProjectionDirs(ctx, rootDir)
	if err != nil {
		return 0, err
	}
	verified := 0
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		want, err := readCanonicalAgent(rootDir, names[i])
		if err != nil {
			return verified, err
		}
		for j := 0; j < len(dirs); j++ {
			rel := filepath.Join(filepath.FromSlash(dirs[j]), names[i])
			if err := verifyProjection(rootDir, rel, want); err != nil {
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
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		canonical[names[i]] = true
	}
	for j := 0; j < len(dirs) && j < maxAgentProjections; j++ {
		found, err := listProjectedAgents(rootDir, dirs[j])
		if err != nil {
			return err
		}
		for k := 0; k < len(found) && k < maxAgentProjections; k++ {
			if canonical[found[k]] {
				continue
			}
			return fmt.Errorf("%s/%s projects no canonical persona; every file in a projection "+
				"directory is compiled output and must correspond to one in %s",
				dirs[j], found[k], canonicalAgentsRel)
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
	var names []string
	for i := 0; i < len(entries) && i < maxAgentProjections; i++ {
		if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".md") {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// verifyProjection compares one projection with the canonical content.
func verifyProjection(rootDir, rel string, want []byte) error {
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return err
	}
	// rel is the host path the file system needs; the error names the projection, which is a
	// declared identity and reads the same on every platform. Reporting rel directly printed
	// ".github\agents\x.md" on Windows. ToSlash is a no-op on POSIX.
	display := filepath.ToSlash(rel)
	// #nosec G304 -- path was confined to the repository root by ConfinePath.
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("projection %s missing or unreadable (run 'praetorctl compile-context'): %w", display, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s (run 'praetorctl compile-context' to regenerate it)", ErrAgentProjectionDrift, display)
	}
	return nil
}

// projectPluginAgents copies the canonical personas into the plugin agents directory when
// the repository ships the praetor plugin. It returns the number of files written.
func projectPluginAgents(rootDir string) (int, error) {
	if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(pluginManifestRel))) {
		return 0, nil
	}
	names, err := listCanonicalAgents(rootDir)
	if err != nil {
		return 0, err
	}
	targetDir, err := util.ConfinePath(rootDir, filepath.FromSlash(pluginAgentsRel))
	if err != nil {
		return 0, err
	}
	if err := util.MkdirSecure(targetDir, projectedDirPerm); err != nil {
		return 0, err
	}
	written := 0
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(rootDir, names[i])
		if err != nil {
			return written, err
		}
		if err := util.WriteFileSecure(filepath.Join(targetDir, names[i]), data, projectedFilePerm); err != nil {
			return written, fmt.Errorf("write plugin persona %s: %w", names[i], err)
		}
		written++
	}
	return written, nil
}
