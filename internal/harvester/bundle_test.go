package harvester

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func createMockBundleRecords(bundleDir string) []BundleFileRecord {
	skillFiles := []string{
		"agent-skills/copilot/novel-skill/SKILL.md",
		"agent-skills/copilot/novel-skill/LICENSE.txt",
		"agent-skills/codex/.system/.marker",
		"agent-skills/gemini/existing-skill/SKILL.md",
		"agent-memories/claude/p1/mem.md",
		"agent-memories/claude/p1/mem.md",
		"dev-patches/fix.patch",
		"dev-patches/fix.patch",
	}
	records := make([]BundleFileRecord, 0, len(skillFiles))
	for _, rel := range skillFiles {
		fullPath := filepath.Join(bundleDir, rel)
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, []byte("content"), 0644)

		cat := "skill"
		if strings.Contains(rel, "memories") {
			cat = "project-memory"
		} else if strings.Contains(rel, "patches") {
			cat = "patch"
		}
		records = append(records, BundleFileRecord{
			RelativePath: rel,
			SizeBytes:    7,
			Category:     cat,
		})
	}
	return records
}

func TestIngestBundle_DeduplicationAndSystemFilter(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	bundleDir := filepath.Join(tmpDir, "bundle")
	localSkillsDir := filepath.Join(tmpDir, "local-skills")
	_ = os.MkdirAll(bundleDir, 0755)
	_ = os.MkdirAll(filepath.Join(localSkillsDir, "existing-skill"), 0755)

	records := createMockBundleRecords(bundleDir)
	report := WorkstationBundleReport{
		WorkstationName: "mock-box",
		Records:         records,
	}
	data, _ := json.Marshal(report)
	_ = os.WriteFile(filepath.Join(bundleDir, "manifest.json"), data, 0644)

	ingest, err := IngestBundle(ctx, bundleDir, localSkillsDir, false)
	if err != nil {
		t.Fatalf("IngestBundle failed: %v", err)
	}

	if len(ingest.NovelSkills) != 1 || ingest.NovelSkills[0] != "novel-skill" {
		t.Fatalf("expected 1 novel skill 'novel-skill', got: %v", ingest.NovelSkills)
	}
	if len(ingest.ExistingSkills) != 1 || ingest.ExistingSkills[0] != "existing-skill" {
		t.Fatalf("expected 1 existing skill 'existing-skill', got: %v", ingest.ExistingSkills)
	}
	if len(ingest.NovelMemories) != 1 || len(ingest.NovelPatches) != 1 {
		t.Fatalf("expected 1 deduped memory and patch, got %d and %d", len(ingest.NovelMemories), len(ingest.NovelPatches))
	}

	copiedSkillMD := filepath.Join(localSkillsDir, "novel-skill", "SKILL.md")
	if _, err := os.Stat(copiedSkillMD); os.IsNotExist(err) {
		t.Fatalf("expected copied file at %s", copiedSkillMD)
	}
}
