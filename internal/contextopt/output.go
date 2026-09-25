// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteCandidate revalidates the selected snapshot, then creates context.md and
// manifest.json in a new 0700 directory, with 0600 files. It never replaces
// existing output, source files, canonical AGENTS.md, or generated vendor rules.
func (p *Plan) WriteCandidate(ctx context.Context, outputDir string) (err error) {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	if p == nil || len(p.report.Sources) == 0 {
		return fmt.Errorf("an analyzed plan is required")
	}
	abs, err := p.outputPath(outputDir)
	if err != nil {
		return err
	}
	parent, err := openDirectory(ctx, filepath.Dir(abs))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	name := filepath.Base(abs)
	if _, statErr := parent.Lstat(name); !errors.Is(statErr, os.ErrNotExist) {
		return errors.Join(fmt.Errorf("output directory must not already exist"), statErr)
	}
	if err := p.revalidate(ctx); err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(p.report, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.Mkdir(name, 0o700); err != nil {
		return err
	}
	return p.writeFiles(ctx, parent, name, append(manifest, '\n'))
}

func (p *Plan) outputPath(outputDir string) (string, error) {
	if outputDir == "" {
		return "", fmt.Errorf("explicit output directory is required")
	}
	abs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	if err := validatePath(abs); err != nil {
		return "", err
	}
	for i := 0; i < len(p.options.Sources); i++ {
		source := filepath.Join(p.options.Root, p.options.Sources[i])
		if Overlaps(abs, source) {
			return "", fmt.Errorf("output must not overlap a selected source")
		}
	}
	return abs, nil
}

// Overlaps reports whether either path lexically contains the other; equal paths
// overlap. Paths filepath.Rel cannot relate, such as different Windows volumes or an
// absolute path against a relative one, are disjoint. Callers resolve symlinks first
// when aliases matter.
func Overlaps(first, second string) bool {
	relative, err := filepath.Rel(first, second)
	if err == nil && filepath.IsLocal(relative) {
		return true
	}
	relative, err = filepath.Rel(second, first)
	return err == nil && filepath.IsLocal(relative)
}

func (p *Plan) revalidate(ctx context.Context) error {
	data, err := snapshots(ctx, p.options)
	if err != nil {
		return err
	}
	for i := 0; i < len(data); i++ {
		if digest(data[i]) != p.report.Sources[i].SHA256 {
			return fmt.Errorf("source %d changed since analysis; analyze again", i+1)
		}
	}
	return ctx.Err()
}

func (p *Plan) writeFiles(ctx context.Context, parent *os.Root, name string, manifest []byte) (err error) {
	root, err := childDirectory(ctx, parent, name)
	if err != nil {
		return errors.Join(err, parent.Remove(name))
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeIfPresent(root, "context.md"), removeIfPresent(root, "manifest.json"))
		}
		err = errors.Join(err, root.Close())
		if err != nil {
			err = errors.Join(err, parent.Remove(name))
		}
	}()
	if err := writePrivate(ctx, root, "context.md", p.pack); err != nil {
		return err
	}
	return writePrivate(ctx, root, "manifest.json", manifest)
}

func writePrivate(ctx context.Context, root *os.Root, name string, data []byte) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	const chunkSize = 32 * 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := file.Write(data[offset:min(offset+chunkSize, len(data))]); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return ctx.Err()
}

func removeIfPresent(root *os.Root, name string) error {
	err := root.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
