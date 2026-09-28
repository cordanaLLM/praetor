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

// TestGitVisiblePathsAnswersTheScanScope exercises the exported inventory that adopt's C source
// detection reads beside the scan. Positive: an untracked file no rule ignores, one directory
// down, is visible under its slash-separated path. Negative: a file an ignore rule matches is
// not. Boundary: outside a work tree git gives no answer, the tree is nil, and a nil tree reports
// every file visible, as the scan's fallback walk does.
func TestGitVisiblePathsAnswersTheScanScope(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	for rel, body := range map[string]string{".gitignore": "*.c\n", "src/probe.h": "int p;\n", "src/fast.c": "int f;\n"} {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	visible := GitVisiblePaths(ctx, repo)
	if visible == nil || !visible.HasFile("src/probe.h") || !visible.HasFile(".gitignore") {
		t.Fatalf("GitVisiblePaths(work tree) = %+v; want src/probe.h and .gitignore visible", visible)
	}
	if visible.HasFile("src/fast.c") {
		t.Fatalf("an ignored file is visible: %+v", visible)
	}
	outside := GitVisiblePaths(ctx, t.TempDir())
	if outside != nil || !outside.HasFile("src/fast.c") {
		t.Fatalf("GitVisiblePaths(plain directory) = %+v; want nil, every file visible", outside)
	}
}

// TestIsNativeExtensionMatchesTheScanDispatch ties the exported extension answer adopt's C/C++
// source detection reads to the files Scan actually runs the native checks on, so the two cannot
// drift. Positive: every native extension, in any case, is reported and scanned (a banned
// strcpy is a HISS-08 finding). Negative: Objective-C, the C++ spellings and CUDA header the
// scanner has no dispatch for, and a non-source file, are neither. Boundary: a file with no
// extension, and a lone dot, are not native.
func TestIsNativeExtensionMatchesTheScanDispatch(t *testing.T) {
	cases := map[string]bool{
		".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".h": true, ".hpp": true, ".cu": true, ".hip": true,
		".C": true, ".CPP": true, ".H": true,
		".m": false, ".mm": false, ".hh": false, ".hxx": false, ".cuh": false, ".txt": false,
		"": false, ".": false,
	}
	const source = "void copy(char *d, const char *s) { strcpy(d, s); }\n"
	for ext, want := range cases {
		if got := IsNativeExtension(ext); got != want {
			t.Errorf("IsNativeExtension(%q) = %v, want %v", ext, got, want)
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "widget"+ext), []byte(source), 0o600); err != nil {
			t.Fatalf("write widget%s: %v", ext, err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		rep, err := Scan(ctx, dir, ScanOptions{})
		cancel()
		if err != nil {
			t.Fatalf("scan widget%s: %v", ext, err)
		}
		if scanned := rep.Breakdown["HISS-08"] > 0; scanned != want {
			t.Errorf("Scan(widget%s) HISS-08 found = %v, want %v", ext, scanned, want)
		}
	}
}
