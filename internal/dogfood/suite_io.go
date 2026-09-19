package dogfood

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func validateSuitePath(path string) error {
	// The component count uses every separator the host accepts. Splitting on
	// filepath.Separator alone split only on '' on Windows, which also accepts '/', so a
	// slash-separated path of any depth counted as one component and passed the 128 bound:
	// measured, a 201-deep slash path was accepted while the same depth in backslashes was
	// refused. ToSlash is a no-op on POSIX, where '' is a filename character, not a separator.
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) || len(strings.Split(filepath.ToSlash(path), "/")) > 128 {
		return errors.New("suite path must be bounded UTF-8 without control characters")
	}
	return nil
}

// openSuiteDirectory pins path as a confinement root.
//
// This used to walk every component from the volume root itself, Lstat-checking each one and
// rejecting the first symlink found. macOS ships /var and /tmp as symlinks to /private/var and
// /private/tmp, so any suite path under the platform's own TMPDIR -- which is where every
// t.TempDir() fixture in this package's own tests lives -- was rejected before the suite ever
// ran. That was #109's exact shape, just re-implemented here instead of reused, and it took
// down the dogfood, cmd/standards-mcp and cmd/standardsctl packages on the macOS leg of the
// portability matrix (#135).
//
// contextopt.OpenDirectoryIn already carries the fix: it resolves the ancestry once (the
// operator's own filesystem, not praetor's threat surface) and enforces strictness only on
// the confinement root and whatever is walked below it. Delegating here removes a second
// implementation of the identical walk (HISS-19) instead of patching this copy in place.
func openSuiteDirectory(ctx context.Context, path string) (*os.Root, error) {
	if err := validateSuitePath(path); err != nil {
		return nil, err
	}
	return contextopt.OpenDirectoryIn(ctx, path, ".")
}

func openSuiteChild(ctx context.Context, parent *os.Root, name string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, errors.New("suite path ancestors must be directories, never symlinks")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err == nil && !os.SameFile(before, after) {
		err = errors.New("suite directory changed during open")
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func readSuiteConfig(ctx context.Context, path string) (data []byte, err error) {
	if ctx == nil {
		return nil, errors.New("suite requires a context")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := validateSuitePath(path); err != nil {
		return nil, err
	}
	root, err := openSuiteDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return readSuiteFile(ctx, root, filepath.Base(abs))
}

func readSuiteFile(ctx context.Context, root *os.Root, name string) (data []byte, err error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxSuiteConfigBytes {
		return nil, errors.New("suite config must be a regular file at most 64 KiB")
	}
	file, err := openSuiteConfigFile(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("suite config changed during open")
	}
	data, err = io.ReadAll(io.LimitReader(file, maxSuiteConfigBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkSuiteSnapshot(before, after, data); err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

func checkSuiteSnapshot(before, after os.FileInfo, data []byte) error {
	if !os.SameFile(before, after) || before.Size() != int64(len(data)) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(data) > maxSuiteConfigBytes {
		return errors.New("suite config changed during snapshot or exceeded bounds")
	}
	if !utf8.Valid(data) {
		return errors.New("suite config must be UTF-8")
	}
	return nil
}

func createSuiteRun(ctx context.Context, path string) (absolute string, err error) {
	if err := validateSuitePath(path); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := openSuiteDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := parent.Mkdir(filepath.Base(abs), 0o700); err != nil {
		return "", fmt.Errorf("suite requires a new evidence directory: %w", err)
	}
	return abs, nil
}
