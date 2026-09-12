package compiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxLineBudget = 300

// TargetFile describes a generated vendor-specific agent instructions file.
type TargetFile struct {
	RelativePath string
	Content      string
	LineCount    int
}

// CompileResult contains the output of context transpilation.
type CompileResult struct {
	SourcePath string
	Files      []TargetFile
}

// Transpiler compiles canonical AGENTS.md into vendor-native agent configurations.
type Transpiler struct {
	MaxLines int
}

// NewTranspiler creates a Transpiler with standard budget constraints.
func NewTranspiler() *Transpiler {
	return &Transpiler{
		MaxLines: MaxLineBudget,
	}
}

// Compile reads the canonical AGENTS.md and synthesizes vendor-specific files.
func (t *Transpiler) Compile(agentsMdPath string) (*CompileResult, error) {
	contentBytes, err := os.ReadFile(agentsMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source %s: %w", agentsMdPath, err)
	}
	res, err := t.CompileContent(string(contentBytes))
	if err != nil {
		return nil, err
	}
	res.SourcePath = agentsMdPath
	return res, nil
}

// CompileContent synthesizes vendor-specific files directly from in-memory markdown content.
func (t *Transpiler) CompileContent(content string) (*CompileResult, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("canonical AGENTS.md content is empty")
	}
	canonical := generatedHeader + content
	files := []TargetFile{
		{RelativePath: "CLAUDE.md", Content: canonical},
		{RelativePath: ".cursor/rules/hiss-invariants.mdc", Content: cursorFrontmatter + canonical},
		{RelativePath: ".github/copilot-instructions.md", Content: canonical},
		{RelativePath: ".windsurfrules", Content: canonical},
		{RelativePath: ".gemini/GEMINI.md", Content: canonical},
		{RelativePath: ".codex/rules.md", Content: canonical},
	}

	for i := range files {
		lines := countLines(files[i].Content)
		files[i].LineCount = lines
		if lines > t.MaxLines {
			return nil, fmt.Errorf("target file %s exceeds max line budget (%d > %d)", files[i].RelativePath, lines, t.MaxLines)
		}
	}

	return &CompileResult{
		SourcePath: "AGENTS.md",
		Files:      files,
	}, nil
}

// WriteOutputs writes compiled files to targetDir.
func (t *Transpiler) WriteOutputs(result *CompileResult, targetDir string) error {
	for _, f := range result.Files {
		fullPath := filepath.Join(targetDir, f.RelativePath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("failed to create dir for %s: %w", fullPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", fullPath, err)
		}
	}
	return nil
}

// Verify checks that existing target files match compiled output without modification.
func (t *Transpiler) Verify(agentsMdPath string, targetDir string) error {
	res, err := t.Compile(agentsMdPath)
	if err != nil {
		return err
	}

	for _, f := range res.Files {
		fullPath := filepath.Join(targetDir, f.RelativePath)
		existing, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("target %s missing or unreadable: %w", f.RelativePath, err)
		}
		if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace([]byte(f.Content))) {
			return fmt.Errorf("target %s is out of sync with %s; run 'standardsctl compile-context' to reconcile", f.RelativePath, agentsMdPath)
		}
	}
	return nil
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// Vendor wrappers carry metadata only. Policy must come entirely from AGENTS.md.
const generatedHeader = "<!-- markdownlint-disable MD013 -->\n" +
	"<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"

const cursorFrontmatter = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n"
