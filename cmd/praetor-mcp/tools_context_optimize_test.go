// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func contextMCPReport(t *testing.T, result *mcp.ToolResult) contextopt.Report {
	t.Helper()
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("analysis failed: %+v", result)
	}
	var report contextopt.Report
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestContextAnalyzeMCPReturnsMetadataWithoutWrites(t *testing.T) {
	srv, root := newFixtureServer(t)
	content := bytes.Repeat([]byte("private context marker\n"), 100)
	for _, name := range []string{"rules.md", "copy.md"} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_context_analyze", map[string]any{"sources": []string{"rules.md", "copy.md"}})
	report := contextMCPReport(t, result)
	if len(report.Documents) != 1 || report.SavedBytes <= 0 || !report.ReviewRequired {
		t.Fatalf("invalid report: %+v", report)
	}
	if strings.Contains(result.Content[0].Text, "private context marker") {
		t.Fatal("MCP leaked source payload")
	}
	after, err := os.ReadDir(root)
	if err != nil || len(before) != len(after) {
		t.Fatalf("read-only analysis wrote files: %v", err)
	}
	for _, name := range []string{"rules.md", "copy.md"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("source changed: %v", err)
		}
	}
}

func TestContextAnalyzeMCPRejectsBadArgumentsAndBoundaries(t *testing.T) {
	srv, root := newFixtureServer(t)
	for _, args := range []map[string]any{
		{}, {"sources": "AGENTS.md"}, {"sources": []any{1}}, {"sources": []string{}},
		{"sources": []string{"../outside.md"}}, {"root": t.TempDir(), "sources": []string{"AGENTS.md"}},
		{"sources": []string{"AGENTS.md"}, "output_dir": "candidate"},
	} {
		if result := callTool(t, srv, "standards_context_analyze", args); !result.IsError {
			t.Fatalf("invalid args accepted: %+v", args)
		}
	}
	sources := make([]string, contextopt.MaxSources)
	for i := 0; i < len(sources); i++ {
		sources[i] = fmt.Sprintf("context-%d.md", i)
		if err := os.WriteFile(filepath.Join(root, sources[i]), []byte("rule"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := contextMCPReport(t, callTool(t, srv, "standards_context_analyze", map[string]any{"sources": sources}))
	if len(report.Sources) != contextopt.MaxSources {
		t.Fatal("exact limit silently truncated")
	}
	if result := callTool(t, srv, "standards_context_analyze", map[string]any{"sources": append(sources, "extra.md")}); !result.IsError {
		t.Fatal("source limit overflow accepted")
	}
	if err := os.WriteFile(filepath.Join(root, sources[0]), bytes.Repeat([]byte("a"), contextopt.MaxSourceBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	contextMCPReport(t, callTool(t, srv, "standards_context_analyze", map[string]any{"sources": sources[:1]}))
	if err := os.WriteFile(filepath.Join(root, sources[0]), bytes.Repeat([]byte("a"), contextopt.MaxSourceBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := callTool(t, srv, "standards_context_analyze", map[string]any{"sources": sources[:1]}); !result.IsError {
		t.Fatal("byte limit overflow accepted")
	}
}
