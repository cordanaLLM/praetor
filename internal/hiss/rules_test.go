package hiss

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Positive / negative / boundary cases for the HISS-07 discard heuristic.
func TestIsDiscardedValue(t *testing.T) {
	cases := map[string]bool{
		"_ = f.Close()":                    true,
		"defer func() { _ = f.Close() }()": true,
		"x, _ = strconv.Atoi(s)":           true, // the dropped second value is almost always an error
		"var _ io.Closer = (*T)(nil)":      false,
		"var _ = json.Marshal":             false,
		"_ := 1":                           false,
		"":                                 false,
	}
	for line, want := range cases {
		if got := isDiscardedValue(line); got != want {
			t.Errorf("isDiscardedValue(%q) = %v, want %v", line, got, want)
		}
	}
}

// Comments and interface assertions must not be counted as HISS-07.
func TestScanGoIgnoresCommentsAndAssertions(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\n// _ = ignored in comments\nvar _ error = (*E)(nil)\n\ntype E struct{}\n\nfunc (E) Error() string { return \"\" }\n\nfunc use(f interface{ Close() error }) {\n\t_ = f.Close()\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Breakdown["HISS-07"] != 1 {
		t.Fatalf("want exactly 1 HISS-07 (the real discard), got %d: %+v", rep.Breakdown["HISS-07"], rep.Violations)
	}
}

// Inside a git work tree, gitignored material is not the repository's debt.
func TestScanSkipsGitignoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("_audit/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "_audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "package p\n\nfunc f() {\n\tfor {\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "_audit", "x.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "y.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Breakdown["HISS-02"] != 1 {
		t.Fatalf("expected only the tracked file's loop to count, got %+v", rep.Violations)
	}
	for _, v := range rep.Violations {
		if filepath.ToSlash(v.FilePath) == "_audit/x.go" {
			t.Fatalf("gitignored file was scanned: %+v", v)
		}
	}
}
