package agenthook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// manifestName marks a governed repository root.
const manifestName = ".standards.yaml"

// maxRootBytes bounds the git answer; a path is far below it.
const maxRootBytes = 8192

// ResolveRoot finds the repository root of the workspace the payload names, falling back
// to workDir only when the payload carries none. An empty root with a nil error means
// "no repository"; every other failure is an error so the caller fails closed. The probe
// ignores inherited Git variables, so an ambient GIT_DIR cannot redirect the answer.
func ResolveRoot(ctx context.Context, workspaces []string, workDir string) (string, error) {
	dir := workDir
	if len(workspaces) > 0 && workspaces[0] != "" {
		dir = workspaces[0]
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("workspace %q is not an absolute path", boundReason(dir))
	}
	result, err := util.RunGitProbe(ctx, dir, maxRootBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && bytes.Contains(result.Stderr, []byte("not a git repository")) {
			return "", nil
		}
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root := strings.TrimSpace(string(result.Stdout))
	if root == "" {
		return "", errors.New("resolve workspace root: git returned no path")
	}
	return filepath.FromSlash(root), nil
}

// Governed reports whether root holds the standards manifest.
func Governed(root string) bool {
	return root != "" && util.FileExists(filepath.Join(root, manifestName))
}
