package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestAdoptMCPSourceRootRefusalNamesParameter_3D covers how standards_adopt reports a source it
// cannot capture (#839, HISS-15). Positive: the source_root description says what a source root
// may be, in the words the CLI flags use. Negative: a source_root holding go.mod outside any Git
// checkout is refused naming source_root, the path and the cause, never the CLI flag, and
// nothing is written. Boundary: a dry run is refused the same way.
func TestAdoptMCPSourceRootRefusalNamesParameter_3D(t *testing.T) {
	description := adoptTool(t).InputSchema.Properties["source_root"].Description
	// MCP descriptions are literal agent-register text (the caveman source census), so they restate
	// the CLI's source root forms rather than concatenating them.
	if !strings.Contains(description, devcontainer.SourceRootForms) || !strings.Contains(description, "git ls-files") {
		t.Fatalf("source_root description disagrees with the CLI source root help: %q", description)
	}

	source := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), source); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the refusal needs one outside", present, err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module github.com/cordanaLLM/praetor\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{false, true} {
		root := t.TempDir()
		initGitRepo(t, root)
		srv, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "test", AllowOutsideRoot: true})
		if err != nil {
			t.Fatal(err)
		}
		result := callTool(t, srv, "standards_adopt", map[string]any{"source_root": source, "record_baseline": false, "dry_run": dryRun})
		text := result.Content[0].Text
		want := "source_root: prepare devcontainer bootstrap: bootstrap source " + strconv.Quote(source) + ": not a Git checkout"
		// The report's remedy lines print whole CLI commands; the refusal itself names no flag.
		if !result.IsError || !strings.Contains(text, want) || strings.Contains(text, "--lock-source-root \"") || strings.Contains(text, "--lock-source-root: ") {
			t.Fatalf("dry_run=%v: refusal lacks %q or names the CLI flag:\n%s", dryRun, want, text)
		}
		if _, err := os.Stat(filepath.Join(root, ".standards.yaml")); !os.IsNotExist(err) {
			t.Fatalf("dry_run=%v: refused adoption wrote the manifest: %v", dryRun, err)
		}
	}
}
