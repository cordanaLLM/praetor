package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

const (
	maxPublicTreeEntries = 20000
	maxPublicFileBytes   = 16 << 20
	maxPublicTreeBytes   = 256 << 20
)

type publicTree map[string]string

type publicSnapshot struct {
	tree     publicTree
	dirs     []string
	total    int64
	entries  int
	filtered bool
}

func snapshotPublicTree(ctx context.Context, dir string) (tree publicTree, err error) {
	return snapshotTree(ctx, dir, false)
}

// snapshotDiscoveryTree reuses the bounded snapshot reader while excluding
// generated/dependency/private state according to the existing scanner policy.
func snapshotDiscoveryTree(ctx context.Context, dir string) (publicTree, error) {
	return snapshotTree(ctx, dir, true)
}

func snapshotTree(ctx context.Context, dir string, filtered bool) (tree publicTree, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	snapshot := publicSnapshot{tree: make(publicTree), dirs: []string{"."}, entries: 1, filtered: filtered}
	for i := 0; i < len(snapshot.dirs) && i < maxPublicTreeEntries; i++ {
		if err := snapshot.addDirectory(ctx, root, snapshot.dirs[i]); err != nil {
			return nil, err
		}
	}
	return snapshot.tree, nil
}

func (snapshot *publicSnapshot) addDirectory(ctx context.Context, root *os.Root, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := readPublicDirectory(root, dir)
	if err != nil {
		return err
	}
	info, err := root.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("snapshot directory %s changed type", dir)
	}
	snapshot.tree[filepath.ToSlash(dir)] = fmt.Sprintf("directory:%04o", info.Mode().Perm())
	for i := 0; i < len(entries) && i < maxPublicTreeEntries; i++ {
		rel := filepath.Join(dir, entries[i].Name())
		if err := snapshot.addEntry(ctx, root, rel, entries[i]); err != nil {
			return err
		}
	}
	return nil
}

func (snapshot *publicSnapshot) addEntry(ctx context.Context, root *os.Root, rel string, entry os.DirEntry) error {
	if rel == ".git" {
		return nil
	}
	snapshot.entries++
	if snapshot.entries > maxPublicTreeEntries {
		return errors.New("public checkout exceeds 20000 entries")
	}
	if snapshot.filtered && discoveryIgnored(rel, entry) {
		return nil
	}
	if entry.IsDir() {
		snapshot.dirs = append(snapshot.dirs, rel)
		return nil
	}
	if err := snapshot.addFile(ctx, root, rel); err != nil {
		return err
	}
	return nil
}

func discoveryIgnored(rel string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return hiss.ShouldIgnoreDir(entry.Name(), rel)
	}
	return rel == ".standards-receipt.json" || hiss.ShouldIgnorePath(rel)
}

func (snapshot *publicSnapshot) addFile(ctx context.Context, root *os.Root, rel string) error {
	digest, size, err := publicFileDigest(ctx, root, rel)
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", rel, err)
	}
	snapshot.total += size
	if snapshot.total > maxPublicTreeBytes {
		return errors.New("public checkout exceeds 256 MiB")
	}
	snapshot.tree[filepath.ToSlash(rel)] = digest
	return nil
}

func readPublicDirectory(root *os.Root, rel string) ([]os.DirEntry, error) {
	dir, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(maxPublicTreeEntries + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > maxPublicTreeEntries {
		return nil, errors.New("public directory exceeds 20000 entries")
	}
	return entries, nil
}

func publicFileDigest(ctx context.Context, root *os.Root, rel string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return "", 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(rel)
		return "symlink:" + target, int64(len(target)), err
	}
	data, err := readPublicFile(root, rel, maxPublicFileBytes)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%04o:%x", info.Mode().Perm(), sum), int64(len(data)), nil
}

func readPublicFile(root *os.Root, rel string, limit int64) (data []byte, err error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", rel, limit)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, fmt.Errorf("%s changed while opening", rel)
	}
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", rel, limit)
	}
	if err := checkPublicFileStable(root, rel, f, actual, len(data)); err != nil {
		return nil, err
	}
	return data, nil
}

func checkPublicFileStable(root *os.Root, rel string, file *os.File, before os.FileInfo, size int) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if !os.SameFile(before, current) || before.Size() != int64(size) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		return fmt.Errorf("%s changed while reading", rel)
	}
	return nil
}

func (tree publicTree) digest() string {
	keys := make([]string, 0, len(tree))
	for path := range tree {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	var data strings.Builder
	for i := 0; i < len(keys) && i < maxPublicTreeEntries; i++ {
		fmt.Fprintf(&data, "%s\x00%s\x00", keys[i], tree[keys[i]])
	}
	sum := sha256.Sum256([]byte(data.String()))
	return hex.EncodeToString(sum[:])
}

func (tree publicTree) changed(original publicTree) []string {
	changed := make([]string, 0)
	for path, digest := range tree {
		if original[path] != digest {
			changed = append(changed, path)
		}
	}
	for path := range original {
		if _, exists := tree[path]; !exists {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}
