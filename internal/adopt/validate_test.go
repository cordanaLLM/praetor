package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeLeafGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD failed: %v", err)
	}
}

func TestValidateAdoptionTarget_Positive(t *testing.T) {
	tempDir := t.TempDir()
	writeLeafGit(t, tempDir)
	if err := ValidateAdoptionTarget(tempDir); err != nil {
		t.Fatalf("expected valid repo to pass, got: %v", err)
	}
}

func TestValidateAdoptionTarget_WorktreeGitLink(t *testing.T) {
	tempDir := t.TempDir()
	gitLink := filepath.Join(tempDir, ".git")
	if err := os.WriteFile(gitLink, []byte("gitdir: /path/to/parent/.git/worktrees/child\n"), 0o644); err != nil {
		t.Fatalf("write .git link failed: %v", err)
	}
	if err := ValidateAdoptionTarget(tempDir); err != nil {
		t.Fatalf("expected worktree gitlink to pass, got: %v", err)
	}
}

func TestValidateAdoptionTarget_Positive_RepoNamedDev(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	repo := filepath.Join(t.TempDir(), "src", "dev")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLeafGit(t, repo)
	if err := ValidateAdoptionTarget(repo); err != nil {
		t.Fatalf("a repository that merely is named dev must be adoptable, got: %v", err)
	}
}

func TestValidateAdoptionTarget_Positive_SubmoduleGitlinkChild(t *testing.T) {
	tempDir := t.TempDir()
	writeLeafGit(t, tempDir)
	sub := filepath.Join(tempDir, "third_party", "foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../../.git/modules/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The submodule sits one level below the root; also cover a top-level gitlink child.
	top := filepath.Join(tempDir, "vendored")
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, ".git"), []byte("gitdir: ../.git/modules/vendored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAdoptionTarget(tempDir); err != nil {
		t.Fatalf("submodule gitlinks belong to the leaf repository, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeNonDir(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "regular_file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := ValidateAdoptionTarget(filePath); !errors.Is(err, ErrTargetNotDirectory) {
		t.Fatalf("expected ErrTargetNotDirectory, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeNotGitRepo(t *testing.T) {
	if err := ValidateAdoptionTarget(t.TempDir()); !errors.Is(err, ErrTargetNotGitRepo) {
		t.Fatalf("expected ErrTargetNotGitRepo, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeMissingHead(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}
	if err := ValidateAdoptionTarget(tempDir); !errors.Is(err, ErrTargetNotGitRepo) {
		t.Fatalf("expected ErrTargetNotGitRepo for missing HEAD, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeContainsChildRepos(t *testing.T) {
	tempDir := t.TempDir()
	writeLeafGit(t, tempDir)
	if err := os.MkdirAll(filepath.Join(tempDir, "child-repo", ".git"), 0o755); err != nil {
		t.Fatalf("mkdir child .git failed: %v", err)
	}
	if err := ValidateAdoptionTarget(tempDir); !errors.Is(err, ErrOrgContainerWithSubRepos) {
		t.Fatalf("expected ErrOrgContainerWithSubRepos, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeDevWorkstationRoot(t *testing.T) {
	// A dev directory that is not a repository is a workstation root wherever it lives.
	devRoot := filepath.Join(t.TempDir(), "dev")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
		t.Fatalf("mkdir dev failed: %v", err)
	}
	if err := ValidateAdoptionTarget(devRoot); !errors.Is(err, ErrTargetIsWorkstationRoot) {
		t.Fatalf("expected ErrTargetIsWorkstationRoot, got: %v", err)
	}

	// $HOME/dev is rejected even when it carries a .git of its own.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	homeDev := filepath.Join(home, "dev")
	if err := os.MkdirAll(homeDev, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLeafGit(t, homeDev)
	if err := ValidateAdoptionTarget(homeDev); !errors.Is(err, ErrTargetIsWorkstationRoot) {
		t.Fatalf("expected ErrTargetIsWorkstationRoot for $HOME/dev, got: %v", err)
	}
	if err := ValidateAdoptionTarget(home); !errors.Is(err, ErrTargetIsWorkstationRoot) {
		t.Fatalf("expected ErrTargetIsWorkstationRoot for $HOME, got: %v", err)
	}
}

func TestValidateAdoptionTarget_NegativeOrgDirectory(t *testing.T) {
	orgDir := filepath.Join(t.TempDir(), "dev", "vmafx")
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatalf("mkdir dev/vmafx failed: %v", err)
	}
	if err := ValidateAdoptionTarget(orgDir); !errors.Is(err, ErrTargetIsOrgDirectory) {
		t.Fatalf("expected ErrTargetIsOrgDirectory, got: %v", err)
	}
	// The same name is a repository when it carries .git, and then adoptable.
	writeLeafGit(t, orgDir)
	if err := ValidateAdoptionTarget(orgDir); err != nil {
		t.Fatalf("a repository named like an org must be adoptable, got: %v", err)
	}
}

func TestValidateAdoptionTarget_Boundary_EmptyAndTrailingSlash(t *testing.T) {
	repo := t.TempDir()
	writeLeafGit(t, repo)
	t.Chdir(repo)
	if err := ValidateAdoptionTarget(""); err != nil {
		t.Fatalf("empty path resolves to the working directory, got: %v", err)
	}
	if err := ValidateAdoptionTarget(repo + string(os.PathSeparator)); err != nil {
		t.Fatalf("trailing separator must be tolerated, got: %v", err)
	}
	if err := ValidateAdoptionTarget(filepath.Join(repo, "..", filepath.Base(repo))); err != nil {
		t.Fatalf("unclean path must be tolerated, got: %v", err)
	}
}
