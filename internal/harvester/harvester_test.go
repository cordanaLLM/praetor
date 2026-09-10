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
