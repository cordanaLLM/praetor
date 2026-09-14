package compiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanallm/praetor/internal/config"
)

// MaxLineBudget bounds every compiled vendor target (HISS-16: context files
// must stay small enough to be read whole by the agent).
const MaxLineBudget = 300

// DefaultRepoName is used when no manifest names the repository.
const DefaultRepoName = "cordanaLLM/praetor"

// Vendor identifies one compiled target. AGENTS.md may carry a per-vendor
// section (`## <Section>`) whose body is copied verbatim into that target —
// this is how repository-specific guidance (skills, hooks, layout notes)
// survives transpilation instead of being replaced by the generic harness.
type Vendor struct {
	// Name is the human label used in generated headings.
	Name string
	// Section is the AGENTS.md H2 heading that feeds this target (case-insensitive).
	Section string
	// Path is the target file, relative to the repository root.
	Path string
}

// Vendors lists the compiled targets in output order.
var Vendors = []Vendor{
	{Name: "Claude Code", Section: "Claude Code", Path: "CLAUDE.md"},
	{Name: "Cursor", Section: "Cursor", Path: ".cursor/rules/hiss-invariants.mdc"},
	{Name: "GitHub Copilot", Section: "GitHub Copilot", Path: ".github/copilot-instructions.md"},
	{Name: "Windsurf", Section: "Windsurf", Path: ".windsurfrules"},
	{Name: "Gemini", Section: "Gemini", Path: ".gemini/GEMINI.md"},
	{Name: "Codex", Section: "Codex", Path: ".codex/rules.md"},
}

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
	// RepoName is "owner/name"; it parametrises every generated heading.
	RepoName string
	// TestCommand is the fast local test invocation shown in Commands blocks.
	TestCommand string
	// VerifyCommand is the universal verification entrypoint.
	VerifyCommand string
}

// NewTranspiler creates a Transpiler with standard budget constraints and
// default commands.
func NewTranspiler() *Transpiler {
	return &Transpiler{
		MaxLines:      MaxLineBudget,
		RepoName:      DefaultRepoName,
		TestCommand:   "go test -v -race ./...",
		VerifyCommand: "make verify-all",
	}
}

// WithRepoName sets the repository label used in generated headings.
func (t *Transpiler) WithRepoName(name string) *Transpiler {
	if strings.TrimSpace(name) != "" {
		t.RepoName = strings.TrimSpace(name)
	}
	return t
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
	sections := VendorSections(content)
	files := make([]TargetFile, 0, len(Vendors))
	for _, v := range Vendors {
		body := t.generate(v, sections[strings.ToLower(v.Section)])
		lines := countLines(body)
		if lines > t.MaxLines {
			return nil, fmt.Errorf("target file %s exceeds max line budget (%d > %d); trim the `## %s` section of AGENTS.md", v.Path, lines, t.MaxLines, v.Section)
		}
		files = append(files, TargetFile{RelativePath: v.Path, Content: body, LineCount: lines})
	}
	return &CompileResult{SourcePath: "AGENTS.md", Files: files}, nil
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

// h2RE matches an ATX level-2 heading and captures its text.
var h2RE = regexp.MustCompile(`^##\s+(.+?)\s*#*\s*$`)

// VendorSections extracts every `## <Heading>` block of src keyed by the
// lower-cased heading text. A block runs until the next H1/H2 heading; fenced
// code blocks are respected so a `##` inside a fence does not split a section.
func VendorSections(src string) map[string]string {
	out := make(map[string]string)
	var (
		key   string
		buf   strings.Builder
		fence bool
	)
	flush := func() {
		if key != "" {
			out[key] = strings.TrimSpace(buf.String())
		}
		buf.Reset()
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = !fence
		}
		if !fence {
			if m := h2RE.FindStringSubmatch(trimmed); m != nil {
				flush()
				key = strings.ToLower(m[1])
				continue
			}
			if strings.HasPrefix(trimmed, "# ") {
				flush()
				key = ""
				continue
			}
		}
		if key != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	return out
}

func (t *Transpiler) generate(v Vendor, section string) string {
	var body string
	switch v.Path {
	case "CLAUDE.md":
		body = t.generateClaudeMD()
	case ".cursor/rules/hiss-invariants.mdc":
		body = t.generateCursorMDC()
	case ".github/copilot-instructions.md":
		body = t.generateCopilotMD()
	case ".windsurfrules":
		body = t.generateWindsurfRules()
	case ".gemini/GEMINI.md":
		body = t.generateGeminiMD()
	default:
		body = t.generateCodexMD()
	}
	if section == "" {
		return body
	}
	return body + "\n## Repository-specific guidance\n<!-- Copied verbatim from the `## " + v.Section + "` section of AGENTS.md. -->\n\n" + section + "\n"
}

func compiledNotice() string {
	return "<!-- Compiled automatically by standardsctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"
}

func (t *Transpiler) generateClaudeMD() string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# Claude Code Guidelines: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("Read `AGENTS.md` first — it is the canonical operating harness; this file is compiled from it.\n\n")
	b.WriteString("## Commands\n\n")
	b.WriteString("```bash\n")
	b.WriteString(t.TestCommand + "\n")
	b.WriteString("standardsctl compile-context --verify\n")
	b.WriteString("standardsctl audit\n")
	b.WriteString(t.VerifyCommand + "\n")
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

func (t *Transpiler) generateCursorMDC() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: HISS-16 Engineering Invariants and Verification Gates\n")
	b.WriteString("globs: *\n")
	b.WriteString("alwaysApply: true\n")
	b.WriteString("---\n\n")
	b.WriteString("# Cursor Rules: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("## Mandatory Invariants\n\n")
	b.WriteString("1. Bounded loops and explicit context timeouts on all I/O (HISS-02).\n")
	b.WriteString("2. Max McCabe cyclomatic complexity <= 10, function length <= 75 LOC (HISS-04).\n")
	b.WriteString("3. Zero unchecked errors and zero unwraps in production code (HISS-07).\n")
	b.WriteString("4. Positive, negative, and boundary tests for all public APIs (HISS-15).\n")
	b.WriteString("5. All agent instructions originate from AGENTS.md (HISS-16).\n\n")
	b.WriteString("## Verification Entrypoint\n\n")
	b.WriteString("Before concluding, always run: `" + t.VerifyCommand + "`\n")
	return b.String()
}

func (t *Transpiler) generateCopilotMD() string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# GitHub Copilot Instructions: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("- Ensure all Go code passes `" + t.TestCommand + "`.\n")
	b.WriteString("- Strictly adhere to HISS-16 invariants (McCabe <= 10, LOC <= 75, zero unwraps).\n")
	b.WriteString("- Do not edit generated vendor files directly; update `AGENTS.md` and run `standardsctl compile-context`.\n")
	b.WriteString("- All public interfaces require 3D test discipline: positive, negative, and boundary cases.\n")
	return b.String()
}

func (t *Transpiler) generateWindsurfRules() string {
	var b strings.Builder
	b.WriteString("# Windsurf Cascade Rules: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("- Lead with direct code edits and executable commands.\n")
	b.WriteString("- Follow HISS-16 invariants: cyclomatic complexity <= 10, function LOC <= 75.\n")
	b.WriteString("- Run verification: `" + t.VerifyCommand + "`.\n")
	b.WriteString("- Limit diagnostic feedback to <= 1500 tokens with file/line pointers.\n")
	return b.String()
}

func (t *Transpiler) generateGeminiMD() string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# Google Antigravity / Gemini Instructions: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("## Operating Directives\n\n")
	b.WriteString("- Canonical harness is `AGENTS.md`.\n")
	b.WriteString("- Run `" + t.VerifyCommand + "` to assert invariant compliance.\n")
	b.WriteString("- Cap diagnostic outputs at <= 1500 tokens.\n")
	b.WriteString("- Reconcile context changes via `standardsctl compile-context`.\n")
	return b.String()
}

func (t *Transpiler) generateCodexMD() string {
	var b strings.Builder
	b.WriteString("<!-- markdownlint-disable MD013 -->\n")
	b.WriteString("# OpenAI Codex Context & Operating Rules: " + t.RepoName + "\n")
	b.WriteString(compiledNotice())
	b.WriteString("## Directives & Invariants\n\n")
	b.WriteString("- Act on verified state, not assumption.\n")
	b.WriteString("- Zero tolerance for destructive or un-revertible actions without authorization.\n")
	b.WriteString("- Maintain strict DAG call graphs (zero recursion, HISS-01).\n")
	b.WriteString("- Enforce scalar loop bounds and context timeouts on all I/O (HISS-02).\n")
	b.WriteString("- Zero unchecked errors and zero unwrap/expect calls (HISS-07).\n")
	b.WriteString("- Positive, negative, and boundary tests mandatory for all public interfaces (HISS-15).\n\n")
	b.WriteString("## Verification Commands\n\n")
	b.WriteString("```bash\n")
	b.WriteString(t.VerifyCommand + "\n")
	b.WriteString("```\n")
	return b.String()
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// RepoNameFromDir returns "owner/name" from the .standards.yaml in dir, or
// DefaultRepoName when there is no readable manifest.
func RepoNameFromDir(dir string) string {
	m, err := config.LoadManifest(filepath.Join(dir, ".standards.yaml"))
	if err != nil || m.Repository.Name == "" {
		return DefaultRepoName
	}
	if m.Repository.Owner == "" {
		return m.Repository.Name
	}
	return m.Repository.Owner + "/" + m.Repository.Name
}
