package gating

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrefetchDependencies_Positive_And_Negative(t *testing.T) {
	ctx := context.Background()
	repoRoot := filepath.Join("..", "..")

	rep, err := PrefetchDependencies(ctx, repoRoot)
	if err != nil {
		t.Fatalf("expected prefetch to succeed on repo root, got: %v", err)
	}
	if !rep.VerifiedDependencies {
		t.Errorf("expected VerifiedDependencies=true, got false")
	}

	// Negative: Cancelled Context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = PrefetchDependencies(cancCtx, repoRoot)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}

	// Boundary: Nil Context
	_, err = PrefetchDependencies(nil, repoRoot)
	if err == nil {
		t.Error("expected error for nil context, got nil")
	}
}

func TestVerifyLockfiles_3D(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if err := VerifyLockfiles(repoRoot); err != nil {
		t.Fatalf("expected lockfiles to verify on repo root, got: %v", err)
	}

	tmpDir := t.TempDir()
	if err := VerifyLockfiles(tmpDir); err == nil {
		t.Errorf("expected error for missing lockfiles in empty dir, got nil")
	}

	// Boundary: Only manifest exists, missing lockfile
	manifestPath := filepath.Join(tmpDir, ".standards.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\n"), 0644); err != nil {
		t.Fatalf("failed to write dummy manifest: %v", err)
	}
	if err := VerifyLockfiles(tmpDir); err == nil {
		t.Errorf("expected error when .standards.lock is missing, got nil")
	}
}

func TestRunGatedPipeline_Positive_And_Negative(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repoRoot := filepath.Join("..", "..")
	rep, err := RunGatedPipeline(ctx, repoRoot, true)
	if err != nil {
		t.Fatalf("RunGatedPipeline dry-run error: %v", err)
	}
	if rep.Status != StatusAdmitted {
		t.Fatalf("expected StatusAdmitted, got %s", rep.Status)
	}
	if len(rep.Stages) != 4 {
		t.Errorf("expected 4 stages, got %d", len(rep.Stages))
	}
	if rep.ReceiptSignature == "" {
		t.Error("expected non-empty ReceiptSignature on admitted gate run, got empty")
	}

	// Negative: Invalid directory missing lockfiles
	tmpDir := t.TempDir()
	negRep, err := RunGatedPipeline(ctx, tmpDir, true)
	if err != nil {
		t.Fatalf("expected pipeline to return report, not err: %v", err)
	}
	if negRep.Status != StatusRejected {
		t.Errorf("expected StatusRejected for empty dir, got %s", negRep.Status)
	}

	// Boundary: Nil Context
	_, nilErr := RunGatedPipeline(nil, repoRoot, true)
	if nilErr == nil {
		t.Error("expected error for nil context, got nil")
	}
}
