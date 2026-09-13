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
	limits   SnapshotLimits
	report   *SnapshotReport
	total    int64
	entries  int
	files    int
	filtered bool
}

func snapshotPublicTree(ctx context.Context, dir string) (tree publicTree, err error) {
	return snapshotTree(ctx, dir, false)
}

func snapshotTree(ctx context.Context, dir string, filtered bool) (tree publicTree, err error) {
	tree, _, err = snapshotTreeWithLimits(ctx, dir, filtered, nil)
	return tree, err
}

func snapshotTreeWithLimits(ctx context.Context, dir string, filtered bool, input *SnapshotLimits) (tree publicTree, report SnapshotReport, err error) {
	if ctx == nil {
		report.Status = "failed"
		return nil, report, errors.New("snapshot requires context")
	}
	limits, err := NormalizeSnapshotLimits(input)
	if err != nil {
		report.Status = "failed"
		return nil, report, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		report.Status = "failed"
		return nil, report, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			tree = nil
			report.Status = "failed"
		}
	}()
	snapshot := publicSnapshot{tree: make(publicTree), dirs: []string{"."}, entries: 1, filtered: filtered, limits: limits, report: &report}
	for i := 0; i < len(snapshot.dirs) && i < limits.MaxEntries; i++ {
		if err := snapshot.addDirectory(ctx, root, snapshot.dirs[i]); err != nil {
			report.Status = "partial"
			report.EntriesObserved, report.FilesObserved, report.BytesObserved = snapshot.entries, snapshot.files, snapshot.total
			return nil, report, err
		}
	}
	report.Status = "complete"
	report.EntriesObserved, report.FilesObserved, report.BytesObserved = snapshot.entries, snapshot.files, snapshot.total
	return snapshot.tree, report, nil
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
	if snapshot.entries > snapshot.limits.MaxEntries {
		snapshot.report.OffendingPath = rel
		snapshot.report.Limit = "max_entries"
		return fmt.Errorf("public checkout exceeds %d entries", snapshot.limits.MaxEntries)
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
	readLimit := snapshot.limits.MaxFileBytes
	limitName := "max_file_bytes"
	if remaining := snapshot.limits.MaxTreeBytes - snapshot.total; remaining < readLimit {
		readLimit, limitName = remaining, "max_tree_bytes"
	}
	digest, size, err := publicFileDigestWithLimit(ctx, root, rel, readLimit)
	if err != nil {
		snapshot.report.OffendingPath = rel
		snapshot.report.Limit = "file_read"
		if strings.Contains(err.Error(), "exceeds") {
			snapshot.report.Limit = limitName
		}
		return fmt.Errorf("snapshot %s: %w", rel, err)
	}
	snapshot.total += size
	snapshot.tree[filepath.ToSlash(rel)] = digest
	snapshot.files++
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

func publicFileDigestWithLimit(ctx context.Context, root *os.Root, rel string, limit int64) (digest string, size int64, err error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return "", 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(rel)
		if int64(len(target)) > limit {
			return "", 0, fmt.Errorf("%s exceeds %d bytes", rel, limit)
		}
		return "symlink:" + target, int64(len(target)), err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s is not a regular file", rel)
	}
	if info.Size() > limit {
		return "", 0, fmt.Errorf("%s exceeds %d bytes", rel, limit)
	}
	return digestPublicRegularFile(ctx, root, rel, info, limit)
}

func digestPublicRegularFile(ctx context.Context, root *os.Root, rel string, info os.FileInfo, limit int64) (digest string, size int64, err error) {
	f, err := openSuiteConfigFile(root, rel)
	if err != nil {
		return "", 0, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	actual, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(info, actual) {
		return "", 0, fmt.Errorf("%s changed while opening", rel)
	}
	hasher := sha256.New()
	count, err := io.Copy(hasher, io.LimitReader(contextReader{ctx: ctx, reader: f}, limit+1))
	if err != nil {
		return "", 0, err
	}
	if count > limit {
		return "", 0, fmt.Errorf("%s exceeds %d bytes", rel, limit)
	}
	if err := checkPublicFileStable(root, rel, f, actual, int(count)); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%04o:%x", info.Mode().Perm(), hasher.Sum(nil)), count, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
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
