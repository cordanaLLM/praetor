package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PathExists returns true if path exists on the filesystem.
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileExists returns true if path exists and is not a directory.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DirExists returns true if path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// CleanGitURL normalizes git remote URLs (SSH, HTTPS, git://).
func CleanGitURL(url string) string {
	trimmed := strings.TrimSpace(url)
	trimmed = strings.TrimSuffix(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	return strings.TrimSuffix(trimmed, "/")
}

// ExtractOwnerAndRepo returns owner and repository name from a git URL.
func ExtractOwnerAndRepo(url string) (owner, repo string) {
	trimmed := CleanGitURL(url)
	if trimmed == "" {
		return "", ""
	}
	// Check SSH-style remote e.g. git@github.com:owner/repo
	if idx := strings.LastIndex(trimmed, ":"); idx != -1 && !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		pathPart := trimmed[idx+1:]
		parts := strings.Split(pathPart, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2], parts[len(parts)-1]
		}
		if len(parts) == 1 && parts[0] != "" {
			return "", parts[0]
		}
	}
	parts := strings.Split(trimmed, "/")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) >= 2 {
		return filtered[len(filtered)-2], filtered[len(filtered)-1]
	}
	if len(filtered) == 1 {
		return "", filtered[0]
	}
	return "", ""
}

// RunCommand executes a command with context timeout and returns trimmed output.
func RunCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RunGit executes a git command with context timeout and returns trimmed output.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	return RunCommand(ctx, dir, "git", args...)
}

// ResolveRepoIdentity extracts owner and repository name using git remote or directory path.
func ResolveRepoIdentity(ctx context.Context, repoPath string) (owner, repo string, err error) {
	out, gitErr := RunGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if gitErr == nil && out != "" {
		o, r := ExtractOwnerAndRepo(out)
		if r != "" {
			if o == "" {
				o = "cordanaLLM"
			}
			return o, r, nil
		}
	}

	normPath := filepath.Clean(repoPath)
	repo = filepath.Base(normPath)
	parent := filepath.Base(filepath.Dir(normPath))
	if parent != "" && parent != "." && parent != "/" && parent != "dev" {
		owner = parent
	} else {
		owner = "cordanaLLM"
	}
	return owner, repo, nil
}

// ResolveAuthToken resolves an authentication token from explicit string, environment variables, or gh CLI session.
func ResolveAuthToken(explicitToken string) string {
	if explicitToken != "" {
		return explicitToken
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	if out, err := RunCommand(context.Background(), "", "gh", "auth", "token"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}
