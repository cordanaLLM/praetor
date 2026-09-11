package compiler

import (
	"strings"
	"testing"
)

func FuzzCompilerTranspile(f *testing.F) {
	f.Add("# Agent Operating Harness\n## Commands\nmake verify-all\n")
	f.Add("## Core Directives & Invariants\n| Invariant | Scope |\n| HISS-01 | Control |\n")
	f.Add("```mermaid\nflowchart LR\nA --> B\n```\n")

	tr := NewTranspiler()

	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 100000 {
			content = content[:100000]
		}

		res, err := tr.CompileContent(content)
		if err != nil {
			return
		}
		if res == nil {
			t.Fatal("CompileContent returned nil result without error")
		}
		if len(res.Files) != 6 {
			t.Fatalf("expected 6 vendor targets, got %d", len(res.Files))
		}
		for _, file := range res.Files {
			if strings.TrimSpace(file.RelativePath) == "" {
				t.Fatal("target file has empty relative path")
			}
		}
	})
}
