package compiler

import (
	"context"
	"errors"
	"fmt"
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

// CompileAgents scans canonical .agents/agents/*.md and projects them to vendor agent directories.
func CompileAgents(ctx context.Context, agentsSrcDir, targetDir string) ([]AgentFile, error) {
	if ctx == nil {
		return nil, fmt.Errorf("compile-agents: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compile-agents cancelled: %w", err)
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
		agentFiles, pErr := projectAgentToVendors(ctx, srcPath, entry.Name(), targetDir)
		if pErr != nil {
			return nil, pErr
		}
		results = append(results, agentFiles...)
	}

	return results, nil
}

func projectAgentToVendors(ctx context.Context, srcPath, filename, targetDir string) ([]AgentFile, error) {
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
	targets := []struct {
		vendorRel string
	}{
		{".claude/agents/" + filename},
		{".codex/agents/" + filename},
		{".github/agents/" + filename},
		{".gemini/agents/" + filename},
	}

	files := make([]AgentFile, 0, len(targets))
	for _, t := range targets {
		dstPath := filepath.Join(targetDir, t.vendorRel)
		if err := writeVendorAgent(ctx, dstPath, content); err != nil {
			return nil, err
		}
		files = append(files, AgentFile{
			Name:         agentName,
			SourcePath:   srcPath,
			VendorTarget: t.vendorRel,
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
