package harvester

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestDetectArchetype_Positive(t *testing.T) {
	gpuArch := DetectArchetype("C", "A high-performance Vulkan GPU pre-encode filter engine")
	if gpuArch != "native-gpu-systems" {
		t.Fatalf("expected native-gpu-systems, got: %s", gpuArch)
	}

	k8sArch := DetectArchetype("Python", "Kubernetes cluster GitOps deployment with ArgoCD")
	if k8sArch != "gitops-infra" {
		t.Fatalf("expected gitops-infra, got: %s", k8sArch)
	}

	fwArch := DetectArchetype("Go", "Composable Go framework with opt-in fx modules")
	if fwArch != "framework" {
		t.Fatalf("expected framework, got: %s", fwArch)
	}
}

func TestScanLocalWorkstation_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Mock repo A with AGENTS.md
	repoA := filepath.Join(tmpDir, "repo-a")
	if err := os.MkdirAll(filepath.Join(repoA, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoA, "AGENTS.md"), []byte("# Rules"), 0644); err != nil {
		t.Fatal(err)
	}

	// Mock repo B missing rules
	repoB := filepath.Join(tmpDir, "repo-b")
	if err := os.MkdirAll(filepath.Join(repoB, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Mock worktree dir
	wtDir := filepath.Join(tmpDir, "k8s-worktrees", "task-123")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation failed: %v", err)
	}

	if rep.DevReposCount != 2 {
		t.Fatalf("expected 2 dev repos, got: %d", rep.DevReposCount)
	}
	if len(rep.MissingRulesRepos) != 1 || rep.MissingRulesRepos[0] != "repo-b" {
		t.Fatalf("expected repo-b in missing rules, got: %v", rep.MissingRulesRepos)
	}
	if len(rep.StaleWorktrees) != 1 {
		t.Fatalf("expected 1 stale worktree, got: %v", rep.StaleWorktrees)
	}
}

func TestAuditSkills_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	geminiSkills := filepath.Join(tmpDir, "skills", "skill-a")
	configSkills := filepath.Join(tmpDir, "config", "skills", "skill-a")
	if err := os.MkdirAll(geminiSkills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configSkills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(geminiSkills, "SKILL.md"), []byte("skill a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configSkills, "SKILL.md"), []byte("skill a"), 0644); err != nil {
		t.Fatal(err)
	}

	// Stale backup
	if err := os.WriteFile(filepath.Join(tmpDir, "GEMINI.md.bak-12345"), []byte("backup"), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := AuditSkills(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("AuditSkills failed: %v", err)
	}

	if rep.TotalSkills != 2 {
		t.Fatalf("expected 2 total skills, got: %d", rep.TotalSkills)
	}
	if rep.UniqueSkills != 1 {
		t.Fatalf("expected 1 unique skill, got: %d", rep.UniqueSkills)
	}
	if len(rep.Duplicates["skill-a"]) != 2 {
		t.Fatalf("expected duplicate for skill-a, got: %v", rep.Duplicates)
	}
	if len(rep.StaleBackups) != 1 {
		t.Fatalf("expected 1 stale backup, got: %v", rep.StaleBackups)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestScanLocalWorkstation_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ScanLocalWorkstation(ctx, "/tmp")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestScanLocalWorkstation_Negative_InvalidDir(t *testing.T) {
	ctx := context.Background()
	_, err := ScanLocalWorkstation(ctx, "/nonexistent/dev/dir/test")
	if err == nil {
		t.Fatal("expected error on nonexistent directory")
	}
}

func TestAuditSkills_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := AuditSkills(ctx, "/tmp", "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestDetectArchetype_Boundary_EmptyInputs(t *testing.T) {
	arch := DetectArchetype("", "")
	if arch != "template-seed" {
		t.Fatalf("expected template-seed for empty inputs, got: %s", arch)
	}

	arch2 := DetectArchetype("UnknownLang", "Just an ordinary project")
	if arch2 != "template-seed" {
		t.Fatalf("expected template-seed for unknown inputs, got: %s", arch2)
	}
}

func TestScanLocalWorkstation_Boundary_EmptyDir(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation on empty dir failed: %v", err)
	}
	if rep.DevReposCount != 0 {
		t.Fatalf("expected 0 repos in empty dir, got: %d", rep.DevReposCount)
	}
	if len(rep.StaleWorktrees) != 0 {
		t.Fatalf("expected 0 stale worktrees, got: %d", len(rep.StaleWorktrees))
	}
}

func TestDeduplicateSkills_PositiveAndDryRun(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	geminiSkills := filepath.Join(tmpDir, ".gemini", "skills", "my-skill")
	configSkills := filepath.Join(tmpDir, ".gemini", "config", "skills", "my-skill")
	_ = os.MkdirAll(geminiSkills, 0755)
	_ = os.MkdirAll(configSkills, 0755)
	_ = os.WriteFile(filepath.Join(geminiSkills, "SKILL.md"), []byte("data"), 0644)
	_ = os.WriteFile(filepath.Join(configSkills, "SKILL.md"), []byte("data"), 0644)

	rep, err := AuditSkills(ctx, filepath.Join(tmpDir, ".gemini"), "")
	if err != nil {
		t.Fatal(err)
	}

	// Dry-run
	dRep, err := DeduplicateSkills(ctx, rep, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dRep.PrunedSkills) != 1 {
		t.Fatalf("expected 1 pruned skill in dry-run, got: %d", len(dRep.PrunedSkills))
	}
	if _, err := os.Stat(geminiSkills); os.IsNotExist(err) {
		t.Fatal("dry-run should not delete files")
	}

	// Live
	dRepLive, err := DeduplicateSkills(ctx, rep, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dRepLive.PrunedSkills) != 1 {
		t.Fatalf("expected 1 pruned skill in live, got: %d", len(dRepLive.PrunedSkills))
	}
	if _, err := os.Stat(geminiSkills); !os.IsNotExist(err) {
		t.Fatal("live deduplication should remove redundant skill folder")
	}
	if _, err := os.Stat(configSkills); os.IsNotExist(err) {
		t.Fatal("canonical config skill folder must be preserved")
	}
}

func TestPurgeBackups_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	backupFile := filepath.Join(tmpDir, "GEMINI.md.bak-999")
	_ = os.WriteFile(backupFile, []byte("backup"), 0644)

	// Dry run
	purged, err := PurgeBackups(ctx, tmpDir, []string{"GEMINI.md.bak-999"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 {
		t.Fatalf("expected 1 purged file in dry-run, got: %d", len(purged))
	}
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		t.Fatal("dry-run should not delete backup file")
	}

	// Live
	_, err = PurgeBackups(ctx, tmpDir, []string{"GEMINI.md.bak-999"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Fatal("live purge should delete backup file")
	}
}

func TestOnboardRepository_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "sample-repo")
	_ = os.MkdirAll(repoPath, 0755)
	_ = os.WriteFile(filepath.Join(repoPath, "go.mod"), []byte("module sample\n"), 0644)

	// Dry run
	plan, err := OnboardRepository(ctx, repoPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", plan.Archetype)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.yaml")); !os.IsNotExist(err) {
		t.Fatal("dry-run should not create .standards.yaml")
	}

	// Live
	planLive, err := OnboardRepository(ctx, repoPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(planLive.Actions) == 0 {
		t.Fatal("expected actions in live onboarding plan")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.yaml")); os.IsNotExist(err) {
		t.Fatal("live onboarding should create .standards.yaml")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards-baseline.json")); os.IsNotExist(err) {
		t.Fatal("live onboarding should create .standards-baseline.json")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); os.IsNotExist(err) {
		t.Fatal("live onboarding should create .standards.lock")
	}
}
