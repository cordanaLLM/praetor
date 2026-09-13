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
	selected := Options{Root: root, Sources: append([]string(nil), options.Sources...)}
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
		if !filepath.IsLocal(name) || filepath.Clean(name) != name || name == "." || seen[name] {
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
	if len(strings.Split(path, string(filepath.Separator))) > MaxPathDepth {
		return fmt.Errorf("path exceeds %d components", MaxPathDepth)
	}
	return nil
}

// openDirectory pins every ancestor separately. Lstat/identity checks reject
// symlinks while os.Root confines a concurrently replaced child to its parent.
func openDirectory(ctx context.Context, absolute string) (*os.Root, error) {
	if err := validatePath(absolute); err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(absolute) + string(filepath.Separator)
	root, err := os.OpenRoot(volume)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(absolute, volume), string(filepath.Separator))
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		next, openErr := childDirectory(ctx, root, parts[i])
		err = errors.Join(openErr, root.Close())
		if err != nil {
			if next != nil {
				err = errors.Join(err, next.Close())
			}
			return nil, err
		}
		root = next
	}
	return root, nil
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

func snapshotRootBytes(ctx context.Context, root *os.Root, name string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > MaxSourceBytes {
		return nil, fmt.Errorf("source must be regular and at most %d bytes", MaxSourceBytes)
	}
	file, err := openSource(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	data, err = stableRead(ctx, file, before)
	if err != nil {
		return nil, err
	}
	linked, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, linked) {
		return nil, errors.Join(fmt.Errorf("source replaced during snapshot"), err)
	}
	return data, nil
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
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("source changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, MaxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSourceBytes || int64(len(data)) != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("source changed during snapshot or exceeded byte bound")
	}
	return data, ctx.Err()
}
