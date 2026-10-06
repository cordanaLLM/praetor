package agentcontext

import (
	"os"
	"path"
	"path/filepath"
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

// TestCompileContentNestedFenceOpensNoVendorSection pins the ownership scan on
// util.MarkdownFence (HISS-19). The toggle it replaced treated the inner ``` of a ````
// block as a close, so the quoted `## Gemini` heading below opened a real Gemini section
// and the shared appendix after the block was swallowed into it - guidance written for one
// agent reaching exactly the file it must not.
func TestCompileContentNestedFenceOpensNoVendorSection(t *testing.T) {
	canonical := strings.Join([]string{
		"# Harness",
		"Shared policy for every agent.",
		"",
		"## Claude Code",
		"Run the skills directory.",
		"",
		"````md",
		"```",
		"## Gemini",
		"Quoted, not a section.",
		"````",
		"",
		"## Shared Appendix",
		"Everyone reads this.",
		"",
	}, "\n")
	result, err := NewTranspiler().CompileContent(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		for _, shared := range []string{"# Harness", "## Shared Appendix", "Everyone reads this."} {
			if !strings.Contains(file.Content, shared) {
				t.Fatalf("%s lost the shared line %q to a fenced heading", file.RelativePath, shared)
			}
		}
		// The whole quoted block stays inside the section it was written in.
		for _, owned := range []string{"Run the skills directory.", "## Gemini", "Quoted, not a section."} {
			got, want := strings.Contains(file.Content, owned), file.RelativePath == "CLAUDE.md"
			if got != want {
				t.Fatalf("%s contains(%q) = %v, want %v", file.RelativePath, owned, got, want)
			}
		}
	}
}

// VendorTargetPaths is the list the CI filter classifies agent files by (BUG-242), so it must
// name exactly the files CompileContent writes, and a caller that edits its copy must not
// change what the next caller sees.
func TestVendorTargetPathsMatchCompiledFiles(t *testing.T) {
	paths := VendorTargetPaths()
	if strings.Join(paths, "\n") != strings.Join(testPaths, "\n") {
		t.Fatalf("vendor target paths = %q, want %q", paths, testPaths)
	}
	result, err := NewTranspiler().CompileContent("# Fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range result.Files {
		if file.RelativePath != paths[i] {
			t.Fatalf("compiled file %d = %s, listed %s", i, file.RelativePath, paths[i])
		}
	}
	paths[0] = "mutated.md"
	if again := VendorTargetPaths(); again[0] != "CLAUDE.md" {
		t.Fatalf("a caller's edit leaked into the shared list: %q", again)
	}
}

// TestVendorTargetsMatchCompiledFiles: the exported registry names exactly the files
// CompileContent writes, in order, each with its own section, so a surface reading it cannot
// name a file compile-context does not write or miss one it does (BUG-840).
func TestVendorTargetsMatchCompiledFiles(t *testing.T) {
	result, err := NewTranspiler().CompileContent("# Fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	targets := allVendorTargets(t)
	if len(targets) != len(result.Files) || len(targets) != len(testPaths) {
		t.Fatalf("registry lists %d targets, compile writes %d", len(targets), len(result.Files))
	}
	sections := map[string]bool{}
	for i, target := range targets {
		if target.Path != result.Files[i].RelativePath || target.Path != testPaths[i] {
			t.Errorf("target %d = %q, compiled %q", i, target.Path, result.Files[i].RelativePath)
		}
		if target.Section == "" || sections[target.Section] || vendorFor("## "+target.Section) != target.Section {
			t.Errorf("target %q section %q is empty, repeated or not recognised", target.Path, target.Section)
		}
		sections[target.Section] = true
	}
}

// namesTarget reports whether rule names target in a code span, exactly or by a glob; the
// frozen clarity floor (internal/compiler/testdata/agents-floor.txt) keeps the Cursor glob.
func namesTarget(rule, target string) bool {
	spans := strings.Split(rule, "`")
	for i := 1; i < len(spans); i += 2 {
		if matched, err := path.Match(spans[i], target); err == nil && matched {
			return true
		}
	}
	return false
}

// TestRepositoryAgentsNamesEveryVendorTarget: this repository's own transpiler rule names
// each compiled file; it had named four of the six (BUG-840).
func TestRepositoryAgentsNamesEveryVendorTarget(t *testing.T) {
	if namesTarget("edit `.cursor/rules/*.mdc` and `CLAUDE.md`", ".codex/rules.md") ||
		!namesTarget("edit `.cursor/rules/*.mdc`", ".cursor/rules/hiss-invariants.mdc") {
		t.Fatal("namesTarget matches the wrong spans")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	var rule string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "**Context transpiler first.**") {
			rule = line
		}
	}
	if rule == "" {
		t.Fatal("AGENTS.md has no transpiler rule")
	}
	for _, target := range allVendorTargets(t) {
		if !namesTarget(rule, target.Path) {
			t.Errorf("AGENTS.md transpiler rule omits %s", target.Path)
		}
	}
}

// allVendorTargets is VendorTargets with no agent_clients selection, which must equal
// AllVendorTargets: every compiled file.
func allVendorTargets(t *testing.T) []VendorTarget {
	t.Helper()
	targets, err := VendorTargets(nil)
	if err != nil {
		t.Fatal(err)
	}
	all := AllVendorTargets()
	if len(all) != len(targets) {
		t.Fatalf("AllVendorTargets lists %d files, the nil selection %d", len(all), len(targets))
	}
	for i := range all {
		if all[i] != targets[i] {
			t.Fatalf("AllVendorTargets[%d] = %+v, nil selection %+v", i, all[i], targets[i])
		}
	}
	return targets
}

// TestVendorTargetsReturnsACopy: editing the returned slice leaves the registry intact.
func TestVendorTargetsReturnsACopy(t *testing.T) {
	first := allVendorTargets(t)
	first[0].Path = "edited"
	if allVendorTargets(t)[0].Path != testPaths[0] {
		t.Fatal("VendorTargets exposed the registry")
	}
}

// TestVendorTargetsFollowsClientSelection: the list names what compile-context writes under
// agent_clients. Positive: a subset keeps exactly the files CompileContent writes for it, in
// order. Boundary: an empty selection names none. Negative: an unknown id fails as the
// transpiler does, instead of naming every file.
func TestVendorTargetsFollowsClientSelection(t *testing.T) {
	selection := []string{"codex", "Claude"}
	tr := NewTranspiler()
	tr.Clients = selection
	result, err := tr.CompileContent("# Fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := VendorTargets(selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || len(result.Files) != 2 {
		t.Fatalf("selection names %d targets, compile writes %d", len(targets), len(result.Files))
	}
	for i, target := range targets {
		if target.Path != result.Files[i].RelativePath {
			t.Errorf("target %d = %q, compiled %q", i, target.Path, result.Files[i].RelativePath)
		}
	}
	if none, err := VendorTargets([]string{}); err != nil || len(none) != 0 {
		t.Fatalf("empty selection = %v, %v", none, err)
	}
	if _, err := VendorTargets([]string{"claude", "vim"}); err == nil || !strings.Contains(err.Error(), "unknown agent client id(s): vim") {
		t.Fatalf("unknown client accepted: %v", err)
	}
}

// TestContextFiles_Positive_MatchesCanonicalAndVendorTargets tests that ContextFiles returns
// the canonical file followed by every vendor target path in compile order (HISS-19).
func TestContextFiles_Positive_MatchesCanonicalAndVendorTargets(t *testing.T) {
	files := ContextFiles()
	vendor := VendorTargetPaths()
	if len(files) != len(vendor)+1 {
		t.Fatalf("ContextFiles length %d, want %d", len(files), len(vendor)+1)
	}
	if files[0] != CanonicalFile {
		t.Errorf("ContextFiles[0] = %q, want %q", files[0], CanonicalFile)
	}
	for i, path := range vendor {
		if files[i+1] != path {
			t.Errorf("ContextFiles[%d] = %q, want %q", i+1, files[i+1], path)
		}
	}
}

// TestIsContextPath_Positive verifies that canonical AGENTS.md and compiled vendor projections
// are recognized with direct, relative, and nested paths.
func TestIsContextPath_Positive(t *testing.T) {
	for _, path := range []string{
		"AGENTS.md",
		"./AGENTS.md",
		"sub/dir/AGENTS.md",
		"CLAUDE.md",
		"./CLAUDE.md",
		"sub/dir/CLAUDE.md",
		".cursor/rules/hiss-invariants.mdc",
		"sub/.cursor/rules/hiss-invariants.mdc",
		".github/copilot-instructions.md",
		".windsurfrules",
		".gemini/GEMINI.md",
		".codex/rules.md",
	} {
		if !IsContextPath(path) {
			t.Errorf("IsContextPath(%q) = false, want true", path)
		}
	}
}

// TestIsContextPath_Negative verifies that non-context paths, notes, and commit messages
// are not classified as context paths.
func TestIsContextPath_Negative(t *testing.T) {
	for _, path := range []string{
		"",
		"-",
		"candidate-note.md",
		"NOT_AGENTS.md",
		"sub/NOT_AGENTS.md",
		"my-AGENTS.md",
		"not_CLAUDE.md",
		"COMMIT_EDITMSG",
		".git/COMMIT_EDITMSG",
		"scripts/check.sh",
		"main.go",
	} {
		if IsContextPath(path) {
			t.Errorf("IsContextPath(%q) = true, want false", path)
		}
	}
}

// TestIsContextPath_Boundary verifies case-insensitivity and platform path separator normalization (HISS-21).
func TestIsContextPath_Boundary(t *testing.T) {
	for _, path := range []string{
		"agents.md",
		"Agents.md",
		"claude.md",
		".WINDSURFRULES",
		".cursor\\rules\\hiss-invariants.mdc",
		"sub\\dir\\AGENTS.md",
		"sub\\dir\\CLAUDE.md",
	} {
		if !IsContextPath(path) {
			t.Errorf("boundary IsContextPath(%q) = false, want true", path)
		}
	}
}
