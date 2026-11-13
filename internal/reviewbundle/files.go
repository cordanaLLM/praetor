// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
)

// appendFiles retains bounded predicted content and records every shortened or omitted path.
func appendFiles(ctx context.Context, repo string, data []byte, b *Bundle) error {
	if len(data) == 0 {
		return nil
	}
	if data[len(data)-1] != 0 {
		return fmt.Errorf("unterminated changed-path listing")
	}
	paths := strings.Split(string(data[:len(data)-1]), "\x00")
	remaining := b.Limits.MaxBundleBytes
	for i := 0; i < len(paths); i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("bundle paths: %w", err)
		}
		if len(b.Files) >= b.Limits.MaxFiles {
			b.Cut = append(b.Cut, paths[i])
			continue
		}
		limit := min(b.Limits.MaxFileBytes, remaining)
		file, err := readFile(ctx, repo, b.Tree, paths[i], limit)
		if err != nil {
			return fmt.Errorf("bundle path %q: %w", paths[i], err)
		}
		remaining -= len(file.Content)
		b.Files = append(b.Files, file)
		if file.Truncated {
			b.Cut = append(b.Cut, file.Path)
		}
	}
	return nil
}

// readFile uses exact tree entries and blob objects, avoiding text conversion and working-tree reads.
func readFile(ctx context.Context, repo, tree, path string, limit int) (File, error) {
	f := File{Path: path}
	out, err := util.RunGitProbe(ctx, repo, util.MaxCommandOutputBytes, "--literal-pathspecs", "ls-tree", "--full-tree", "-l", "-z", tree, "--", path)
	if err != nil {
		return f, fmt.Errorf("tree entry: %w", err)
	}
	entries, err := util.ParseGitTreeListing(out.Stdout, true, util.MaxCommandOutputBytes/util.MinGitTreeRecordBytes)
	if err != nil {
		return f, fmt.Errorf("tree listing: %w", err)
	}
	for i := 0; i < len(entries); i++ {
		if entries[i].Path != path {
			continue
		}
		if entries[i].Type == "tree" {
			break
		}
		if entries[i].Type != "blob" {
			f.Binary = true
			return f, nil
		}
		return readBlob(ctx, repo, entries[i], limit)
	}
	f.Deleted = true
	return f, nil
}

// readBlob retains at most limit bytes but samples Git's first 8000 bytes for binary NULs.
// An overflow-cancelled read is accepted only at the known blob's prefix boundary, with live caller.
func readBlob(ctx context.Context, repo string, entry util.GitTreeEntry, limit int) (File, error) {
	f := File{Path: entry.Path}
	capBytes := max(limit, 8000)
	out, err := util.RunGitProbe(ctx, repo, capBytes, "cat-file", "blob", entry.Object)
	if err != nil && !expectedPrefix(ctx, out, err, entry.Size, capBytes) {
		return f, fmt.Errorf("blob contents: %w", err)
	}
	if bytes.IndexByte(out.Stdout[:min(8000, len(out.Stdout))], 0) >= 0 {
		f.Binary = true
		return f, nil
	}
	f.Content = textPrefix(out.Stdout, limit, entry.Size > int64(limit))
	omitted := entry.Size - int64(len(f.Content))
	if omitted > int64(int(^uint(0)>>1)) {
		return f, fmt.Errorf("blob size exceeds supported byte count")
	}
	if omitted > 0 {
		f.Truncated = true
		f.TruncatedBytes = int(omitted)
	}
	return f, nil
}

// textPrefix keeps at most limit bytes of data. Only a cut blob drops an incomplete final UTF-8 rune
// at the budget boundary; a blob kept whole keeps every byte, invalid UTF-8 included, and is never marked cut.
func textPrefix(data []byte, limit int, cut bool) string {
	end := min(limit, len(data))
	if !cut {
		return string(data[:end])
	}
	for i := 0; i < utf8.UTFMax && end > 0; i++ {
		r, size := utf8.DecodeLastRune(data[:end])
		if r != utf8.RuneError || size != 1 {
			break
		}
		end--
	}
	return string(data[:end])
}

// expectedPrefix distinguishes a bounded-output cancellation from caller cancellation or Git failure.
func expectedPrefix(ctx context.Context, out util.CommandBytes, err error, size int64, capBytes int) bool {
	return ctx.Err() == nil && size > int64(capBytes) && len(out.Stdout) == capBytes && len(out.Stderr) == 0 && errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
