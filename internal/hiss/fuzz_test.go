package hiss

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fuzzSeeds are syntactically clean inputs in every scanned language; each must
// yield zero violations (the negative dimension of HISS-15).
var fuzzSeeds = []struct {
	data []byte
	name string
}{
	{[]byte("package test\nfunc foo() {}\n"), "foo.go"},
	{[]byte("def bar():\n    pass\n"), "bar.py"},
	{[]byte("fn baz() -> () {}\n"), "baz.rs"},
	{[]byte("void qux(void) {}\n"), "qux.c"},
}

func TestFuzzSeedsAreClean(t *testing.T) {
	for _, seed := range fuzzSeeds {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, seed.name), seed.data, 0o644); err != nil {
			t.Fatal(err)
		}
		rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60, Cap: 50})
		if rep.TotalInfractions != 0 {
			t.Errorf("seed %s must be clean, got %+v", seed.name, rep.Violations)
		}
	}
}

func FuzzHissScan(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed.data, seed.name)
	}

	f.Fuzz(func(t *testing.T, data []byte, filename string) {
		if len(filename) > 32 || len(filename) == 0 {
			filename = "input.go"
		}
		filename = filepath.Base(filename)
		if filename == "." || filename == "/" || filename == ".." {
			filename = "test.go"
		}

		tmpDir := t.TempDir()
		target := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Skipf("unwritable fuzz filename %q: %v", filename, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		rep, err := Scan(ctx, tmpDir, ScanOptions{MaxFuncLOC: 60, Cap: 50})
		if err != nil {
			t.Fatalf("Scan of a readable directory must not fail: %v", err)
		}
		if rep == nil {
			t.Fatal("Scan returned nil report without error")
		}
		assertReportInvariants(t, rep, filename, bytes.Count(data, []byte("\n"))+1)
	})
}

// assertReportInvariants checks the structural contract of a report: every violation
// points at an existing line of the scanned file, carries a rule, and the totals and
// breakdown agree with the violation list.
func assertReportInvariants(t *testing.T, rep *ScanReport, filename string, lineCount int) {
	t.Helper()
	if rep.TotalInfractions != len(rep.Violations) {
		t.Fatalf("TotalInfractions=%d but %d violations", rep.TotalInfractions, len(rep.Violations))
	}
	if rep.TotalInfractions > 50 || (rep.Truncated && rep.TotalInfractions != 50) {
		t.Fatalf("cap contract violated: total=%d truncated=%v", rep.TotalInfractions, rep.Truncated)
	}
	sum := 0
	for _, n := range rep.Breakdown {
		sum += n
	}
	if sum != rep.TotalInfractions {
		t.Fatalf("breakdown sums to %d, total is %d", sum, rep.TotalInfractions)
	}
	for _, v := range rep.Violations {
		if v.RuleID == "" || v.Message == "" {
			t.Fatalf("violation without rule or message: %+v", v)
		}
		if filepath.ToSlash(v.FilePath) != filename {
			t.Fatalf("violation attributed to %q, scanned %q", v.FilePath, filename)
		}
		if v.LineNumber < 1 || v.LineNumber > lineCount {
			t.Fatalf("line %d outside the %d-line file: %+v", v.LineNumber, lineCount, v)
		}
	}
}
