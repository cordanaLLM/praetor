package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxAgentFiles   = 50
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

	entries, err := os.ReadDir(agentsSrcDir)
	if err != nil {
		if os.IsNotExist(err) {
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
		agentFiles, pErr := projectAgentToVendors(srcPath, entry.Name(), targetDir)
		if pErr != nil {
			return nil, pErr
		}
		results = append(results, agentFiles...)
	}

	return results, nil
}

func projectAgentToVendors(srcPath, filename, targetDir string) ([]AgentFile, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("read agent file %s: %w", srcPath, err)
	}

	agentName := strings.TrimSuffix(filename, ".md")
	content := string(data)

	targets := []struct {
		vendorRel string
	}{
		{filepath.Join(".claude", "agents", filename)},
		{filepath.Join(".codex", "agents", filename)},
		{filepath.Join(".github", "agents", filename)},
		{filepath.Join(".gemini", "agents", filename)},
	}

	files := make([]AgentFile, 0, len(targets))
	for _, t := range targets {
		dstPath := filepath.Join(targetDir, t.vendorRel)
		if err := writeVendorAgent(dstPath, content); err != nil {
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

func writeVendorAgent(dstPath, content string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return fmt.Errorf("mkdir vendor agent dir: %w", err)
	}
	if err := os.WriteFile(dstPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write vendor agent %s: %w", dstPath, err)
	}
	return nil
}
