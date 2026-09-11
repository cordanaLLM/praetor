package harvester

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupMockWorkstation(mockHome, mockVault string) {
	geminiSkill := filepath.Join(mockHome, ".gemini", "config", "skills", "test-skill")
	_ = os.MkdirAll(geminiSkill, 0755)
	_ = os.WriteFile(filepath.Join(geminiSkill, "SKILL.md"), []byte("# Test Skill"), 0644)

	claudeMem := filepath.Join(mockHome, ".claude", "projects", "proj1", "memory")
	_ = os.MkdirAll(claudeMem, 0755)
	_ = os.WriteFile(filepath.Join(claudeMem, "MEMORY.md"), []byte("# Memory"), 0644)

	vaultPatches := filepath.Join(mockVault, "dev-patches")
	_ = os.MkdirAll(vaultPatches, 0755)
	_ = os.WriteFile(filepath.Join(vaultPatches, "test.patch"), []byte("diff --git a b"), 0644)

	mockCodex := filepath.Join(mockHome, ".codex")
	_ = os.MkdirAll(mockCodex, 0755)
	_ = os.WriteFile(filepath.Join(mockCodex, "config.toml"), []byte("model = 'o3'"), 0644)

	mockCopilot := filepath.Join(mockHome, ".copilot")
	_ = os.MkdirAll(mockCopilot, 0755)
	_ = os.WriteFile(filepath.Join(mockCopilot, "config.json"), []byte("{}"), 0644)
}

func TestBundleWorkstation_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	mockHome := filepath.Join(tmpDir, "home")
	mockOut := filepath.Join(tmpDir, "out")
	mockVault := filepath.Join(tmpDir, "vault")

	setupMockWorkstation(mockHome, mockVault)

	opts := BundleOptions{
		WorkstationName: "test-box",
		OutputDir:       mockOut,
		HomeDir:         mockHome,
		VaultDir:        mockVault,
	}

	rep, err := BundleWorkstation(ctx, opts)
	if err != nil {
		t.Fatalf("BundleWorkstation failed: %v", err)
	}

	if rep.TotalFiles != 5 {
		t.Fatalf("expected 5 bundled files, got: %d", rep.TotalFiles)
	}
	if rep.WorkstationName != "test-box" {
		t.Fatalf("expected test-box, got: %s", rep.WorkstationName)
	}
	if _, err := os.Stat(rep.ManifestPath); os.IsNotExist(err) {
		t.Fatalf("manifest not created at: %s", rep.ManifestPath)
	}

	// Test Ingest
	ingest, err := IngestBundle(ctx, mockOut, filepath.Join(mockHome, "existing"), true)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}
	if len(ingest.NovelSkills) != 1 || ingest.NovelSkills[0] != "test-skill" {
		t.Fatalf("expected novel skill 'test-skill', got: %v", ingest.NovelSkills)
	}
}

func TestBundleWorkstation_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmpDir := t.TempDir()
	opts := BundleOptions{
		WorkstationName: "test-box",
		OutputDir:       tmpDir,
		HomeDir:         tmpDir,
	}

	_, err := BundleWorkstation(ctx, opts)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestBundleWorkstation_Boundary_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	opts := BundleOptions{
		WorkstationName: "empty-box",
		OutputDir:       filepath.Join(tmpDir, "out"),
		HomeDir:         filepath.Join(tmpDir, "nonexistent"),
	}

	rep, err := BundleWorkstation(ctx, opts)
	if err != nil {
		t.Fatalf("expected no error with empty inputs, got: %v", err)
	}
	if rep.TotalFiles != 0 {
		t.Fatalf("expected 0 files, got: %d", rep.TotalFiles)
	}
}
