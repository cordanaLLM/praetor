package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adr"
)

func TestDecisionsExportCommandWritesRepositoryCorpus(t *testing.T) {
	repo := t.TempDir()
	writeDecisionCLITestFile(t, repo, filepath.ToSlash(filepath.Join(adr.RecordDirectory, "0001-choice.md")), "# Choice\n")
	if err := runDecisionExport([]string{"--path", repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".standards", "cache", "decisions", "decisions", "adr", "adr-0001-choice.md")); err != nil {
		t.Fatalf("default corpus path absent: %v", err)
	}
}

func TestDecisionsPinCheckCommandExitCodes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if code := commandExitCode(io.Discard, runDecisionPinCheck(nil)); code != 1 {
		t.Fatalf("missing binary rc = %d, want 1", code)
	}
	name := "decided-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("wrong digest"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := commandExitCode(io.Discard, runDecisionPinCheck(nil)); code != 1 {
		t.Fatalf("mismatched binary rc = %d, want 1", code)
	}
}

func TestDecisionsPinCheckCommandCorrectBinaryExitZero(t *testing.T) {
	release := os.Getenv("PRAETOR_DECIDED_MCP_TEST_BINARY")
	if release == "" {
		t.Skip("PRAETOR_DECIDED_MCP_TEST_BINARY not set; correct release binary unavailable")
	}
	data, err := os.ReadFile(release)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "decided-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runDecisionPinCheck(nil); err != nil {
		t.Fatalf("correct binary check failed: %v", err)
	}
}

// Positive: decisions serve exits with decided-mcp's own status and adds no text. Negative:
// another command that wraps a failed child keeps exit 1 and its error text.
func TestServerExitStatusPassesOnlyTheServerStatus(t *testing.T) {
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd", "/c", "exit 23")
	} else {
		command = exec.Command("/bin/sh", "-c", "exit 23")
	}
	err := fmt.Errorf("run decided-mcp: %w", command.Run())
	var stderr bytes.Buffer
	if got := commandExitCode(&stderr, serverExitStatus(err)); got != 23 || stderr.Len() != 0 {
		t.Fatalf("server exit = %d, stderr %q; want 23 and no text", got, stderr.String())
	}
	if got := commandExitCode(&stderr, err); got != 1 || !strings.Contains(stderr.String(), "run decided-mcp") {
		t.Fatalf("wrapped child elsewhere = %d, stderr %q; want 1 with the error text", got, stderr.String())
	}
	if serverExitStatus(nil) != nil {
		t.Fatal("a clean server exit became an error")
	}
}

func TestDecisionsPublicCommandDispatch(t *testing.T) {
	if err := runDecisions([]string{"unknown"}); err == nil {
		t.Fatal("unknown decisions command accepted")
	}
	if err := runDecisions([]string{"pin-check", "extra"}); err == nil {
		t.Fatal("unexpected pin-check argument accepted")
	}
}

func writeDecisionCLITestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
