package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDispatchCommand_HelpAndVersion(t *testing.T) {
	// Boundary: Help flags
	if err := dispatchCommand("help", []string{}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if err := dispatchCommand("--help", []string{}); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if err := dispatchCommand("-h", []string{}); err != nil {
		t.Fatalf("-h failed: %v", err)
	}

	// Positive: Version
	if err := dispatchCommand("version", []string{}); err != nil {
		t.Fatalf("version failed: %v", err)
	}

	// Negative: Unknown command
	if err := dispatchCommand("unknown-cmd", []string{}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestDispatchCommand_NeedsSubcommands(t *testing.T) {
	// Boundary: No arguments prints usage
	if err := dispatchCommand("needs", []string{}); err != nil {
		t.Fatalf("needs with no args failed: %v", err)
	}
	if err := dispatchCommand("needs", []string{"-h"}); err != nil {
		t.Fatalf("needs -h failed: %v", err)
	}

	// Positive: Scan current repo
	if err := dispatchCommand("needs", []string{"scan", "--path=../.."}); err != nil {
		t.Fatalf("needs scan failed: %v", err)
	}

	// Positive: Report against current repo
	if err := dispatchCommand("needs", []string{"report", "--path=../.."}); err != nil {
		t.Fatalf("needs report failed: %v", err)
	}

	// Negative: Unknown subcommand
	if err := dispatchCommand("needs", []string{"invalid-sub"}); err == nil {
		t.Fatal("expected error for unknown needs subcommand")
	}
}

func TestDispatchCommand_CoreGovernance(t *testing.T) {
	// Positive: Sentinel check
	if err := dispatchCommand("sentinel", []string{}); err != nil {
		t.Fatalf("sentinel failed: %v", err)
	}

	// Positive: GC check
	if err := dispatchCommand("gc", []string{"--dry-run", "--path=../.."}); err != nil {
		t.Fatalf("gc failed: %v", err)
	}

	// Positive: Worktree list
	if err := dispatchCommand("worktree", []string{"list", "--path=../.."}); err != nil {
		t.Fatalf("worktree list failed: %v", err)
	}

	// Negative: Unknown worktree subcommand
	if err := dispatchCommand("worktree", []string{"bogus"}); err == nil {
		t.Fatal("expected error for unknown worktree subcommand")
	}
}

func TestDispatchCommand_BumpAndChangelog(t *testing.T) {
	// Boundary: Bump help
	if err := dispatchCommand("bump", []string{}); err != nil {
		t.Fatalf("bump with no args failed: %v", err)
	}
	if err := dispatchCommand("bump", []string{"-h"}); err != nil {
		t.Fatalf("bump -h failed: %v", err)
	}

	// Boundary: Changelog help
	if err := dispatchCommand("changelog", []string{}); err != nil {
		t.Fatalf("changelog with no args failed: %v", err)
	}
	if err := dispatchCommand("changelog", []string{"-h"}); err != nil {
		t.Fatalf("changelog -h failed: %v", err)
	}

	// Negative: Release without required version flag
	if err := dispatchCommand("release", []string{}); err == nil {
		t.Fatal("expected error when release version is missing")
	}
}

func TestDispatchCommand_ForgeSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Positive: Forge help
	if err := dispatchCommand("forge", []string{}); err != nil {
		t.Fatalf("forge with no args failed: %v", err)
	}

	// Positive: PR validation with compliant body
	prFile := filepath.Join(tmpDir, "compliant-pr.md")
	prContent := "## Summary\nTest PR\n\n- [x] HISS-16 standards verified\n- [x] 3D tests (positive, negative, boundary) added\n- [x] Ed25519 Exit-0 Receipt verified: `receipt:ed25519:abcdef0123456789`\n"
	if err := os.WriteFile(prFile, []byte(prContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", prFile}); err != nil {
		t.Fatalf("forge validate-pr failed: %v", err)
	}

	// Negative: Non-compliant PR body
	badPRFile := filepath.Join(tmpDir, "bad-pr.md")
	if err := os.WriteFile(badPRFile, []byte("Just random text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", badPRFile}); err == nil {
		t.Fatal("expected error on non-compliant PR validation")
	}
}
