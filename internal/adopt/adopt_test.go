package adopt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestAdopt_Positive_Greenfield(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "new-service")
	_ = os.MkdirAll(repoPath, 0755)
	_ = os.WriteFile(filepath.Join(repoPath, "go.mod"), []byte("module github.com/test/service\n"), 0644)

	opts := AdoptOptions{
		Path:    repoPath,
		Profile: "framework",
		DryRun:  false,
	}

	report, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt greenfield failed: %v", err)
	}

	if report.State != StateGreenfield {
		t.Fatalf("expected state greenfield, got: %s", report.State)
	}
	if report.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", report.Archetype)
	}
	if len(report.CreatedFiles) == 0 {
		t.Fatal("expected created files in report")
	}

	// Verify files physically exist
	expectedFiles := []string{
		".standards.yaml",
		".standards.lock",
		".standards-baseline.json",
		"AGENTS.md",
		"CLAUDE.md",
		".devcontainer/devcontainer.json",
		"Makefile",
		".gitignore",
	}
	for _, ef := range expectedFiles {
		p := filepath.Join(repoPath, ef)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Fatalf("expected file %s to exist after greenfield adoption", ef)
		}
	}
}

func TestAdopt_Positive_PartialAndDryRun(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "partial-repo")
	_ = os.MkdirAll(repoPath, 0755)
	_ = os.WriteFile(filepath.Join(repoPath, ".standards.yaml"), []byte("version: 1\n"), 0644)

	// Dry run
	optsDry := AdoptOptions{
		Path:   repoPath,
		DryRun: true,
	}
	repDry, err := Adopt(ctx, optsDry)
	if err != nil {
		t.Fatalf("Adopt dry-run failed: %v", err)
	}
	if repDry.State != StatePartial {
		t.Fatalf("expected partial state, got: %s", repDry.State)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); !os.IsNotExist(err) {
		t.Fatal("dry-run must not create .standards.lock")
	}

	// Live run
	optsLive := AdoptOptions{
		Path:   repoPath,
		DryRun: false,
	}
	repLive, err := Adopt(ctx, optsLive)
	if err != nil {
		t.Fatalf("Adopt live failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); os.IsNotExist(err) {
		t.Fatal("live adopt should create missing .standards.lock")
	}
	if len(repLive.ReconciledFiles) == 0 {
		t.Fatal("expected reconciled files in report")
	}
}

func TestAdopt_Positive_BrownfieldWithDebtRatcheting(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "legacy-repo")
	_ = os.MkdirAll(repoPath, 0755)

	// Simulate legacy files with HISS violation
	legacyCode := "package main\nfunc run() {\n\t_ = doSomething()\n}\n"
	_ = os.WriteFile(filepath.Join(repoPath, "main.go"), []byte(legacyCode), 0644)

	opts := AdoptOptions{
		Path:           repoPath,
		RecordBaseline: true,
		DryRun:         false,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt brownfield failed: %v", err)
	}

	if rep.LegacyDebtCount != 1 {
		t.Fatalf("expected 1 legacy infraction recorded, got: %d", rep.LegacyDebtCount)
	}

	// Check baseline file content
	data, err := os.ReadFile(filepath.Join(repoPath, ".standards-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("baseline file should not be empty")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestAdopt_Negative_NilContext(t *testing.T) {
	opts := AdoptOptions{Path: "/tmp"}
	_, err := Adopt(nil, opts)
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestAdopt_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := AdoptOptions{Path: "/tmp"}
	_, err := Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAdopt_Negative_NonExistentPath(t *testing.T) {
	ctx := context.Background()
	opts := AdoptOptions{Path: "/tmp/this/path/absolutely/does/not/exist/ever"}
	_, err := Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error with non-existent path")
	}
}

func TestAdopt_Negative_PathIsFileNotDir(t *testing.T) {
	ctx := context.Background()
	tmpFile, err := os.CreateTemp("", "adopt_test_file_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	opts := AdoptOptions{Path: tmpFile.Name()}
	_, err = Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error when path is a regular file")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestAdopt_Boundary_EmptyRepoPathDefaults(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	emptyRepo := filepath.Join(tmpDir, "empty")
	_ = os.MkdirAll(emptyRepo, 0755)

	opts := AdoptOptions{
		Path:   emptyRepo,
		DryRun: true,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt on empty repo failed: %v", err)
	}

	if rep.Archetype != "template-seed" {
		t.Fatalf("expected template-seed archetype for empty dir, got: %s", rep.Archetype)
	}
	if len(rep.Facets) != 4 {
		t.Fatalf("expected 4 default facets, got: %d", len(rep.Facets))
	}
}

func TestDetectState_Boundary(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Completely empty directory
	state1 := DetectState(tmpDir)
	if state1 != StateGreenfield {
		t.Fatalf("expected greenfield for empty dir, got: %s", state1)
	}

	// 2. Only AGENTS.md exists
	_ = os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("rules"), 0644)
	state2 := DetectState(tmpDir)
	if state2 != StatePartial {
		t.Fatalf("expected partial for dir with only AGENTS.md, got: %s", state2)
	}

	// 3. All files exist
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards.yaml"), []byte("manifest"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards.lock"), []byte("lock"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards-baseline.json"), []byte("{}"), 0644)
	state3 := DetectState(tmpDir)
	if state3 != StateBrownfield {
		t.Fatalf("expected brownfield for dir with all files, got: %s", state3)
	}
}
