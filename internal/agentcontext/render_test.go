package agentcontext

import (
	"strings"
	"testing"
)

const (
	testHeader      = "<!-- markdownlint-disable MD013 -->\n<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"
	testFrontmatter = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n"
)

var testPaths = []string{"CLAUDE.md", ".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md", ".windsurfrules", ".gemini/GEMINI.md", ".codex/rules.md"}

func TestCompileContentGoldenAndBudget(t *testing.T) {
	source := "# Fixture\nCanonical policy only.\n"
	tr := NewTranspiler()
	result, err := tr.CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != len(testPaths) {
		t.Fatalf("got %d outputs", len(result.Files))
	}
	for i, file := range result.Files {
		want := testHeader + source
		if i == 1 {
			want = testFrontmatter + want
		}
		if file.RelativePath != testPaths[i] || file.Content != want || file.LineCount != strings.Count(want, "\n")+1 {
			t.Fatalf("projection changed: %+v", file)
		}
	}
	for _, invalid := range []string{"", " \n", strings.Repeat("line\n", MaxLineBudget)} {
		if got, err := tr.CompileContent(invalid); err == nil || got != nil {
			t.Fatalf("invalid canonical accepted: %v", err)
		}
	}
}

// vendorFixture carries a vendor section, a lower-cased one, an H3 vendor title and a fenced
// heading, so a leak in either direction is observable.
var vendorFixture = strings.Join([]string{
	"# Harness",
	"Shared policy for every agent.",
	"",
	"### Windsurf",
	"An H3 never opens a section.",
	"",
	"## Claude Code",
	"Run the skills directory.",
	"",
	"## cursor",
	"Use the rules pane.",
	"",
	"```md",
	"## Gemini",
	"Fenced, not a section.",
	"```",
	"",
	"## Shared Appendix",
	"Everyone reads this.",
	"",
}, "\n")

func TestCompileContentKeepsOnlyOwnVendorSection(t *testing.T) {
	result, err := NewTranspiler().CompileContent(vendorFixture)
	if err != nil {
		t.Fatal(err)
	}
	shared := []string{"# Harness", "Shared policy for every agent.", "### Windsurf", "An H3 never opens a section.", "## Shared Appendix", "Everyone reads this."}
	// The fenced Gemini heading opens nothing, so it stays part of the Cursor section.
	owned := map[string][]string{
		"CLAUDE.md":                         {"## Claude Code", "Run the skills directory."},
		".cursor/rules/hiss-invariants.mdc": {"## cursor", "Use the rules pane.", "## Gemini", "Fenced, not a section."},
	}
	for _, file := range result.Files {
		for _, want := range shared {
			if !strings.Contains(file.Content, want) {
				t.Fatalf("%s dropped shared line %q", file.RelativePath, want)
			}
		}
		for path, lines := range owned {
			for _, line := range lines {
				got, want := strings.Contains(file.Content, line), path == file.RelativePath
				if got != want {
					t.Fatalf("%s contains(%q) = %v, want %v", file.RelativePath, line, got, want)
				}
			}
		}
		if file.LineCount != strings.Count(file.Content, "\n")+1 {
			t.Fatalf("%s line count %d disagrees with content", file.RelativePath, file.LineCount)
		}
	}
	if !strings.HasPrefix(result.Files[1].Content, testFrontmatter+testHeader) {
		t.Fatalf("cursor target lost its frontmatter: %q", result.Files[1].Content)
	}
}

func TestCompileContentVendorScanBoundary(t *testing.T) {
	tr := NewTranspiler()
	atLimit := strings.Repeat("x\n", maxCanonicalLines-1)
	if _, err := tr.CompileContent(atLimit); err == nil || !strings.Contains(err.Error(), "exceeds max line budget") {
		t.Fatalf("at-limit canonical: %v", err)
	}
	overLimit := strings.Repeat("x\n", maxCanonicalLines)
	if _, err := tr.CompileContent(overLimit); err == nil || !strings.Contains(err.Error(), "admits at most") {
		t.Fatalf("over-limit canonical: %v", err)
	}
}
