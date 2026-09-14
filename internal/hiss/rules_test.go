package hiss

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestScanGoIgnoresCommentsAndAssertions pins the HISS-07 discard rule against two shapes
// that a text heuristic mistakes for unchecked errors: a comment that happens to contain
// the blank-assignment text, and a compile-time interface assertion, which declares a
// value rather than discarding one. Only the real discard is a violation.
func TestScanGoIgnoresCommentsAndAssertions(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\n// _ = ignored in comments\nvar _ error = (*E)(nil)\n\ntype E struct{}\n\nfunc (E) Error() string { return \"\" }\n\nfunc use(f interface{ Close() error }) {\n\t_ = f.Close()\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rep.Breakdown["HISS-07"] != 1 {
		t.Fatalf("want exactly 1 HISS-07 (the real discard), got %d: %+v", rep.Breakdown["HISS-07"], rep.Violations)
	}
}

// initGitRepo makes dir a git work tree, skipping the test when git is unavailable.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

// scanForTest scans dir under a bounded context.
func scanForTest(t *testing.T, dir string) *ScanReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return rep
}

// badGoSource is a file whose unbounded loop is a single HISS-02 violation.
const badGoSource = "package p\n\nfunc f() {\n\tfor {\n\t}\n}\n"

// TestScanSkipsGitignoredFiles is the positive dimension of the git-visibility rule:
// inside a work tree, gitignored material is not the repository's debt. An audit clone of
// another project under an ignored directory is what inflated a downstream baseline more
// than tenfold, because the walk counted its violations as this repository's own.
func TestScanSkipsGitignoredFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("_audit/\n"), 0o600); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "_audit"), 0o750); err != nil {
		t.Fatalf("create ignored directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_audit", "x.go"), []byte(badGoSource), 0o600); err != nil {
		t.Fatalf("write ignored source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "y.go"), []byte(badGoSource), 0o600); err != nil {
		t.Fatalf("write tracked source: %v", err)
	}

	rep := scanForTest(t, dir)
	if rep.Breakdown["HISS-02"] != 1 {
		t.Fatalf("only the visible file's loop may count, got %d: %+v", rep.Breakdown["HISS-02"], rep.Violations)
	}
	for _, v := range rep.Violations {
		if strings.HasPrefix(filepath.ToSlash(v.FilePath), "_audit/") {
			t.Fatalf("a gitignored file was scanned: %+v", v)
		}
	}
}

// TestScanCountsUntrackedButUnignoredFiles is the negative dimension: the rule excludes
// ignored files, not merely uncommitted ones. A new source file that nothing ignores is
// still the repository's debt, so it must be scanned before it is ever committed.
func TestScanCountsUntrackedButUnignoredFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"), []byte(badGoSource), 0o600); err != nil {
		t.Fatalf("write untracked source: %v", err)
	}

	if rep := scanForTest(t, dir); rep.Breakdown["HISS-02"] != 1 {
		t.Fatalf("an untracked but unignored file must still be scanned, got %d: %+v",
			rep.Breakdown["HISS-02"], rep.Violations)
	}
}

// TestScanOutsideGitWorkTreeStillWalks is the boundary dimension: with no git answer the
// scanner must fall back to walking everything. Failing open over-reports; failing closed
// would silently report a clean scan for a tree it never read.
func TestScanOutsideGitWorkTreeStillWalks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "loose.go"), []byte(badGoSource), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if rep := scanForTest(t, dir); rep.Breakdown["HISS-02"] != 1 {
		t.Fatalf("a non-git tree must still be scanned, got %d: %+v",
			rep.Breakdown["HISS-02"], rep.Violations)
	}
}
