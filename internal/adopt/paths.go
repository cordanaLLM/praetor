package adopt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// filePerm is the mode applied to scaffolded text files.
	filePerm os.FileMode = 0o644
	// execPerm is the mode applied to scaffolded scripts and hooks.
	execPerm os.FileMode = 0o755
	// dirPerm is the mode applied to scaffolded directories.
	dirPerm os.FileMode = 0o755
)

// repoFile confines a repository-relative path to repoPath and returns the absolute
// path to operate on. It refuses any path that leaves the repository lexically or
// through a symbolic link, so a checked-in symlink such as AGENTS.md -> ~/.ssh/id_rsa
// can never be read, rewritten or created through by adoption.
func repoFile(repoPath string, elem ...string) (string, error) {
	rel := filepath.Join(elem...)
	full, err := util.ConfinePath(repoPath, rel)
	if err != nil {
		return "", fmt.Errorf("refusing %s: %w", rel, err)
	}
	return full, nil
}

// writeRepoFile writes data to a path already confined by repoFile, creating missing
// parent directories. The parent is only created when absent so that the mode of an
// existing directory (including the repository root) is never altered.
func writeRepoFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if !util.DirExists(dir) {
		if err := util.MkdirSecure(dir, dirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := util.WriteFileSecure(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readRepoFile reads a path already confined by repoFile.
func readRepoFile(path string) ([]byte, error) {
	// #nosec G304 -- path was confined to the repository root by repoFile (ConfinePath).
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func fileExists(path string) bool {
	return util.PathExists(path)
}
