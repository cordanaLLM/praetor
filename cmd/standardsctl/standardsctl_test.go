package main

import (
	"flag"
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

func TestDispatchCommand_AuditAndBaseline(t *testing.T) {
	// Audit pass with real files
	if err := dispatchCommand("audit", []string{
		"--config=../../.standards.yaml",
		"--baseline=../../.standards-baseline.json",
		"--agents=../../AGENTS.md",
	}); err != nil {
		t.Fatalf("audit failed: %v", err)
	}

	// Audit fail with nonexistent manifest
	if err := dispatchCommand("audit", []string{"--config=nonexistent.yaml"}); err == nil {
		t.Fatal("expected audit to fail with nonexistent config")
	}

	// Baseline inspect
	if err := dispatchCommand("baseline", []string{"--file=../../.standards-baseline.json"}); err != nil {
		t.Fatalf("baseline inspect failed: %v", err)
	}
}

func TestDispatchCommand_ContextAndDevcontainer(t *testing.T) {
	// Compile-context verify
	if err := dispatchCommand("compile-context", []string{"--verify", "--source=../../AGENTS.md", "--target-dir=../.."}); err != nil {
		t.Fatalf("compile-context verify failed: %v", err)
	}

	// Devcontainer verify
	if err := dispatchCommand("devcontainer", []string{"--verify", "--config=../../.standards.yaml", "--output=../../.devcontainer/devcontainer.json"}); err != nil {
		t.Fatalf("devcontainer verify failed: %v", err)
	}

	// Devcontainer help
	if err := dispatchCommand("devcontainer", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("devcontainer -h failed: %v", err)
	}
}

func TestDispatchCommand_EditorsAndFlavors(t *testing.T) {
	tmpDir := t.TempDir()

	// Editors synthesize in temp dir
	if err := dispatchCommand("editors", []string{"generate", "--path=" + tmpDir}); err != nil {
		t.Fatalf("editors failed: %v", err)
	}

	// Flavors list and help
	if err := dispatchCommand("flavors", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("flavors -h failed: %v", err)
	}
	if err := dispatchCommand("flavors", []string{"--config=../../.config/flavors.yaml", "list"}); err != nil {
		t.Fatalf("flavors list failed: %v", err)
	}
}

func TestDispatchCommand_ModelsAndHarvest(t *testing.T) {
	tmpDir := t.TempDir()

	// Models list with existing routing config
	if err := dispatchCommand("models", []string{"--config=../../.config/models/routing.yaml", "list"}); err != nil {
		t.Fatalf("models list failed: %v", err)
	}

	// Harvest help and dry run on empty dev dir
	if err := dispatchCommand("harvest", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("harvest -h failed: %v", err)
	}
	if err := dispatchCommand("harvest", []string{"fleet", "--dev-dir=" + tmpDir}); err != nil {
		t.Fatalf("harvest fleet failed: %v", err)
	}
}

func TestDispatchCommand_AdoptPlanSyncInit(t *testing.T) {
	// Adopt help
	if err := dispatchCommand("adopt", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("adopt -h failed: %v", err)
	}

	// Plan and Sync help
	if err := dispatchCommand("plan", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("plan -h failed: %v", err)
	}
	if err := dispatchCommand("sync", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("sync -h failed: %v", err)
	}

	// Init help
	if err := dispatchCommand("init", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("init -h failed: %v", err)
	}
}

func TestDispatchCommand_PaperclipAndAdopt(t *testing.T) {
	tmpDir := t.TempDir()

	// Paperclip help
	if err := dispatchCommand("paperclip", []string{"-h"}); err != nil && err != flag.ErrHelp {
		t.Fatalf("paperclip -h failed: %v", err)
	}

	// Paperclip harness synthesis
	if err := dispatchCommand("paperclip", []string{"harness", "--path=" + tmpDir}); err != nil {
		t.Fatalf("paperclip harness failed: %v", err)
	}

	// Paperclip disposition
	dispArgs := []string{
		"disposition",
		"--issue=ISS-42",
		"--status=in_review",
		"--output=" + filepath.Join(tmpDir, "disposition.json"),
		"--note=Completed task",
		"--proof=receipt:ed25519:abcdef1234567890",
	}
	if err := dispatchCommand("paperclip", dispArgs); err != nil {
		t.Fatalf("paperclip disposition failed: %v", err)
	}

	// Adopt dry-run
	if err := dispatchCommand("adopt", []string{"--dry-run", "--path=" + tmpDir, "--profile=framework"}); err != nil {
		t.Fatalf("adopt dry-run failed: %v", err)
	}
}
