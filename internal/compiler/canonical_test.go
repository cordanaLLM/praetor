package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompilePreservesCanonicalBody leaves canonical policy at its source instead of copying it.
func TestCompilePreservesCanonicalBody(t *testing.T) {
	source := "# Fixture\n\nUnique policy: DEV_MCP_PREFLIGHT_SENTINEL.\n\n```sh\ncustom-check --strict\n```\n"
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		if strings.Contains(file.Content, "DEV_MCP_PREFLIGHT_SENTINEL") {
			t.Errorf("%s copied canonical source body", file.RelativePath)
		}
		if strings.Contains(file.Content, "make verify-all") {
			t.Errorf("%s injected policy absent from canonical source", file.RelativePath)
		}
	}
}

// TestVerifyAcceptsCanonicalBodyChange requires regeneration only for client-only edits.
func TestVerifyAcceptsCanonicalBodyChange(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Fixture\n\nOriginal policy.\n\n## Claude Code\nOriginal client policy.\n")
	compiler := NewTranspiler()
	result, err := compiler.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiler.WriteOutputs(result, root); err != nil {
		t.Fatal(err)
	}
	if err := compiler.Verify(source, root); err != nil {
		t.Fatalf("unchanged source: %v", err)
	}
	writeCanonicalFixture(t, source, "# Fixture\n\nChanged policy.\n\n## Claude Code\nOriginal client policy.\n")
	if err := compiler.Verify(source, root); err != nil {
		t.Fatalf("shared body edit invalidated pointer: %v", err)
	}
	writeCanonicalFixture(t, source, "# Fixture\n\nChanged policy.\n\n## Claude Code\nChanged client policy.\n")
	if err := compiler.Verify(source, root); err == nil {
		t.Fatal("client-only edit incorrectly verified")
	}
	for _, file := range result.Files {
		content, err := os.ReadFile(filepath.Join(root, file.RelativePath))
		if err != nil || string(content) != file.Content {
			t.Errorf("verification changed %s: %v", file.RelativePath, err)
		}
	}
}

func TestCompileRejectsEmptyCanonicalSource(t *testing.T) {
	for _, source := range []string{"", " \n\t\n"} {
		if result, err := NewTranspiler().CompileContent(source); err == nil || result != nil {
			t.Errorf("empty canonical source accepted: err=%v", err)
		}
	}
}

// TestCompileVendorLineBudget refuses client-only text above the projection line cap.
func TestCompileVendorLineBudget(t *testing.T) {
	compiler := NewTranspiler()
	compiler.Clients = []string{"cursor"}
	minimal, err := compiler.CompileContent("## Cursor\npolicy")
	if err != nil {
		t.Fatal(err)
	}
	maxLines := 0
	for _, file := range minimal.Files {
		if file.LineCount > maxLines {
			maxLines = file.LineCount
		}
	}
	source := "## Cursor\npolicy" + strings.Repeat("\npolicy", MaxLineBudget-maxLines)
	result, err := compiler.CompileContent(source)
	if err != nil {
		t.Fatalf("exact maximum budget rejected: %v", err)
	}
	for _, file := range result.Files {
		if !strings.Contains(file.Content, source) {
			t.Errorf("%s truncated source to meet budget", file.RelativePath)
		}
	}
	if result, err := compiler.CompileContent(source + "\noverflow"); err == nil || result != nil {
		t.Errorf("maximum budget plus one accepted: err=%v", err)
	}
}

func writeCanonicalFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}
