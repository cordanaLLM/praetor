// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// The REUSE lint gate runs one reuse release line everywhere: praetor's own Compliance workflow
// (.github/workflows/compliance.yml), the workflow and the pre-commit job adoption emits into a
// repository that declares its licensing the REUSE way (internal/adopt, reuse_gate.go and
// hooks.go), and the release bump compares workflows against (internal/bump, knownActionLatest).
// The pin is ReuseActionVersion alone: fsfe/reuse-action at that tag builds FROM
// fsfe/reuse:<major>, so its major is the reuse major the emitted hook requires locally.

const (
	// ReuseAction is the action that runs reuse lint in a workflow.
	ReuseAction = "fsfe/reuse-action"
	// ReuseActionVersion is the one tag every emitted or own workflow runs ReuseAction at.
	ReuseActionVersion = "v6"
	// ReuseActionCommit is the full commit SHA corresponding to ReuseActionVersion.
	ReuseActionCommit = "676e2d560c9a403aa252096d99fcab3e1132b0f5"
	// LicensesDir holds the full text of every license REUSE names, one <id>.txt each.
	LicensesDir = "LICENSES"
	// ReuseDep5File is the deprecated machine-readable declaration REUSE still reads beside
	// ReuseFile (REUSE Specification 3.3).
	ReuseDep5File = ".reuse/dep5"
	// REUSE-IgnoreStart: the tag below is the string the scan looks for, not this file's licence.
	// reuseHeaderTag is the comment tag a file states its license with (REUSE Specification 3.3).
	reuseHeaderTag = "SPDX-License-Identifier:"
	// REUSE-IgnoreEnd
	// reuseHeaderBytes is how much of a file ReuseHeadersOnlyNotice reads for reuseHeaderTag:
	// the first chunk reuse 6.x reads (reuse.extract.CHUNK_SIZE, 64 KiB), where a header stands.
	reuseHeaderBytes = 64 << 10
	// maxReuseHeaderFiles bounds the files ReuseHeadersOnlyNotice reads (HISS-02).
	maxReuseHeaderFiles = 20000
)

// ReuseActionRef is ReuseAction pinned at ReuseActionVersion, the form the hook job output uses.
func ReuseActionRef() string {
	return ReuseAction + "@" + ReuseActionVersion
}

// ReuseActionPinnedRef is ReuseAction pinned by full commit SHA with its release tag as a
// trailing comment, the digest-pinned form a workflow step uses (HISS-11).
func ReuseActionPinnedRef() string {
	return ReuseAction + "@" + ReuseActionCommit + "  # " + ReuseActionVersion
}

// ReuseMajor is the reuse release major ReuseActionVersion runs: its tag without the "v".
func ReuseMajor() string {
	return strings.TrimPrefix(ReuseActionVersion, "v")
}

// ReuseDeclared reports whether the repository at root declares its licensing the REUSE way
// in one of the two files REUSE Specification 3.3 reads for the whole tree: ReuseFile or the
// deprecated ReuseDep5File, each a file, not a directory, and neither followed through a symlink.
// LICENSES/ alone declares nothing: it is where REUSE keeps license texts, and where repositories
// that never used the reuse tool keep third-party notices (#1244). A repository that labels its
// files by headers alone is not counted either; ReuseHeadersOnlyNotice names it. A path that
// exists but cannot be inspected is an error, never a repository without REUSE.
func ReuseDeclared(ctx context.Context, root string) (bool, error) {
	if ctx == nil {
		return false, errors.New("REUSE detection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, name := range []string{ReuseFile, ReuseDep5File} {
		present, err := rootEntry(root, name, false)
		if err != nil || present {
			return present, err
		}
	}
	return false, nil
}

// ReuseDeclarationFiles names the files ReuseDeclared reads, for messages.
func ReuseDeclarationFiles() string {
	return ReuseFile + " or " + ReuseDep5File
}

// rootEntry reports whether root holds name as a directory (dir) or as anything else, not
// through a symlink. A path that exists but cannot be inspected is an error.
func rootEntry(root, name string, dir bool) (bool, error) {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", name, err)
	}
	return info.IsDir() == dir && info.Mode()&os.ModeSymlink == 0, nil
}

// ReuseHeadersOnlyNotice returns the line that names a repository labelling its files the REUSE
// way by headers alone: LICENSES/ is a directory, neither ReuseFile nor ReuseDep5File is there,
// and repository files outside LICENSES/ and .reuse/ carry the SPDX-License-Identifier tag in
// their first reuseHeaderBytes, or are .license companions. Since #1244 such a repository gets no
// REUSE gate, and adoption and audit both print this line, so the drop is visible. It returns ""
// for every other repository, and when git lists no files (no work tree): the files are the
// repository's own only by git's answer (hiss.GitVisiblePaths).
func ReuseHeadersOnlyNotice(ctx context.Context, root string) (string, error) {
	declared, err := ReuseDeclared(ctx, root)
	if err != nil || declared {
		return "", err
	}
	licenses, err := rootEntry(root, LicensesDir, true)
	if err != nil || !licenses {
		return "", err
	}
	count, err := reuseHeaderFiles(ctx, root, hiss.GitVisiblePaths(ctx, root).Files())
	if err != nil || count == 0 {
		return "", err
	}
	return fmt.Sprintf("REUSE by headers only, no %s: the REUSE gate is not rendered; add %s to keep it "+
		"(%d file(s) carry %s headers or .license companions, %s/ holds the texts)",
		ReuseFile, ReuseFile, count, strings.TrimSuffix(reuseHeaderTag, ":"), LicensesDir), nil
}

// reuseHeaderFiles counts the files among rels, outside LICENSES/ and .reuse/, that are .license
// companions or carry reuseHeaderTag in their first reuseHeaderBytes. A file that is not a regular
// file, or no longer exists, is skipped.
func reuseHeaderFiles(ctx context.Context, root string, rels []string) (count int, err error) {
	// Every read goes through one handle on root, which follows a path only while it stays inside
	// root (os.Root), so a tracked path cannot reach a file outside the repository.
	dir, err := os.OpenRoot(root)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", root, err)
	}
	defer func() {
		if closeErr := dir.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", root, closeErr)
		}
	}()
	for i := 0; i < len(rels) && i < maxReuseHeaderFiles; i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		labelled, err := reuseLabelled(dir, rels[i])
		if err != nil {
			return 0, err
		}
		if labelled {
			count++
		}
	}
	return count, nil
}

// reuseLabelled reports whether rel counts for reuseHeaderFiles: a .license companion, or a file
// that carries reuseHeaderTag. Paths under LICENSES/ and .reuse/ never count.
func reuseLabelled(dir *os.Root, rel string) (bool, error) {
	if strings.HasPrefix(rel, LicensesDir+"/") || strings.HasPrefix(rel, ".reuse/") {
		return false, nil
	}
	if strings.HasSuffix(rel, ".license") {
		return true, nil
	}
	return carriesReuseHeader(dir, filepath.FromSlash(rel))
}

// carriesReuseHeader reports whether the regular file name inside dir holds reuseHeaderTag in its
// first reuseHeaderBytes. Only the head is read: a large file is not refused, unlike the
// whole-file readers in internal/util, because the header sits at its top.
func carriesReuseHeader(dir *os.Root, name string) (bool, error) {
	info, err := dir.Lstat(name)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", name, err)
	}
	file, err := dir.Open(name)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", name, err)
	}
	head, err := io.ReadAll(io.LimitReader(file, reuseHeaderBytes))
	closeErr := file.Close()
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close %s: %w", name, closeErr)
	}
	return bytes.Contains(head, []byte(reuseHeaderTag)), nil
}
