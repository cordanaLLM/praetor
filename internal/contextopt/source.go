// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validateOptions(options Options) (Options, error) {
	if options.Root == "" || len(options.Sources) == 0 || len(options.Sources) > MaxSources {
		return Options{}, fmt.Errorf("explicit root and 1..%d sources required", MaxSources)
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Options{}, err
	}
	if err := validatePath(root); err != nil {
		return Options{}, err
	}
	selected := Options{Root: root, Sources: make([]string, len(options.Sources))}
	// A source is an identity compared against the compiler's vendor targets, which are
	// slash paths (".cursor/rules/hiss-invariants.mdc"), not a host path. Normalising here
	// means a Windows user's backslash spelling names the same source and still matches
	// drift detection, instead of being judged -- and compared -- in a different form.
	// filepath.ToSlash is a no-op where the separator is already '/'.
	for i := range options.Sources {
		selected.Sources[i] = filepath.ToSlash(options.Sources[i])
	}
	if err := validateSources(selected.Sources); err != nil {
		return Options{}, err
	}
	return selected, nil
}

func validateSources(sources []string) error {
	seen := make(map[string]bool)
	for i := 0; i < len(sources); i++ {
		name := sources[i]
		if err := validatePath(name); err != nil {
			return err
		}
		// Cleanliness is judged with slash semantics, since the name is now in slash form:
		// filepath.Clean returns backslashes on Windows and never equalled a slash name, so
		// every documented spelling was refused. filepath.IsLocal is kept because containment
		// is a host question, and it is what still rejects an escaping or drive-qualified name.
		if !filepath.IsLocal(name) || path.Clean(name) != name || name == "." || seen[name] {
			return fmt.Errorf("source %d must be a unique clean relative file path", i+1)
		}
		seen[name] = true
	}
	return nil
}

func validatePath(path string) error {
	if path == "" || len(path) > MaxPathBytes || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("path must be nonempty UTF-8 without controls, at most %d bytes", MaxPathBytes)
	}
	// Count components across every separator the host accepts. Splitting on
	// filepath.Separator alone split only on '\' on Windows, which also accepts '/', so
	// "x/x/.../x" counted as one component and the depth bound never fired: a fail-open
	// input bound. Sources are normalised to slash form before reaching here, which would
	// have made the bound unreachable for every source on Windows. ToSlash is a no-op on
	// POSIX, where '\' is an ordinary filename character and must not be counted.
	if len(strings.Split(filepath.ToSlash(path), "/")) > MaxPathDepth {
		return fmt.Errorf("path exceeds %d components", MaxPathDepth)
	}
	return nil
}

// openDirectory pins a directory the caller names as its own confinement root.
//
// The named directory is the boundary. Its ancestry is the operator's filesystem, not praetor's
// threat surface, and treating it as hostile had a concrete cost: macOS ships /var and /tmp as
// symlinks, so rejecting a symlinked ancestor rejected every path under the platform's own
// temporary directory and left 30 of 56 packages unable to run there at all (#109). An attacker
// who controls /var does not need a symlink to defeat this.
//
// Strictness moves to where the untrusted content actually is. Everything *below* a root is
// walked component by component with no symlink tolerated -- see OpenDirectoryIn -- and the
// os.Root returned here confines every subsequent open to this subtree.
func openDirectory(ctx context.Context, absolute string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePath(absolute); err != nil {
		return nil, err
	}
	// Resolve the ancestry only. The named directory itself stays strict: a symlink handed in
	// as the root is still refused, because that is the caller naming one thing and getting
	// another. What is relaxed is the path *to* it, which the operator's platform chooses.
	parent, leaf := filepath.Dir(absolute), filepath.Base(absolute)
	if parent == absolute {
		// The filesystem root has no ancestry to resolve and no leaf to check.
		return os.OpenRoot(absolute)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	if err := validatePath(resolvedParent); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(resolvedParent, leaf))
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("confinement root must be a directory, never a symlink: %s", absolute)
	}
	return os.OpenRoot(filepath.Join(resolvedParent, leaf))
}

// OpenDirectoryIn pins root/rel, with root as the confinement boundary.
//
// The root's ancestry is resolved once; every component of rel is then walked strictly, so a
// symlink introduced inside the tree under audit is rejected rather than followed. That is the
// direction that matters. Repository content is attacker-influenceable; the operator's own
// filesystem above the root is not, and conflating the two is what #109 was.
//
// Prefer this over OpenDirectory wherever the caller already holds a root and a path beneath it.
func OpenDirectoryIn(ctx context.Context, root, rel string) (*os.Root, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parts, err := relativeComponents(rel)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	current, err := openDirectory(ctx, absRoot)
	if err != nil {
		return nil, err
	}
	return walkComponents(ctx, current, parts)
}

// relativeComponents splits a root-relative path into the components to walk.
//
// An over-deep path is refused rather than truncated: walking a prefix would return a root for
// a directory the caller never named. HISS-02 bounds the loop; it does not license a different
// answer than the one asked for.
func relativeComponents(rel string) ([]string, error) {
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("path %q must stay inside the confinement root", rel)
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) > MaxPathDepth {
		return nil, fmt.Errorf("path exceeds %d components: %q", MaxPathDepth, rel)
	}
	return parts, nil
}

// walkComponents descends each component strictly, closing whichever root it does not return.
func walkComponents(ctx context.Context, current *os.Root, parts []string) (_ *os.Root, err error) {
	for i := 0; i < len(parts) && i < MaxPathDepth; i++ {
		if parts[i] == "" || parts[i] == "." {
			continue
		}
		next, openErr := childDirectory(ctx, current, parts[i])
		err = errors.Join(openErr, current.Close())
		if err != nil {
			if next != nil {
				err = errors.Join(err, next.Close())
			}
			return nil, err
		}
		current = next
	}
	return current, nil
}

func childDirectory(ctx context.Context, parent *os.Root, name string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("path component must be a directory, never a symlink")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err == nil && !os.SameFile(before, after) {
		err = fmt.Errorf("directory changed while opening")
	}
	if err != nil {
		return nil, errors.Join(err, child.Close())
	}
	return child, nil
}

func snapshots(ctx context.Context, options Options) ([][]byte, error) {
	data := make([][]byte, 0, len(options.Sources))
	total := 0
	for i := 0; i < len(options.Sources); i++ {
		content, err := snapshot(ctx, filepath.Join(options.Root, options.Sources[i]))
		if err != nil {
			return nil, fmt.Errorf("source %d: %w", i+1, err)
		}
		total += len(content)
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("selected sources exceed %d total bytes", MaxTotalBytes)
		}
		data = append(data, content)
	}
	return data, nil
}

func snapshot(ctx context.Context, absolute string) (data []byte, err error) {
	if err := validatePath(absolute); err != nil {
		return nil, err
	}
	root, err := openDirectory(ctx, filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	name := filepath.Base(absolute)
	return snapshotRoot(ctx, root, name)
}

func snapshotRoot(ctx context.Context, root *os.Root, name string) (data []byte, err error) {
	data, err = snapshotRootBytes(ctx, root, name)
	if err != nil {
		return nil, err
	}
	if err := validateText(data); err != nil {
		return nil, err
	}
	return data, nil
}

func snapshotRootBytes(ctx context.Context, root *os.Root, name string) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := snapshotRootCopy(ctx, root, name, &buffer, MaxSourceBytes); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// snapshotRootCopy streams one regular file of at most limit bytes from a pinned
// directory into dst, rejecting symlinks, non-regular files and any change to the
// file's identity, size or modification time during the copy. dst may hold a partial
// copy when an error is returned; callers discard it.
func snapshotRootCopy(ctx context.Context, root *os.Root, name string, dst io.Writer, limit int64) (size int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return 0, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return 0, fmt.Errorf("source must be regular and at most %d bytes", limit)
	}
	file, err := openSource(root, name)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	size, err = stableCopy(ctx, dst, file, before, limit)
	if err != nil {
		return 0, err
	}
	linked, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, linked) {
		return 0, errors.Join(fmt.Errorf("source replaced during snapshot"), err)
	}
	return size, nil
}

func validateText(data []byte) error {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return fmt.Errorf("source must be UTF-8 text without NUL bytes")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func stableRead(ctx context.Context, file *os.File, before os.FileInfo) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := stableCopy(ctx, &buffer, file, before, MaxSourceBytes); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// stableCopy copies at most limit bytes of an opened file into dst and fails unless
// exactly the size observed before opening was read and the file kept its identity,
// size and modification time throughout.
func stableCopy(ctx context.Context, dst io.Writer, file *os.File, before os.FileInfo, limit int64) (int64, error) {
	opened, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return 0, fmt.Errorf("source changed while opening")
	}
	count, err := io.Copy(dst, io.LimitReader(contextReader{ctx, file}, limit+1))
	if err != nil {
		return 0, err
	}
	after, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if count > limit || count != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return 0, fmt.Errorf("source changed during snapshot or exceeded byte bound")
	}
	return count, ctx.Err()
}
