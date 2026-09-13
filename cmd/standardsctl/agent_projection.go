package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// vendorAgentDirs mirrors the vendor targets written by compiler.CompileAgents; the
// compile-context round trip test keeps the two lists in step.
func vendorAgentDirs() []string {
	return []string{".claude/agents", ".codex/agents", ".github/agents", ".gemini/agents"}
}

// agentProjectionDirs lists every directory a persona is projected into under rootDir.
func agentProjectionDirs(rootDir string) []string {
	dirs := vendorAgentDirs()
	if util.FileExists(filepath.Join(rootDir, filepath.FromSlash(pluginManifestRel))) {
		dirs = append(dirs, pluginAgentsRel)
	}
	return dirs
}

// listCanonicalAgents returns the persona file names under .agents/agents, or nil when
// the directory is absent.
func listCanonicalAgents(rootDir string) ([]string, error) {
	dir := filepath.Join(rootDir, filepath.FromSlash(canonicalAgentsRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
// and is byte-identical to its source. It returns the number of verified copies.
func verifyAgentProjections(rootDir string) (int, error) {
	names, err := listCanonicalAgents(rootDir)
	if err != nil {
		return 0, err
	}
	dirs := agentProjectionDirs(rootDir)
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
	return verified, nil
}

// verifyProjection compares one projection with the canonical content.
func verifyProjection(rootDir, rel string, want []byte) error {
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return err
	}
	// #nosec G304 -- path was confined to the repository root by ConfinePath.
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("projection %s missing or unreadable (run 'praetorctl compile-context'): %w", rel, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s (run 'praetorctl compile-context' to regenerate it)", ErrAgentProjectionDrift, rel)
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
