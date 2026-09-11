package adopt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrTargetNotDirectory indicates the path is not a directory.
	ErrTargetNotDirectory = errors.New("target path must be an existing directory")
	// ErrTargetNotGitRepo indicates the target is not a leaf git repository.
	ErrTargetNotGitRepo = errors.New("target is not a git repository (missing .git)")
	// ErrTargetIsWorkstationRoot indicates target is the top-level dev workspace root.
	ErrTargetIsWorkstationRoot = errors.New("target path is the workstation dev root; adopting dev workspace root is strictly prohibited (DEV-01)")
	// ErrTargetIsOrgDirectory indicates target is a recognized GitHub organization container.
	ErrTargetIsOrgDirectory = errors.New("target path is an organization container directory; repositories must live within org directories, not on org roots (DEV-01)")
	// ErrOrgContainerWithSubRepos indicates the directory contains child git repositories.
	ErrOrgContainerWithSubRepos = errors.New("target directory contains child git repositories; adoption must target individual leaf repositories")
)

var knownOrgNames = map[string]bool{
	"cordanallm": true,
	"lusoris":    true,
	"vmafx":      true,
	"golusoris":  true,
	"upstream":   true,
	"local":      true,
	"stacks":     true,
	"worktrees":  true,
	"scratch":    true,
}

// ValidateAdoptionTarget verifies that the target path is a genuine, individual leaf Git repository.
func ValidateAdoptionTarget(targetPath string) error {
	normPath, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolve repo path %q: %w", targetPath, err)
	}

	info, err := os.Stat(normPath)
	if err != nil || !info.IsDir() {
		return ErrTargetNotDirectory
	}

	if err := checkWorkstationBoundaries(normPath); err != nil {
		return err
	}

	if err := checkGitRepositoryRoot(normPath); err != nil {
		return err
	}

	return checkForChildRepositories(normPath)
}

func checkWorkstationBoundaries(normPath string) error {
	cleanPath := filepath.Clean(normPath)
	baseName := strings.ToLower(filepath.Base(cleanPath))

	// Reject dev workspace root
	if strings.HasSuffix(cleanPath, "/dev") || baseName == "dev" {
		return ErrTargetIsWorkstationRoot
	}

	// Reject user home root
	if home, err := os.UserHomeDir(); err == nil && cleanPath == filepath.Clean(home) {
		return ErrTargetIsWorkstationRoot
	}

	// Reject organization directory
	parentBase := strings.ToLower(filepath.Base(filepath.Dir(cleanPath)))
	if parentBase == "dev" && knownOrgNames[baseName] {
		return fmt.Errorf("%w: %s", ErrTargetIsOrgDirectory, normPath)
	}

	return nil
}

func checkGitRepositoryRoot(normPath string) error {
	gitEntry := filepath.Join(normPath, ".git")
	gitInfo, err := os.Stat(gitEntry)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrTargetNotGitRepo, normPath)
	}

	// .git can be a directory or a gitlink file (worktree or submodule)
	if !gitInfo.IsDir() && gitInfo.Mode().IsRegular() {
		return nil
	}
	if !gitInfo.IsDir() {
		return fmt.Errorf("%w: %s is not valid git metadata", ErrTargetNotGitRepo, gitEntry)
	}

	// Verify it contains a HEAD file to confirm valid git repo, not stray hooks
	headEntry := filepath.Join(gitEntry, "HEAD")
	if _, err := os.Stat(headEntry); err != nil {
		return fmt.Errorf("%w: %s missing HEAD file", ErrTargetNotGitRepo, normPath)
	}

	return nil
}

func checkForChildRepositories(normPath string) error {
	entries, err := os.ReadDir(normPath)
	if err != nil {
		return fmt.Errorf("read target dir %q: %w", normPath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childGit := filepath.Join(normPath, entry.Name(), ".git")
		if info, err := os.Stat(childGit); err == nil {
			if info.IsDir() || info.Mode().IsRegular() {
				return fmt.Errorf("%w: found child repository %s inside %s",
					ErrOrgContainerWithSubRepos, entry.Name(), normPath)
			}
		}
	}

	return nil
}
