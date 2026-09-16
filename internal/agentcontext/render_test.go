package agentcontext

import (
	"strings"
	"testing"
)

func TestCompileContentGoldenAndBudget(t *testing.T) {
	source := "# Fixture\nCanonical policy only.\n"
	tr := NewTranspiler()
	result, err := tr.CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	header := "<!-- markdownlint-disable MD013 -->\n<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->\n\n"
	paths := []string{"CLAUDE.md", ".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md", ".windsurfrules", ".gemini/GEMINI.md", ".codex/rules.md"}
	if len(result.Files) != len(paths) {
		t.Fatalf("got %d outputs", len(result.Files))
	}
	for i, file := range result.Files {
		want := header + source
		if i == 1 {
			want = "---\ndescription: Canonical agent instructions\nglobs: \"*\"\nalwaysApply: true\n---\n\n" + want
		}
		if file.RelativePath != paths[i] || file.Content != want || file.LineCount != strings.Count(want, "\n")+1 {
			t.Fatalf("projection changed: %+v", file)
		}
	}
	for _, invalid := range []string{"", " \n", strings.Repeat("line\n", MaxLineBudget)} {
		if got, err := tr.CompileContent(invalid); err == nil || got != nil {
			t.Fatalf("invalid canonical accepted: %v", err)
		}
	}
}
