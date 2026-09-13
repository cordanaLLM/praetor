package agentcontext

import (
	"fmt"
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

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// Vendor wrappers carry metadata only. Policy must come entirely from AGENTS.md.
const generatedHeader = "<!-- markdownlint-disable MD013 -->\n" +
	"<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"

const cursorFrontmatter = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n"
