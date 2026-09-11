package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAdoptionTarget_Positive(t *testing.T) {
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("write HEAD failed: %v", err)
	}

	if err := ValidateAdoptionTarget(tempDir); err != nil {
		t.Fatalf("expected valid repo to pass, got: %v", err)
	}
}

func TestValidateAdoptionTarget_WorktreeGitLink(t *testing.T) {
	tempDir := t.TempDir()
	gitLink := filepath.Join(tempDir, ".git")
	if err := os.WriteFile(gitLink, []byte("gitdir: /path/to/parent/.git/worktrees/child\n"), 0644); err != nil {
		t.Fatalf("write .git link failed: %v", err)
	}

	if err := ValidateAdoptionTarget(tempDir); err != nil {
		t.Fatalf("expected worktree gitlink to pass, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeNonDir(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "regular_file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	err := ValidateAdoptionTarget(filePath)
	if !errors.Is(err, ErrTargetNotDirectory) {
		t.Fatalf("expected ErrTargetNotDirectory, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeNotGitRepo(t *testing.T) {
	tempDir := t.TempDir()

	err := ValidateAdoptionTarget(tempDir)
	if !errors.Is(err, ErrTargetNotGitRepo) {
		t.Fatalf("expected ErrTargetNotGitRepo, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeMissingHead(t *testing.T) {
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}

	err := ValidateAdoptionTarget(tempDir)
	if !errors.Is(err, ErrTargetNotGitRepo) {
		t.Fatalf("expected ErrTargetNotGitRepo for missing HEAD, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeContainsChildRepos(t *testing.T) {
	tempDir := t.TempDir()
	// Create parent .git so parent passes initial git check
	parentGit := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(parentGit, 0755); err != nil {
		t.Fatalf("mkdir parent .git failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("write parent HEAD failed: %v", err)
	}

	// Create child repo
	childRepo := filepath.Join(tempDir, "child-repo", ".git")
	if err := os.MkdirAll(childRepo, 0755); err != nil {
		t.Fatalf("mkdir child .git failed: %v", err)
	}

	err := ValidateAdoptionTarget(tempDir)
	if !errors.Is(err, ErrOrgContainerWithSubRepos) {
		t.Fatalf("expected ErrOrgContainerWithSubRepos, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeDevWorkstationRoot(t *testing.T) {
	tempDir := t.TempDir()
	devRoot := filepath.Join(tempDir, "dev")
	if err := os.MkdirAll(devRoot, 0755); err != nil {
		t.Fatalf("mkdir dev failed: %v", err)
	}

	err := ValidateAdoptionTarget(devRoot)
	if !errors.Is(err, ErrTargetIsWorkstationRoot) {
		t.Fatalf("expected ErrTargetIsWorkstationRoot, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeOrgDirectory(t *testing.T) {
	tempDir := t.TempDir()
	orgDir := filepath.Join(tempDir, "dev", "vmafx")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatalf("mkdir dev/vmafx failed: %v", err)
	}

	err := ValidateAdoptionTarget(orgDir)
	if !errors.Is(err, ErrTargetIsOrgDirectory) {
		t.Fatalf("expected ErrTargetIsOrgDirectory, got: %v", err)
	}
}
