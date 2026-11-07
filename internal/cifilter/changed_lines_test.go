// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cifilter_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestGetChangedLineRanges_Positive verifies added, modified, and pure-deletion hunks against the merge base.
func TestGetChangedLineRanges_Positive(t *testing.T) {
	dir := gitSandbox(t)
	writeRepoFile(t, dir, "a.go", "package a\n\nfunc One() {\n\t_ = 1\n}\n\nfunc Two() {\n\t_ = 2\n}\n")
	writeRepoFile(t, dir, "gone.go", "package a\n")
	runRepoGit(t, dir, "init", "-q", "-b", "main")
	runRepoGit(t, dir, "add", ".")
	runRepoGit(t, dir, "commit", "-q", "-m", "base")
	runRepoGit(t, dir, "checkout", "-q", "-b", "feature")

	writeRepoFile(t, dir, "a.go", "package a\n\nfunc Two() {\n\t_ = 20\n\t_ = 21\n}\n")
	writeRepoFile(t, dir, "pkg/ä.go", "package pkg\n\nfunc Ändern() {\n\t_ = 1\n}\n")
	runRepoGit(t, dir, "rm", "-q", "gone.go")
	runRepoGit(t, dir, "add", "a.go", "pkg/ä.go")
	runRepoGit(t, dir, "commit", "-q", "-m", "modify a.go, add non-ASCII file, and delete gone.go")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	ranges, err := cifilter.GetChangedLineRanges(ctx, dir, "main", "HEAD")
	if err != nil {
		t.Fatalf("GetChangedLineRanges: %v", err)
	}
	if _, deletedPresent := ranges["gone.go"]; deletedPresent {
		t.Fatalf("deleted file gone.go must not appear in changed line ranges: %+v", ranges)
	}
	got := ranges["a.go"]
	if len(got) != 1 || got[0] != (cifilter.LineRange{Start: 4, End: 5}) {
		t.Fatalf("a.go ranges = %+v, want [{Start:4 End:5}]", got)
	}
	gotNonASCII := ranges["pkg/ä.go"]
	if len(gotNonASCII) != 1 || gotNonASCII[0] != (cifilter.LineRange{Start: 1, End: 5}) {
		t.Fatalf("pkg/ä.go ranges = %+v, want [{Start:1 End:5}]", gotNonASCII)
	}
}

// TestGetChangedLineRanges_Negative verifies that nil context, unresolvable refs, or quoted control-byte paths fail closed.
func TestGetChangedLineRanges_Negative(t *testing.T) {
	dir := docsBranchRepo(t)
	//nolint:staticcheck // Deliberate nil context test for fail-closed guard.
	if _, err := cifilter.GetChangedLineRanges(nil, dir, "main", "HEAD"); err == nil {
		t.Fatal("nil context must fail")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := cifilter.GetChangedLineRanges(ctx, dir, "no-such-ref", "HEAD"); err == nil {
		t.Fatal("unresolvable base ref must fail")
	}

	t.Run("quoted control-byte target fails closed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("git for Windows refuses a tab in an index path (update-index: Invalid path), so the quoted control-byte target cannot be planted; the case runs on Linux and macOS (HISS-21)")
		}
		blobOID, err := util.RunGit(t.Context(), dir, "rev-parse", "HEAD:README.md")
		if err != nil {
			t.Fatalf("rev-parse HEAD:README.md: %v", err)
		}
		runRepoGit(t, dir, "update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(blobOID)+",tab\tname.go")
		runRepoGit(t, dir, "commit", "-q", "-m", "add control-byte path")
		if _, err := cifilter.GetChangedLineRanges(t.Context(), dir, "main", "HEAD"); err == nil {
			t.Fatal("quoted diff target line with control byte must return an error instead of being dropped")
		}
	})
}

// TestGetChangedLineRanges_Boundary verifies single-line hunks, worktree diffs, and LineRange.Overlaps boundaries.
func TestGetChangedLineRanges_Boundary(t *testing.T) {
	dir := gitSandbox(t)
	writeRepoFile(t, dir, "single.go", "package s\n")
	runRepoGit(t, dir, "init", "-q", "-b", "main")
	runRepoGit(t, dir, "add", ".")
	runRepoGit(t, dir, "commit", "-q", "-m", "base")

	writeRepoFile(t, dir, "single.go", "package s\nfunc Touch() {}\n")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	ranges, err := cifilter.GetChangedLineRanges(ctx, dir, "main", "WORKTREE")
	if err != nil {
		t.Fatalf("WORKTREE diff failed: %v", err)
	}
	got := ranges["single.go"]
	if len(got) != 1 || got[0] != (cifilter.LineRange{Start: 2, End: 2}) {
		t.Fatalf("single.go worktree range = %+v, want [{Start:2 End:2}]", got)
	}
	r := got[0]
	if !r.Overlaps(2, 5) || !r.Overlaps(1, 2) {
		t.Fatalf("expected boundary overlap on line 2")
	}
	if r.Overlaps(3, 5) || r.Overlaps(1, 1) {
		t.Fatalf("unexpected overlap outside line 2")
	}
}
