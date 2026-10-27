package agenthook

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// manifestName marks a governed repository root.
const manifestName = ".standards.yaml"

// maxRootBytes bounds the git answer; a path is far below it.
const maxRootBytes = 8192

// maxWorkspaces bounds the payload workspaces one evaluation probes (HISS-02); a payload
// naming more fails closed.
const maxWorkspaces = 16

// ResolveRoot finds the repository root the hook evaluates: among the workspaces the payload
// names, the first governed repository, else the first repository; workDir only when the
// payload names none. Every workspace counts, because a client may list a folder that is no
// repository first: AGY 1.4.2 puts each --add-dir folder before the folder it runs in, and
// reading only the first entry skipped the guard for the repository behind it. An empty
// root with a nil error means "no repository"; every other failure, in any workspace, is an
// error so the caller fails closed. The probe ignores inherited Git variables, so an
// ambient GIT_DIR cannot redirect the answer.
func ResolveRoot(ctx context.Context, workspaces []string, workDir string) (string, error) {
	dirs, err := payloadWorkspaces(workspaces)
	if err != nil {
		return "", err
	}
	if len(dirs) == 0 {
		dirs = []string{workDir}
	}
	first := ""
	for i := 0; i < len(dirs); i++ {
		root, err := repositoryRoot(ctx, dirs[i])
		if err != nil {
			return "", err
		}
		if Governed(root) {
			return root, nil
		}
		if first == "" {
			first = root
		}
	}
	return first, nil
}

// payloadWorkspaces answers the nonempty workspaces of a payload, at most maxWorkspaces.
func payloadWorkspaces(workspaces []string) ([]string, error) {
	var dirs []string
	for i := 0; i < len(workspaces); i++ {
		if workspaces[i] == "" {
			continue
		}
		if len(dirs) == maxWorkspaces {
			return nil, fmt.Errorf("payload names more than %d workspaces", maxWorkspaces)
		}
		dirs = append(dirs, workspaces[i])
	}
	return dirs, nil
}

// repositoryRoot answers the repository root that holds dir, "" when git says dir is in none.
func repositoryRoot(ctx context.Context, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("workspace %q is not an absolute path", boundReason(dir))
	}
	result, err := util.RunGitProbe(ctx, dir, maxRootBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		if util.GitAnsweredNotARepository(result, err) {
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

// resolveWorkspaceRoot is the root lookup the hook runs before judging a call (ResolveRoot); a
// test replaces it with one that outlasts the deadline.
var resolveWorkspaceRoot = ResolveRoot

// Governed reports whether root holds the standards manifest.
func Governed(root string) bool {
	return root != "" && util.FileExists(filepath.Join(root, manifestName))
}
