// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// treeListTimeout bounds one listing of a tree's files (HISS-02); a listing grows with the
// repository, so it is longer than one probe.
const treeListTimeout = 30 * time.Second

// Tree is a read-only view of one state of a repository: a checkout's working tree (DirTree) or
// a commit (CommitTree).
type Tree interface {
	// Files lists the state's files as sorted repository-relative slash paths.
	Files(ctx context.Context) ([]string, error)
	// Read returns one file's bytes; a file the state does not hold is (nil, false, nil).
	Read(ctx context.Context, rel string) ([]byte, bool, error)
}

// DirTree is the working tree of the checkout at root: the tracked files that still exist and
// the untracked ones git does not ignore. It is listed afresh on every call, so a view of a
// render worktree sees what the generators wrote.
func DirTree(root string) Tree {
	return dirTree{root: root}
}

type dirTree struct{ root string }

// Files lists the checkout's files through git ls-files, keeping only regular files that exist.
func (t dirTree) Files(ctx context.Context) ([]string, error) {
	listed, err := gitList(ctx, t.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate")
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(listed))
	for index := 0; index < len(listed); index++ {
		info, err := os.Lstat(filepath.Join(t.root, filepath.FromSlash(listed[index])))
		if err == nil && info.Mode().IsRegular() {
			files = append(files, listed[index])
		}
	}
	return files, nil
}

// Read reads one file below the root through the confined, bounded reader.
func (t dirTree) Read(ctx context.Context, rel string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, err := util.ReadConfinedLimited(t.root, filepath.FromSlash(rel), MaxArtefactBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, true, nil
}

// CommitTree is the commit rev of the repository at root. Its listing is read once.
func CommitTree(root, rev string) Tree {
	return &commitTree{root: root, rev: rev}
}

type commitTree struct {
	root  string
	rev   string
	files []string
	index map[string]bool
}

// Files lists the commit's files through git ls-tree.
func (t *commitTree) Files(ctx context.Context) ([]string, error) {
	if t.index != nil {
		return t.files, nil
	}
	files, err := gitList(ctx, t.root, "ls-tree", "-r", "-z", "--name-only", "--full-tree", t.rev)
	if err != nil {
		return nil, err
	}
	t.files, t.index = files, make(map[string]bool, len(files))
	for index := 0; index < len(files); index++ {
		t.index[files[index]] = true
	}
	return t.files, nil
}

// Read reads one blob of the commit, refusing one above MaxArtefactBytes.
func (t *commitTree) Read(ctx context.Context, rel string) ([]byte, bool, error) {
	if _, err := t.Files(ctx); err != nil {
		return nil, false, err
	}
	if !t.index[rel] {
		return nil, false, nil
	}
	result, err := util.RunGitProbe(ctx, t.root, MaxArtefactBytes+1, "cat-file", "blob", t.rev+":"+rel)
	if err != nil {
		return nil, false, fmt.Errorf("read %s at %s: %w", rel, t.rev, err)
	}
	return result.Stdout, true, nil
}

// gitList runs one NUL-separated git listing in root and returns its sorted entries, refusing a
// listing above MaxTreeFiles.
func gitList(ctx context.Context, root string, args ...string) ([]string, error) {
	result, err := util.RunGitProbeWithin(ctx, root, util.MaxCommandOutputBytes, treeListTimeout, args...)
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %w", args[0], root, err)
	}
	fields := bytes.Split(bytes.TrimSuffix(result.Stdout, []byte{0}), []byte{0})
	if len(fields) > MaxTreeFiles {
		return nil, fmt.Errorf("git %s in %s lists more than %d files", args[0], root, MaxTreeFiles)
	}
	entries := make([]string, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		if entry := string(fields[index]); entry != "" {
			entries = append(entries, entry)
		}
	}
	sort.Strings(entries)
	return entries, nil
}

// readText reads one file of tree as text with CRLF line ends read as LF, so a CRLF checkout
// compares equal to the LF blob it was checked out from (HISS-21).
func readText(ctx context.Context, tree Tree, rel string) (string, bool, error) {
	data, exists, err := tree.Read(ctx, rel)
	if err != nil || !exists {
		return "", exists, err
	}
	text, _ := util.NormalizeLineEndings(string(data))
	return text, true, nil
}

// extractBlock returns the lines from block's start marker through its end marker, or found
// false when content carries neither marker. Markers are whole lines and are ignored inside
// fenced code (util.FindMarkedBlock); an unbalanced or repeated marker is an error.
func extractBlock(content string, block *Block) (string, bool, error) {
	first, last, err := util.FindMarkedBlock(content, block.Start, block.End)
	if err != nil || first < 0 {
		return "", false, err
	}
	lines := strings.Split(content, "\n")
	return strings.Join(lines[first:last+1], "\n"), true, nil
}

// replaceBlock returns content with its block lines replaced by rendered, which must itself be
// a whole block, through util.ReplaceMarkedBlock. Content without the block is an error, where
// ReplaceMarkedBlock would append one: the rendering has no place in that file.
func replaceBlock(content, rendered string, block *Block) (string, error) {
	if _, found, err := extractBlock(content, block); err != nil || !found {
		return "", errors.Join(err, fmt.Errorf("no %s .. %s block to replace", block.Start, block.End))
	}
	replaced, _, err := util.ReplaceMarkedBlock(content, block.Start, block.End, rendered, util.MaxMarkedBlockLines)
	return replaced, err
}
