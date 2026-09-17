package agentcontext

import (
	"fmt"
	"strings"
)

const MaxLineBudget = 300

// maxCanonicalLines bounds the ownership scan (HISS-02). A canonical file larger than the
// shared body plus one full-budget section per target cannot compile within the budget.
const maxCanonicalLines = MaxLineBudget * (len(vendorTargets) + 1)

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

// vendorTarget pairs a compiled file with the AGENTS.md `## <Vendor>` heading that belongs
// to that file alone. Every line outside such a section is shared by all targets.
type vendorTarget struct {
	path    string
	section string
	prefix  string
}

var vendorTargets = [6]vendorTarget{
	{path: "CLAUDE.md", section: "Claude Code"},
	{path: ".cursor/rules/hiss-invariants.mdc", section: "Cursor", prefix: cursorFrontmatter},
	{path: ".github/copilot-instructions.md", section: "GitHub Copilot"},
	{path: ".windsurfrules", section: "Windsurf"},
	{path: ".gemini/GEMINI.md", section: "Gemini"},
	{path: ".codex/rules.md", section: "Codex"},
}

// CompileContent synthesizes vendor-specific files directly from in-memory markdown content.
// Each target keeps the shared body and its own `## <Vendor>` section in place; every other
// vendor's section is removed, so guidance written for one agent never reaches another.
func (t *Transpiler) CompileContent(content string) (*CompileResult, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("canonical AGENTS.md content is empty")
	}
	lines, owners, err := ownVendorLines(content)
	if err != nil {
		return nil, err
	}
	files := make([]TargetFile, 0, len(vendorTargets))
	for _, target := range vendorTargets {
		body := target.prefix + generatedHeader + selectVendorLines(lines, owners, target.section)
		count := countLines(body)
		if count > t.MaxLines {
			return nil, fmt.Errorf("target file %s exceeds max line budget (%d > %d)", target.path, count, t.MaxLines)
		}
		files = append(files, TargetFile{RelativePath: target.path, Content: body, LineCount: count})
	}

	return &CompileResult{
		SourcePath: "AGENTS.md",
		Files:      files,
	}, nil
}

// ownVendorLines splits content into lines and labels each one with the vendor section that
// owns it, or "" when it is shared by every target. A section opens at its `## <Vendor>`
// heading and closes at the next H1 or H2; a heading inside a fenced block opens nothing.
func ownVendorLines(content string) ([]string, []string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) > maxCanonicalLines {
		return nil, nil, fmt.Errorf("canonical AGENTS.md has %d lines; the vendor line budget admits at most %d", len(lines), maxCanonicalLines)
	}
	owners := make([]string, len(lines))
	owner, fenced := "", false
	for i := range lines {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
		} else if !fenced && isTopHeading(trimmed) {
			owner = vendorFor(trimmed)
		}
		owners[i] = owner
	}
	return lines, owners, nil
}

// selectVendorLines rejoins the shared lines plus the lines owned by section.
func selectVendorLines(lines, owners []string, section string) string {
	kept := make([]string, 0, len(lines))
	for i := range lines {
		if owners[i] == "" || owners[i] == section {
			kept = append(kept, lines[i])
		}
	}
	return strings.Join(kept, "\n")
}

// isTopHeading reports whether a trimmed line is an ATX H1 or H2, the only headings that
// close a vendor section.
func isTopHeading(trimmed string) bool {
	return strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ")
}

// vendorFor returns the section a trimmed H2 heading opens, or "" for an H1 and for any
// heading whose title is not exactly a vendor name.
func vendorFor(trimmed string) string {
	title := strings.TrimPrefix(trimmed, "## ")
	if title == trimmed {
		return ""
	}
	title = strings.TrimSpace(title)
	for _, target := range vendorTargets {
		if strings.EqualFold(title, target.section) {
			return target.section
		}
	}
	return ""
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// Vendor wrappers carry metadata only. Policy must come entirely from AGENTS.md.
const generatedHeader = "<!-- markdownlint-disable MD013 -->\n" +
	"<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"

const cursorFrontmatter = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n"
