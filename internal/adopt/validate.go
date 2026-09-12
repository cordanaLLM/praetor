package adopt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxChildEntries bounds the child-repository scan of an adoption target (HISS-02).
const maxChildEntries = 4096

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

// hasGitEntry reports whether dir carries a .git directory or gitlink of its own.
func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// checkWorkstationBoundaries rejects the user's home directory, the workstation dev
// root under it, and organization container directories. A directory that merely
// happens to be named "dev" is only rejected when it is not a repository itself, so a
// clone named dev under any other parent remains adoptable.
func checkWorkstationBoundaries(normPath string) error {
	cleanPath := filepath.Clean(normPath)
	baseName := strings.ToLower(filepath.Base(cleanPath))

	if home, err := os.UserHomeDir(); err == nil {
		home = filepath.Clean(home)
		if cleanPath == home || cleanPath == filepath.Join(home, "dev") {
			return ErrTargetIsWorkstationRoot
		}
	}

	isRepo := hasGitEntry(cleanPath)
	if baseName == "dev" && !isRepo {
		return ErrTargetIsWorkstationRoot
	}

	parentBase := strings.ToLower(filepath.Base(filepath.Dir(cleanPath)))
	if parentBase == "dev" && knownOrgNames[baseName] && !isRepo {
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

// checkForChildRepositories rejects a target whose immediate children are independent
// git repositories (an organization container). A child whose .git is a regular file
// is a gitlink (an initialized submodule or linked worktree) that belongs to the target
// itself and does not make it a container.
func checkForChildRepositories(normPath string) error {
	entries, err := os.ReadDir(normPath)
	if err != nil {
		return fmt.Errorf("read target dir %q: %w", normPath, err)
	}

	for i := 0; i < len(entries) && i < maxChildEntries; i++ {
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}
		childGit := filepath.Join(normPath, entry.Name(), ".git")
		info, err := os.Stat(childGit)
		if err != nil || !info.IsDir() {
			continue
		}
		return fmt.Errorf("%w: found child repository %s inside %s",
			ErrOrgContainerWithSubRepos, entry.Name(), normPath)
	}

	return nil
}
