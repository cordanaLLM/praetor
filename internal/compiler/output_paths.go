package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// errOutputNotRegular refuses an existing compiled output that is a symlink, a directory or
// any other non-regular file: the writer (contextopt.WriteSnapshot) would refuse it too.
var errOutputNotRegular = errors.New("compiled output must be a regular file, never a symlink or directory")

// openOutputParent pins the directory holding rel below root and returns it with rel's final
// element. Every component of rel is walked without following a symlink
// (contextopt.OpenDirectoryIn); only root's own ancestry, which the operator's platform
// chooses, is resolved. rel is a declared slash path.
func openOutputParent(ctx context.Context, root, rel string) (*os.Root, string, error) {
	local := filepath.FromSlash(rel)
	dir, err := contextopt.OpenDirectoryIn(ctx, root, filepath.Dir(local))
	if err != nil {
		return nil, "", err
	}
	return dir, filepath.Base(local), nil
}

// readConfinedText reads the regular text file rel below root without following a symlink at
// any component of rel, bounded by contextopt.MaxDuration (HISS-02).
func readConfinedText(ctx context.Context, root, rel string) (data []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	dir, leaf, err := openOutputParent(ctx, root, rel)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return contextopt.ReadRootSnapshot(ctx, dir, leaf)
}

// checkOutputPath applies the path refusals contextopt.WriteSnapshot makes at write time: an
// existing component of rel that is a symlink or not a directory, or an existing output that
// is not a regular file. Callers run it over every output before the first write, so a refusal
// leaves every output untouched. A component that does not exist yet ends the walk: nothing
// below it exists, and the writer creates it without following a symlink.
func checkOutputPath(ctx context.Context, root, rel string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	dir, leaf, err := openOutputParent(ctx, root, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	info, err := dir.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errOutputNotRegular
	}
	return nil
}
