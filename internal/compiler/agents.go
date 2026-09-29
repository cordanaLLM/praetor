package compiler

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

const (
	MaxAgentFiles       = 50
	DefaultAgentTimeout = 10 * time.Second
)

// AgentFile represents a compiled agent persona across vendor tools.
type AgentFile struct {
	Name         string
	SourcePath   string
	VendorTarget string
	Content      string
}

// CompileAgents projects the canonical personas under rootDir/.agents/agents to the persona
// directory of every agent client agent_clients in the manifest at rootDir selects
// (SelectPersonaDirs). A directory the selection leaves out is neither written nor removed.
// It lists, reads and writes through the same confined walk compile-context --verify reads
// through, so a symlink below rootDir that verify refuses is refused here before anything is
// written.
func CompileAgents(ctx context.Context, rootDir string) ([]AgentFile, error) {
	files, err := planAgents(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	if err := writeProjectionFiles(ctx, rootDir, files); err != nil {
		return nil, fmt.Errorf("compile-agents: %w", err)
	}
	return agentFiles(rootDir, files), nil
}

// PlanAgents returns the persona copies CompileAgents would write for rootDir as it is now,
// after the same refusals, and writes nothing. A caller that reports on the copies reads what
// each target holds before CompileAgents replaces it.
func PlanAgents(ctx context.Context, rootDir string) ([]AgentFile, error) {
	files, err := planAgents(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	return agentFiles(rootDir, files), nil
}

// planAgents selects the persona directories, renders every copy and runs the writer's
// refusals over all of them, the part CompileAgents and PlanAgents share.
func planAgents(ctx context.Context, rootDir string) ([]projectionFile, error) {
	if ctx == nil {
		return nil, fmt.Errorf("compile-agents: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compile-agents cancelled: %w", err)
	}
	dirs, _, err := SelectPersonaDirs(ctx, rootDir)
	if err != nil {
		return nil, fmt.Errorf("compile-agents: %w", err)
	}
	files, err := personaProjections(ctx, rootDir, dirs)
	if err != nil {
		return nil, fmt.Errorf("compile-agents: %w", err)
	}
	if err := checkProjectionFiles(ctx, rootDir, files); err != nil {
		return nil, fmt.Errorf("compile-agents: %w", err)
	}
	return files, nil
}

// agentFiles reports each written or planned persona copy. Every copy's name is its persona's
// file name.
func agentFiles(rootDir string, files []projectionFile) []AgentFile {
	results := make([]AgentFile, 0, len(files))
	for _, file := range files {
		name := path.Base(file.rel)
		results = append(results, AgentFile{
			Name:         strings.TrimSuffix(name, ".md"),
			SourcePath:   filepath.Join(rootDir, filepath.FromSlash(CanonicalAgentsRel), name),
			VendorTarget: file.rel,
			Content:      string(file.data),
		})
	}
	return results
}

// SelectPersonaDirs returns the persona directories agent_clients in the manifest at root keeps
// and the ones it leaves out, in registry order (agentcontext.PersonaDirs). A root without a
// manifest, or a manifest without the key, keeps every directory. compile-context, its
// --verify, the audit and adoption all resolve the selection here, so they agree on which
// persona copies exist.
func SelectPersonaDirs(ctx context.Context, root string) (selected, excluded []string, err error) {
	clients, err := declaredAgentClients(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return agentcontext.PersonaDirs(clients)
}

func projectionPath(root, relative string) (string, error) {
	// Vendor targets are declared as slash paths (".cursor/rules/hiss-invariants.mdc"),
	// so cleanliness is a slash-path property. filepath.Clean returns backslashes on
	// Windows and would never equal the declared value, which rejected every
	// projection and left compile-context unable to write on that platform.
	if !filepath.IsLocal(relative) || path.Clean(relative) != relative || relative == "." {
		return "", errors.New("compiled output requires a clean relative file path")
	}
	return filepath.Join(root, relative), nil
}
