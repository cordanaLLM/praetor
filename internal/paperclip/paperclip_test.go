package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestDisposition_Positive_InReview(t *testing.T) {
	ctx := context.Background()
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair failed: %v", err)
	}
	receipt, err := lockdown.CreateReceipt("make verify-all", 0, []byte("ok"), "commit1", "repo1", priv)
	if err != nil {
		t.Fatalf("create receipt failed: %v", err)
	}

	disp, err := CreateDisposition(
		"ISSUE-42",
		"in_review",
		"Implemented telemetry adapter with zero warnings",
		"PR #123 opened, 100% 3D tests passed",
		"",
		"agent-1",
		receipt,
	)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if err := disp.Validate(ctx); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if disp.Status != StatusInReview {
		t.Fatalf("expected in_review status, got: %s", disp.Status)
	}

	jsonBytes, err := disp.FormatJSON()
	if err != nil || len(jsonBytes) == 0 {
		t.Fatal("FormatJSON failed")
	}
}

func TestDisposition_Positive_Blocked(t *testing.T) {
	ctx := context.Background()
	disp, err := CreateDisposition(
		"ISSUE-99",
		"blocked",
		"Missing upstream dependency auth token",
		"",
		"infra-team",
		"agent-2",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if err := disp.Validate(ctx); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if disp.Status != StatusBlocked {
		t.Fatalf("expected blocked status, got: %s", disp.Status)
	}
}

func TestHarness_Positive_SynthesizeAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := SynthesizeHarness(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SynthesizeHarness failed: %v", err)
	}

	if err := WriteHarness(h, tmpDir); err != nil {
		t.Fatalf("WriteHarness failed: %v", err)
	}

	jsonPath := filepath.Join(tmpDir, ".paperclip", "harness.json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", jsonPath)
	}

	mdPath := filepath.Join(tmpDir, ".paperclip", "rules.md")
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", mdPath)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestDisposition_Negative_MissingFields(t *testing.T) {
	// Missing issue ID
	_, err := CreateDisposition("", "in_review", "note", "proof", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty issueID")
	}

	// InReview missing proof
	_, err = CreateDisposition("ISSUE-1", "in_review", "note", "", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty proof for in_review")
	}

	// Blocked missing recovery owner
	_, err = CreateDisposition("ISSUE-1", "blocked", "note", "", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty recovery owner for blocked")
	}

	// Nil context in validation
	disp, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "owner", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	if err := disp.Validate(nilContext); err == nil {
		t.Fatal("expected error on nil context in Validate")
	}
}

func TestDisposition_Negative_TamperedReceipt(t *testing.T) {
	ctx := context.Background()
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := lockdown.CreateReceipt("make verify-all", 0, []byte("ok"), "commit1", "repo1", priv)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with receipt
	receipt.ExitCode = 1

	disp, err := CreateDisposition("ISSUE-1", "in_review", "note", "proof", "", "actor", receipt)
	if err != nil {
		t.Fatal(err)
	}

	if err := disp.Validate(ctx); err == nil {
		t.Fatal("expected validation error on tampered receipt")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestDisposition_Boundary_DoneMapsToInReview(t *testing.T) {
	// ADR-0087: "done" must automatically map to "in_review" because agent claims require review
	disp, err := CreateDisposition("ISSUE-1", "DONE", "finished", "proof of pass", "", "actor", nil)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if disp.Status != StatusInReview {
		t.Fatalf("expected DONE to map to in_review, got: %s", disp.Status)
	}
}

func TestVerifyRun_Boundary_MissingHarness(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	disp, err := CreateDisposition("ISSUE-1", "blocked", "stuck", "", "ops", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyRun(ctx, tmpDir, disp)
	if err == nil {
		t.Fatal("expected error when .paperclip/harness.json is missing")
	}
}

func TestReadDisposition_3D(t *testing.T) {
	tmpDir := t.TempDir()
	dispPath := filepath.Join(tmpDir, "disp.json")

	// Boundary: Non-existent file
	if _, err := ReadDisposition(filepath.Join(tmpDir, "nope.json")); err == nil {
		t.Fatal("expected error reading non-existent file")
	}

	// Negative: Corrupt JSON
	if err := os.WriteFile(dispPath, []byte("invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDisposition(dispPath); err == nil {
		t.Fatal("expected error parsing corrupt json")
	}

	// Positive: Valid disposition
	disp, err := CreateDisposition("ISSUE-10", "blocked", "need key", "", "security", "bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := disp.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dispPath, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadDisposition(dispPath)
	if err != nil || loaded.IssueID != "ISSUE-10" {
		t.Fatalf("failed to read valid disposition: %v", err)
	}
}

func TestLoadHarness_3D(t *testing.T) {
	tmpDir := t.TempDir()
	harnessPath := filepath.Join(tmpDir, "harness.json")

	// Boundary: Non-existent file
	if _, err := LoadHarness(filepath.Join(tmpDir, "missing.json")); err == nil {
		t.Fatal("expected error reading non-existent file")
	}

	// Negative: Corrupt JSON
	if err := os.WriteFile(harnessPath, []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarness(harnessPath); err == nil {
		t.Fatal("expected error parsing corrupt json")
	}

	// Negative: Missing platform
	if err := os.WriteFile(harnessPath, []byte(`{"version":1,"operating_contract":["rule1"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarness(harnessPath); err == nil {
		t.Fatal("expected error with missing platform")
	}

	// Positive: Synthesize and load
	h, err := SynthesizeHarness(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SynthesizeHarness failed: %v", err)
	}
	if err := WriteHarness(h, tmpDir); err != nil {
		t.Fatalf("WriteHarness failed: %v", err)
	}
	loaded, err := LoadHarness(filepath.Join(tmpDir, ".paperclip", "harness.json"))
	if err != nil || loaded.Platform != h.Platform {
		t.Fatalf("LoadHarness failed or platform mismatch: %v (got %s, expected %s)", err, loaded.Platform, h.Platform)
	}

	// Test with explicit manifest
	manifestDir := t.TempDir()
	manifestContent := "repository:\n  owner: test-org\n  name: test-repo\n"
	if err := os.WriteFile(filepath.Join(manifestDir, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}
	h2, err := SynthesizeHarness(context.Background(), manifestDir)
	if err != nil || h2.Platform != "test-org/test-repo" {
		t.Fatalf("expected platform 'test-org/test-repo', got: %s (err: %v)", h2.Platform, err)
	}
}

func TestVerifyRunInReviewWorkingTree(t *testing.T) {
	ctx := t.Context()
	repo := t.TempDir()
	disposition, err := CreateDisposition("ISSUE-1", "in_review", "review", "PR proof", "", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(ctx, repo, disposition); err == nil || !strings.Contains(err.Error(), "git status") {
		t.Fatalf("nonrepository must report git failure, got %v", err)
	}
	harness, err := SynthesizeHarness(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(harness, repo); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", ".paperclip"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "test harness"},
	} {
		if out, err := util.RunGit(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := VerifyRun(ctx, repo, disposition); err != nil {
		t.Fatalf("clean committed worktree rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "unfinished.txt"), []byte("work in progress"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(ctx, repo, disposition); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty worktree must fail disposition, got %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := VerifyRun(cancelled, repo, disposition); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification must preserve context error, got %v", err)
	}
}
