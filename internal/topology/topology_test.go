package topology

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func initTestGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create test git dir: %v", err)
	}
	headFile := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(headFile, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create test HEAD file: %v", err)
	}
}

func setupMockDevEnvironment(t *testing.T) string {
	t.Helper()
	devRoot := t.TempDir()

	// Dev root files
	if err := os.WriteFile(filepath.Join(devRoot, "AGENTS.md"), []byte("# Workstation"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "workstation.code-workspace"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// vmafx org container
	vmafxOrg := filepath.Join(devRoot, "vmafx")
	if err := os.MkdirAll(vmafxOrg, 0755); err != nil {
		t.Fatal(err)
	}

	// Child repos inside vmafx
	vmafxCore := filepath.Join(vmafxOrg, "vmafx")
	initTestGit(t, vmafxCore)
	pelorusRepo := filepath.Join(vmafxOrg, "pelorus")
	initTestGit(t, pelorusRepo)

	// Stray governance files inside vmafx org root
	strayFiles := []string{
		"AGENTS.md",
		".standards.yaml",
		".standards-baseline.json",
		"Makefile",
		"lefthook.yml",
		".needs.yaml",
	}
	for _, sf := range strayFiles {
		if err := os.WriteFile(filepath.Join(vmafxOrg, sf), []byte("stray"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Stray docs directory in vmafx
	if err := os.MkdirAll(filepath.Join(vmafxOrg, "docs", "adr"), 0755); err != nil {
		t.Fatal(err)
	}

	// golusoris org container with stray headless .git
	golusorisOrg := filepath.Join(devRoot, "golusoris")
	if err := os.MkdirAll(filepath.Join(golusorisOrg, ".git", "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	// Child repo inside golusoris
	goenvoyRepo := filepath.Join(golusorisOrg, "goenvoy")
	initTestGit(t, goenvoyRepo)

	// Symlink in dev root (DEV-02)
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if err := os.Symlink(pelorusRepo, symlinkPath); err != nil {
		t.Fatal(err)
	}

	return devRoot
}

func TestAuditWorkstationTopology_Positive(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report, err := AuditWorkstationTopology(ctx, devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology failed: %v", err)
	}

	if len(report.ValidRepos) != 3 {
		t.Errorf("expected 3 valid repos, got %d: %v", len(report.ValidRepos), report.ValidRepos)
	}

	if len(report.Symlinks) != 1 || report.Symlinks[0] != "pelorus" {
		t.Errorf("expected symlink 'pelorus', got %v", report.Symlinks)
	}

	// Verify stray files detected
	if len(report.StrayFiles) < 7 {
		t.Errorf("expected at least 7 stray files/dirs, got %d", len(report.StrayFiles))
	}

	foundHeadlessGit := false
	foundBaseline := false
	for _, sf := range report.StrayFiles {
		if filepath.Base(sf.Path) == ".git" {
			foundHeadlessGit = true
		}
		if filepath.Base(sf.Path) == ".standards-baseline.json" {
			foundBaseline = true
		}
	}

	if !foundHeadlessGit {
		t.Error("expected stray headless .git in golusoris to be detected")
	}
	if !foundBaseline {
		t.Error("expected stray .standards-baseline.json to be detected")
	}
}

func TestCleanWorkstationTopology_DryRun(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cleaned, err := CleanWorkstationTopology(ctx, devRoot, true)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology (dry run) failed: %v", err)
	}

	if len(cleaned) == 0 {
		t.Fatal("expected cleaned items in dry run, got 0")
	}

	// Verify files still exist on disk because it was dry-run
	strayBaseline := filepath.Join(devRoot, "vmafx", ".standards-baseline.json")
	if _, err := os.Stat(strayBaseline); os.IsNotExist(err) {
		t.Error("dry run must not delete files from disk")
	}
}

func TestCleanWorkstationTopology_Apply(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cleaned, err := CleanWorkstationTopology(ctx, devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology (apply) failed: %v", err)
	}

	if len(cleaned) == 0 {
		t.Fatal("expected cleaned items, got 0")
	}

	// Verify stray files are gone
	strayBaseline := filepath.Join(devRoot, "vmafx", ".standards-baseline.json")
	if _, err := os.Stat(strayBaseline); !os.IsNotExist(err) {
		t.Errorf("expected %s to be deleted", strayBaseline)
	}

	strayGit := filepath.Join(devRoot, "golusoris", ".git")
	if _, err := os.Stat(strayGit); !os.IsNotExist(err) {
		t.Errorf("expected stray .git %s to be deleted", strayGit)
	}

	// Verify child repos are 100% intact
	vmafxCoreGit := filepath.Join(devRoot, "vmafx", "vmafx", ".git", "HEAD")
	if _, err := os.Stat(vmafxCoreGit); err != nil {
		t.Errorf("child repo vmafx was corrupted: %v", err)
	}

	goenvoyGit := filepath.Join(devRoot, "golusoris", "goenvoy", ".git", "HEAD")
	if _, err := os.Stat(goenvoyGit); err != nil {
		t.Errorf("child repo goenvoy was corrupted: %v", err)
	}

	// Verify stray symlink in dev root was cleaned
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if isSymlink(symlinkPath) {
		t.Errorf("stray symlink %s should have been removed", symlinkPath)
	}
}

func TestAuditWorkstationTopology_Negative_NonExistent(t *testing.T) {
	ctx := context.Background()
	_, err := AuditWorkstationTopology(ctx, "/nonexistent/path/for/test")
	if err != ErrDevRootNotExist {
		t.Errorf("expected ErrDevRootNotExist, got %v", err)
	}
}

func TestAuditWorkstationTopology_Negative_NotADir(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "somefile.txt")
	if err := os.WriteFile(tmpFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err := AuditWorkstationTopology(ctx, tmpFile)
	if err != ErrDevRootNotDir {
		t.Errorf("expected ErrDevRootNotDir, got %v", err)
	}
}

func TestAuditWorkstationTopology_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AuditWorkstationTopology(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestCleanWorkstationTopology_Boundary_EmptyDevRoot(t *testing.T) {
	devRoot := t.TempDir()
	ctx := context.Background()
	cleaned, err := CleanWorkstationTopology(ctx, devRoot, false)
	if err != nil {
		t.Fatalf("unexpected error on empty dev root: %v", err)
	}
	if len(cleaned) != 0 {
		t.Errorf("expected 0 cleaned items on empty dev root, got %d", len(cleaned))
	}
}

func TestVerifyDeletionSafety_Protections(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "vmafx")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Cannot delete dev root
	if err := verifyDeletionSafety(devRoot, devRoot); err == nil {
		t.Error("expected safety check to reject dev root deletion")
	}

	// 2. Cannot delete org container itself
	if err := verifyDeletionSafety(devRoot, orgDir); err == nil {
		t.Error("expected safety check to reject org container deletion")
	}

	// 3. Symlink unlinking is permitted
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if err := os.Symlink(orgDir, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, symlinkPath); err != nil {
		t.Errorf("expected safety check to allow symlink deletion, got %v", err)
	}

	// 4. Cannot delete directory with valid git repo
	childRepo := filepath.Join(orgDir, "vmafx")
	initTestGit(t, childRepo)
	if err := verifyDeletionSafety(devRoot, childRepo); err == nil {
		t.Error("expected safety check to reject directory with valid git repo")
	}
}
