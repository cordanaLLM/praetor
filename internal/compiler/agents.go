package compiler

import (
	"context"
	"errors"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// CompileAgents scans canonical .agents/agents/*.md and projects them to the persona directory
// of every agent client agent_clients in the manifest at targetDir selects (SelectPersonaDirs).
// A directory the selection leaves out is neither written nor removed.
func CompileAgents(ctx context.Context, agentsSrcDir, targetDir string) ([]AgentFile, error) {
	if ctx == nil {
		return nil, fmt.Errorf("compile-agents: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compile-agents cancelled: %w", err)
	}
	dirs, _, err := SelectPersonaDirs(ctx, targetDir)
	if err != nil {
		return nil, fmt.Errorf("compile-agents: %w", err)
	}

	entries, err := readAgentDirectory(ctx, agentsSrcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents dir %s: %w", agentsSrcDir, err)
	}

	results := make([]AgentFile, 0)
	count := 0
	for _, entry := range entries {
		if count >= MaxAgentFiles {
			break
		}
		count++

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		srcPath := filepath.Join(agentsSrcDir, entry.Name())
		agentFiles, pErr := projectAgentToVendors(ctx, srcPath, entry.Name(), targetDir, dirs)
		if pErr != nil {
			return nil, pErr
		}
		results = append(results, agentFiles...)
	}

	return results, nil
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

func projectAgentToVendors(ctx context.Context, srcPath, filename, targetDir string, dirs []string) ([]AgentFile, error) {
	data, err := contextopt.ReadSnapshot(ctx, srcPath)
	if err != nil {
		return nil, fmt.Errorf("read agent file %s: %w", srcPath, err)
	}

	agentName := strings.TrimSuffix(filename, ".md")
	content := string(data)

	// Each vendorRel is a declared identity, compared and reported as such, so it is spelled
	// with slashes like every other vendor target. filepath.Join made it
	// ".github\agents\x.md" on Windows, which surfaced in drift errors and only passed
	// projectionPath by accident. The disk write below joins it onto targetDir with
	// filepath.Join, which normalises the separator for the host.
	files := make([]AgentFile, 0, len(dirs))
	for _, dir := range dirs {
		vendorRel := dir + "/" + filename
		dstPath := filepath.Join(targetDir, filepath.FromSlash(vendorRel))
		if err := writeVendorAgent(ctx, dstPath, content); err != nil {
			return nil, err
		}
		files = append(files, AgentFile{
			Name:         agentName,
			SourcePath:   srcPath,
			VendorTarget: vendorRel,
			Content:      content,
		})
	}
	return files, nil
}

func writeVendorAgent(ctx context.Context, path, content string) error {
	return contextopt.WriteSnapshot(ctx, path, []byte(content), 0o644)
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

func readAgentDirectory(ctx context.Context, path string) (entries []os.DirEntry, err error) {
	root, err := contextopt.OpenDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err = directory.ReadDir(MaxAgentFiles + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if len(entries) > MaxAgentFiles {
		return nil, errors.New("agent directory exceeds 50 entries")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}
