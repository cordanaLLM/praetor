// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLinterFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunLinters_CLI_StripConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".golangci.yml")
	out := filepath.Join(dir, "out.yml")
	writeLinterFixture(t, cfg, "version: \"2\"\nrun:\n  timeout: 5m\n")

	// Positive
	err := runLinters([]string{"exceptions", "strip-config", "--config=" + cfg, "--out=" + out})
	if err != nil {
		t.Fatalf("strip-config failed: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	if !strings.Contains(string(data), "relative-path-mode: gitroot") {
		t.Errorf("output missing relative-path-mode:\n%s", string(data))
	}
	if !strings.Contains(string(data), "uniq-by-line: false") {
		t.Errorf("output missing uniq-by-line: false:\n%s", string(data))
	}

	// Negative: missing --out
	if err := runLinters([]string{"exceptions", "strip-config", "--config=" + cfg}); err == nil {
		t.Error("expected error for missing --out")
	}
}

func TestRunLinters_CLI_Generate(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".golangci.yml")
	rep := filepath.Join(dir, "report.json")
	writeLinterFixture(t, cfg, "version: \"2\"\nlinters:\n  enable:\n    - gocyclo\n")
	writeLinterFixture(t, rep, `{"Issues":[{"FromLinter":"gocyclo","Pos":{"Filename":"a_test.go"}}]}`)

	// Positive: generate output
	args := []string{
		"exceptions", "generate",
		"--config=" + cfg,
		"--report=" + rep,
		"--issue=1030",
		"--today=2026-10-18",
	}
	if err := runLinters(args); err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	// Negative: missing --report
	if err := runLinters([]string{"exceptions", "generate", "--issue=1030"}); err == nil {
		t.Error("expected error for missing --report")
	}

	// Negative: missing or invalid --issue
	if err := runLinters([]string{"exceptions", "generate", "--report=" + rep}); err == nil {
		t.Error("expected error for missing --issue")
	}

	// Negative: invalid --today format
	badDate := []string{"exceptions", "generate", "--report=" + rep, "--issue=1030", "--today=invalid"}
	if err := runLinters(badDate); err == nil {
		t.Error("expected error for invalid --today")
	}
}

func TestRunLinters_CLI_UnknownCommandsAndHelp(t *testing.T) {
	// Boundary: help tokens
	if err := runLinters([]string{}); err != nil {
		t.Errorf("empty args should succeed: %v", err)
	}
	if err := runLinters([]string{"--help"}); err != nil {
		t.Errorf("--help should succeed: %v", err)
	}
	if err := runLinters([]string{"exceptions", "-h"}); err != nil {
		t.Errorf("exceptions -h should succeed: %v", err)
	}

	// Negative: unknown subcommand
	if err := runLinters([]string{"unknown"}); err == nil {
		t.Error("expected error for unknown subcommand")
	}
	if err := runLinters([]string{"exceptions", "unknown"}); err == nil {
		t.Error("expected error for unknown exceptions action")
	}
}
