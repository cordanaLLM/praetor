package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// projectedFilePerm is the mode ceiling of every compiled output and persona or skill copy.
const projectedFilePerm os.FileMode = 0o644

// errOutputNotRegular refuses an existing compiled output that is a symlink, a directory or
// any other non-regular file: the writer (contextopt.WriteSnapshotIn) would refuse it too.
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

// removeConfinedFile removes the regular file rel below root without following a symlink at
// any component of rel. An absent file or parent is ignored (returns nil).
func removeConfinedFile(ctx context.Context, root, rel string) (err error) {
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
	if rmErr := dir.Remove(leaf); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return rmErr
	}
	return nil
}

// readConfinedDir lists the directory rel below root without following a symlink at any
// component of rel, bounded by contextopt.MaxDuration (HISS-02). It reads at most limit+1
// entries, so a caller can refuse a directory above its cap, and returns them by name. A
// component that does not exist is os.ErrNotExist; one that is a symlink or a file is refused.
func readConfinedDir(ctx context.Context, root, rel string, limit int) (entries []os.DirEntry, err error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	dir, err := contextopt.OpenDirectoryIn(ctx, root, filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	listing, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, listing.Close()) }()
	entries, err = listing.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// writeConfinedText writes rel below root through contextopt.WriteSnapshotIn, which creates and
// walks every directory component of rel without following a symlink. The writer therefore
// refuses exactly what checkOutputPath and readConfinedText refuse, so write and verify agree.
func writeConfinedText(ctx context.Context, root, rel string, data []byte) error {
	return contextopt.WriteSnapshotIn(ctx, root, filepath.FromSlash(rel), data, projectedFilePerm)
}

// projectionFile is one compiled output or persona or skill copy: the declared slash path it
// is written to below the target directory, and the bytes it receives.
type projectionFile struct {
	rel  string
	data []byte
}

// checkProjectionFiles runs the writer's refusals (projectionPath, checkOutputPath) over every
// file before the first is written, so a refused target leaves all of them unchanged.
func checkProjectionFiles(ctx context.Context, root string, files []projectionFile) error {
	for _, file := range files {
		if _, err := projectionPath(root, file.rel); err != nil {
			return fmt.Errorf("target %s: %w", file.rel, err)
		}
		if err := checkOutputPath(ctx, root, file.rel); err != nil {
			return fmt.Errorf("target %s: %w", file.rel, err)
		}
	}
	return nil
}

// writeProjectionFiles writes every file through writeConfinedText, in order.
func writeProjectionFiles(ctx context.Context, root string, files []projectionFile) error {
	for _, file := range files {
		if err := writeConfinedText(ctx, root, file.rel, file.data); err != nil {
			return fmt.Errorf("target %s: %w", file.rel, err)
		}
	}
	return nil
}

// checkOutputPath applies the refusals contextopt.WriteSnapshotIn makes at write time, without
// writing: an existing component of rel that is a symlink or not a directory, an existing output
// that is not a regular file, and an existing output the writer cannot observe, that is one
// above contextopt.MaxSourceBytes or not UTF-8 text without NUL bytes (read through
// contextopt.ReadRootSnapshot, the read the writer's ObserveRootSnapshot makes). Callers run it
// over every output before the first write, so a refusal leaves every output untouched. A
// component that does not exist yet ends the walk: nothing below it exists, and the writer
// creates it without following a symlink.
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
	if _, err := contextopt.ReadRootSnapshot(ctx, dir, leaf); err != nil {
		return fmt.Errorf("existing output cannot be replaced: %w", err)
	}
	return nil
}
