package contextopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureDirectory creates absent directories through pinned parents, rejecting a symlink at
// any component this call creates. Existing directory permissions are left unchanged.
//
// The ancestry that already exists is resolved rather than refused. Rejecting a symlink
// anywhere in the path refused every temporary directory on macOS, where TMPDIR is
// /var/folders/... and /var is a symlink to /private/var: the whole macOS leg of the
// portability matrix failed on it, and had failed on every run for a day (#135).
//
// The confinement property is unchanged. What it must prevent is praetor following a symlink
// inside the tree it governs to write outside that tree. A prefix that already exists is the
// operating system's own layout, not a component praetor is creating, so it is resolved once
// and everything below it is created under the resolved root.
func EnsureDirectory(ctx context.Context, path string, mode os.FileMode) (err error) {
	if ctx == nil || mode == 0 || mode&^0o755 != 0 {
		return errors.New("directory creation requires a context and nonzero permissions at most 0755")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := validatePath(abs); err != nil {
		return err
	}
	return ensureDirectoryPath(ctx, abs, mode)
}

func ensureDirectoryPath(ctx context.Context, abs string, mode os.FileMode) error {
	base, parts, err := existingAncestry(abs)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	leaf, err := ensureComponents(ctx, root, parts, mode)
	if err != nil {
		return err
	}
	return errors.Join(ctx.Err(), leaf.Close())
}

// ensureDirectoryIn creates the absent directories of root/rel and returns the pinned
// directory, with root as the confinement boundary. root must exist; its own ancestry is
// resolved once, as OpenDirectoryIn resolves it. Every component of rel, existing or created,
// is then walked without following a symlink: an existing component that is a symlink or not
// a directory is refused rather than followed. EnsureDirectory resolves whatever already
// exists instead, so a symlinked directory inside the governed tree carried its writes out of
// it. Existing directory permissions are left unchanged. The walk is bounded by MaxDuration
// (HISS-02). The caller closes the result.
func ensureDirectoryIn(ctx context.Context, root, rel string, mode os.FileMode) (*os.Root, error) {
	if ctx == nil || mode == 0 || mode&^0o755 != 0 {
		return nil, errors.New("directory creation requires a context and nonzero permissions at most 0755")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	parts, err := relativeComponents(rel)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	base, err := openDirectory(ctx, absRoot)
	if err != nil {
		return nil, err
	}
	return ensureComponents(ctx, base, parts, mode)
}

// ensureComponents creates and descends each of parts below root without following a
// symlink. It owns root: every directory it does not return is closed, on failure too.
func ensureComponents(ctx context.Context, root *os.Root, parts []string, mode os.FileMode) (*os.Root, error) {
	for i := 0; i < len(parts) && i < maxPathComponents; i++ {
		name := parts[i]
		if name == "" || name == "." {
			continue
		}
		if err := ensureChildDirectory(ctx, root, name, mode); err != nil {
			return nil, errors.Join(err, root.Close())
		}
		next, err := childDirectory(ctx, root, name)
		if err != nil {
			return nil, errors.Join(err, root.Close())
		}
		if err := root.Close(); err != nil {
			return nil, errors.Join(err, next.Close())
		}
		root = next
	}
	return root, nil
}

func ensureChildDirectory(ctx context.Context, root *os.Root, name string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(name, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("directory component must not be a symlink or file: %s", name)
	}
	return nil
}

// maxPathComponents bounds the walk so a pathological path cannot make it iterate without
// limit. A path deeper than this is rejected rather than truncated.
const maxPathComponents = 256

// existingAncestry splits abs into the deepest directory that already exists and the
// components below it that this call has to create.
//
// It probes with Lstat, never EvalSymlinks. A symlink is therefore not a valid base: the
// search backs off above it, and the per-component check below then rejects it exactly as
// before. That keeps the confinement property intact -- praetor still refuses to follow a
// symlink into a tree it does not govern -- while fixing macOS, where the offending symlink
// (/var -> /private/var) sits above a temporary directory that already exists and so is
// never examined at all. The previous walk started at the volume root and inspected every
// component, which is why /var failed there and nowhere else (#135).
func existingAncestry(abs string) (string, []string, error) {
	volume := filepath.VolumeName(abs) + string(filepath.Separator)
	raw := strings.Split(strings.TrimPrefix(abs, volume), string(filepath.Separator))
	if len(raw) > maxPathComponents {
		return "", nil, fmt.Errorf("path exceeds %d components: %s", maxPathComponents, abs)
	}
	components := make([]string, 0, len(raw))
	for i := 0; i < len(raw) && i < maxPathComponents; i++ {
		if raw[i] != "" {
			components = append(components, raw[i])
		}
	}
	// Deepest first: the fewer components this call creates, the smaller the window between
	// the check and the write.
	for cut := len(components); cut >= 0; cut-- {
		candidate := volume + strings.Join(components[:cut], string(filepath.Separator))
		info, err := os.Lstat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		return candidate, components[cut:], nil
	}
	return "", nil, fmt.Errorf("no existing ancestor directory for %s", abs)
}
