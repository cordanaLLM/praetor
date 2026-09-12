package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultCommandTimeout bounds any subprocess started through RunCommand whose
	// context does not already carry a deadline (HISS-02).
	DefaultCommandTimeout = 30 * time.Second
	// DefaultAuthTokenTimeout bounds the gh CLI token lookup, which can block on a
	// locked OS keyring.
	DefaultAuthTokenTimeout = 15 * time.Second
	// CommandWaitDelay bounds how long RunCommand waits for output pipes still held by
	// grandchildren after the context expired and the direct child was killed.
	CommandWaitDelay = 5 * time.Second
)

// ErrRepoIdentityUnresolved is returned by ResolveRepoIdentity when neither the git
// origin remote nor the directory layout identifies an owner and repository.
var ErrRepoIdentityUnresolved = errors.New("util: unable to resolve repository owner and name")

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
	if o, r, ok := extractSSHStyle(trimmed); ok {
		return o, r
	}
	return extractPathStyle(trimmed)
}

// extractSSHStyle handles scp-like remotes such as git@github.com:owner/repo.
func extractSSHStyle(trimmed string) (owner, repo string, ok bool) {
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return "", "", false
	}
	idx := strings.LastIndex(trimmed, ":")
	if idx == -1 {
		return "", "", false
	}
	parts := strings.Split(trimmed[idx+1:], "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1], true
	}
	if len(parts) == 1 && parts[0] != "" {
		return "", parts[0], true
	}
	return "", "", false
}

// extractPathStyle handles URL-shaped remotes by taking the last two path segments.
func extractPathStyle(trimmed string) (owner, repo string) {
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

// RunCommand executes a command and returns its trimmed combined output.
//
// HISS-02: the command always runs under a context that carries a deadline. A nil
// context, or a context without a deadline (context.Background/TODO), is given
// DefaultCommandTimeout, so no subprocess started through this helper can block forever.
func RunCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	ctx, cancel := ensureDeadline(ctx, DefaultCommandTimeout)
	defer cancel()

	// #nosec G204 -- RunCommand is the single audited exec entry point. The binary and
	// arguments are supplied by praetor's own call sites, never parsed from a shell
	// string; values originating from users or config must pass ValidateExecArg first.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = CommandWaitDelay
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ensureDeadline returns a context guaranteed to carry a deadline. A nil context or one
// without a deadline (context.Background/TODO) is given fallback; an existing deadline is
// preserved. The returned cancel func is always non-nil and must be called.
func ensureDeadline(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, fallback)
}

// RunGit executes a git command with context timeout and returns trimmed output.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	return RunCommand(ctx, dir, "git", args...)
}

// ResolveRepoIdentity extracts the owner and repository name from the configured origin
// remote, falling back to the <owner>/<repo> shape of the absolute directory path.
//
// It never invents an owner: when neither the remote nor the directory layout yields
// one, it returns ErrRepoIdentityUnresolved so that callers writing to a forge refuse to
// publish into a guessed repository.
func ResolveRepoIdentity(ctx context.Context, repoPath string) (owner, repo string, err error) {
	out, gitErr := RunGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if gitErr == nil && out != "" {
		o, r := ExtractOwnerAndRepo(out)
		if o != "" && r != "" {
			return o, r, nil
		}
	}

	absPath, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", "", fmt.Errorf("%w: resolve %q: %w", ErrRepoIdentityUnresolved, repoPath, absErr)
	}
	normPath := filepath.Clean(absPath)
	repo = filepath.Base(normPath)
	parent := filepath.Base(filepath.Dir(normPath))
	if !isIdentitySegment(repo) || !isIdentitySegment(parent) || parent == "dev" {
		return "", "", fmt.Errorf("%w: no origin remote in %q and the path lacks an <owner>/<repo> shape",
			ErrRepoIdentityUnresolved, repoPath)
	}
	return parent, repo, nil
}

// isIdentitySegment reports whether a path segment can serve as an owner or repo name.
func isIdentitySegment(segment string) bool {
	return segment != "" && segment != "." && segment != ".." && segment != string(filepath.Separator)
}

// ResolveAuthToken resolves an authentication token from an explicit string, the
// GITHUB_TOKEN / GH_TOKEN environment variables, or the gh CLI session, in that order.
//
// HISS-02: the gh invocation is bounded by DefaultAuthTokenTimeout. Prefer
// ResolveAuthTokenContext when the caller already owns a context.
func ResolveAuthToken(explicitToken string) string {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAuthTokenTimeout)
	defer cancel()
	return ResolveAuthTokenContext(ctx, explicitToken)
}

// ResolveAuthTokenContext resolves an authentication token under the caller's context.
// The gh CLI fallback inherits the context deadline, or DefaultCommandTimeout when the
// context carries none.
func ResolveAuthTokenContext(ctx context.Context, explicitToken string) string {
	if explicitToken != "" {
		return explicitToken
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	if out, err := RunCommand(ctx, "", "gh", "auth", "token"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}
