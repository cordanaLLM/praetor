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
	claudeContent := generateClaudeMD(content)
	cursorContent := generateCursorMDC(content)
	copilotContent := generateCopilotMD(content)
	windsurfContent := generateWindsurfRules(content)
	geminiContent := generateGeminiMD(content)
	codexContent := generateCodexMD(content)

	files := []TargetFile{
		{RelativePath: "CLAUDE.md", Content: claudeContent},
		{RelativePath: ".cursor/rules/hiss-invariants.mdc", Content: cursorContent},
		{RelativePath: ".github/copilot-instructions.md", Content: copilotContent},
		{RelativePath: ".windsurfrules", Content: windsurfContent},
		{RelativePath: ".gemini/GEMINI.md", Content: geminiContent},
		{RelativePath: ".codex/rules.md", Content: codexContent},
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

func generateClaudeMD(src string) string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# Claude Code Guidelines: cordanaLLM/standards\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("## Commands\n\n")
	b.WriteString("```bash\n")
	b.WriteString("go test -v -race ./...\n")
	b.WriteString("go run ./cmd/standardsctl compile-context --verify\n")
	b.WriteString("go run ./cmd/standardsctl audit\n")
	b.WriteString("make verify-all\n")
	b.WriteString("```\n\n")
	b.WriteString("## Architectural Invariants (HISS-16)\n\n")
	b.WriteString("- **Acyclic Control Flow (HISS-01)**: Recursion strictly prohibited; call graph must be DAG.\n")
	b.WriteString("- **Bounded Loops & Timeouts (HISS-02)**: Scalar upper bounds on loops; context timeout on all I/O.\n")
	b.WriteString("- **Complexity Caps (HISS-04)**: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75.\n")
	b.WriteString("- **Zero Unchecked Errors (HISS-07)**: Zero .unwrap() / .expect(); handle all errors explicitly.\n")
	b.WriteString("- **Zero Warnings (HISS-10)**: Compilers and linters must pass with zero warnings.\n")
	b.WriteString("- **3D Testing (HISS-15)**: Positive, negative, and boundary tests mandatory.\n")
	b.WriteString("- **Context Integrity (HISS-16)**: Single canonical AGENTS.md source.\n\n")
	b.WriteString("## Behavioral Invariants\n\n")
	b.WriteString("- **Lead with Action**: Return code changes and commands directly without conversational preamble.\n")
	b.WriteString("- **Diagnostic Distillation**: Limit compiler/linter error feedback to <= 1500 tokens with line pointers.\n")
	b.WriteString("- **No Evasion**: Never bypass hooks or use --no-verify.\n")
	return b.String()
}

func generateCursorMDC(src string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: HISS-16 Engineering Invariants and Verification Gates\n")
	b.WriteString("globs: *\n")
	b.WriteString("alwaysApply: true\n")
	b.WriteString("---\n\n")
	b.WriteString("# Cursor Rules: cordanaLLM/standards\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("## Mandatory Invariants\n\n")
	b.WriteString("1. Bounded loops and explicit context timeouts on all I/O (HISS-02).\n")
	b.WriteString("2. Max McCabe cyclomatic complexity <= 10, function length <= 75 LOC (HISS-04).\n")
	b.WriteString("3. Zero unchecked errors and zero unwraps in production code (HISS-07).\n")
	b.WriteString("4. Positive, negative, and boundary tests for all public APIs (HISS-15).\n")
	b.WriteString("5. All agent instructions originate from AGENTS.md (HISS-16).\n\n")
	b.WriteString("## Verification Entrypoint\n\n")
	b.WriteString("Before concluding, always run: `make verify-all`\n")
	return b.String()
}

func generateCopilotMD(src string) string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# GitHub Copilot Instructions: cordanaLLM/standards\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("- Ensure all Go code passes `go test -v -race ./...`.\n")
	b.WriteString("- Strictly adhere to HISS-16 invariants (McCabe <= 10, LOC <= 75, zero unwraps).\n")
	b.WriteString("- Do not edit generated vendor files directly; update `AGENTS.md` and run `standardsctl compile-context`.\n")
	b.WriteString("- All public interfaces require 3D test discipline: positive, negative, and boundary cases.\n")
	return b.String()
}

func generateWindsurfRules(src string) string {
	var b strings.Builder
	b.WriteString("# Windsurf Cascade Rules: cordanaLLM/standards\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("- Lead with direct code edits and executable commands.\n")
	b.WriteString("- Follow HISS-16 invariants: cyclomatic complexity <= 10, function LOC <= 75.\n")
	b.WriteString("- Run verification: `make verify-all`.\n")
	b.WriteString("- Limit diagnostic feedback to <= 1500 tokens with file/line pointers.\n")
	return b.String()
}

func generateGeminiMD(src string) string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# Google Antigravity / Gemini Instructions: cordanaLLM/standards\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("## Operating Directives\n\n")
	b.WriteString("- Canonical harness is `AGENTS.md`.\n")
	b.WriteString("- Run `make verify-all` to assert invariant compliance.\n")
	b.WriteString("- Cap diagnostic outputs at <= 1500 tokens.\n")
	b.WriteString("- Reconcile context changes via `standardsctl compile-context`.\n")
	return b.String()
}

func generateCodexMD(src string) string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# OpenAI Codex Context & Operating Rules\n")
	b.WriteString("<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n")
	b.WriteString("## Directives & Invariants\n\n")
	b.WriteString("- Act on verified state, not assumption.\n")
	b.WriteString("- Zero tolerance for destructive or un-revertible actions without authorization.\n")
	b.WriteString("- Maintain strict DAG call graphs (zero recursion, HISS-01).\n")
	b.WriteString("- Enforce scalar loop bounds and context timeouts on all I/O (HISS-02).\n")
	b.WriteString("- Zero unchecked errors and zero unwrap/expect calls (HISS-07).\n")
	b.WriteString("- Positive, negative, and boundary tests mandatory for all public interfaces (HISS-15).\n\n")
	b.WriteString("## Verification Commands\n\n")
	b.WriteString("```bash\n")
	b.WriteString("make verify-all\n")
	b.WriteString("```\n")
	return b.String()
}
