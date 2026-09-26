package hiss

import "testing"

// writePanicFiles writes one Go source per name, each carrying exactly one HISS-07
// infraction, so a violation count is also a count of the files the walk reached.
func writePanicFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for i := 0; i < len(names) && i < 64; i++ {
		writeFixture(t, root, names[i]+".go", "package p\n\nfunc "+names[i]+"() {\n\tpanic(\"x\")\n}\n")
	}
}

// TestScan_FileBoundMarksTruncation pins the walk's scalar file bound (HISS-02): an
// ordinary tree is unaffected, exactly MaxFiles entries complete, and one entry more
// stops the walk and reports the result as a lower bound rather than a verdict.
func TestScan_FileBoundMarksTruncation(t *testing.T) {
	root := t.TempDir()
	writePanicFiles(t, root, "a", "b", "c")

	ordinary := scanFixture(t, root, ScanOptions{})
	if ordinary.Truncated || ordinary.TotalInfractions != 3 {
		t.Fatalf("the default bound must not truncate a three-file tree, got truncated=%v total=%d",
			ordinary.Truncated, ordinary.TotalInfractions)
	}
	if ordinary.Incomplete() {
		t.Error("an unbounded-by-size tree must not report an incomplete scan")
	}

	exact := scanFixture(t, root, ScanOptions{MaxFiles: 3})
	if exact.Truncated || exact.TotalInfractions != 3 {
		t.Fatalf("exactly MaxFiles entries must complete, got truncated=%v total=%d",
			exact.Truncated, exact.TotalInfractions)
	}

	over := scanFixture(t, root, ScanOptions{MaxFiles: 2})
	if !over.Truncated || over.TotalInfractions != 2 {
		t.Fatalf("one entry past MaxFiles must truncate, got truncated=%v total=%d",
			over.Truncated, over.TotalInfractions)
	}
	if !over.Incomplete() {
		t.Error("a truncated report must answer Incomplete true")
	}
}

// TestScan_FileBoundRejectsNonPositiveValues keeps a zero or negative bound from
// truncating every scan at the first file: withDefaults must substitute the default.
func TestScan_FileBoundRejectsNonPositiveValues(t *testing.T) {
	root := t.TempDir()
	writePanicFiles(t, root, "a", "b")

	for _, bound := range []int{0, -1} {
		rep := scanFixture(t, root, ScanOptions{MaxFiles: bound})
		if rep.Truncated || rep.TotalInfractions != 2 {
			t.Errorf("MaxFiles=%d must fall back to DefaultMaxScanFiles, got truncated=%v total=%d",
				bound, rep.Truncated, rep.TotalInfractions)
		}
	}
	if DefaultMaxScanFiles <= 0 {
		t.Fatalf("DefaultMaxScanFiles must be a positive scalar bound, got %d", DefaultMaxScanFiles)
	}
}

// TestScan_FileBoundCountsSkippedEntries pins what the bound counts: every file entry the
// walk visits, whether or not it is scannable. A bound that only counted scanned files
// would leave the traversal itself unbounded.
func TestScan_FileBoundCountsSkippedEntries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "notes.txt", "not source\n")
	writeFixture(t, root, "readme.md", "not source\n")
	writePanicFiles(t, root, "z")

	rep := scanFixture(t, root, ScanOptions{MaxFiles: 2})
	if !rep.Truncated {
		t.Fatalf("unscannable entries must count against the bound, got %+v", rep.Skips)
	}
	if rep.TotalInfractions != 0 {
		t.Errorf("the walk must stop before z.go, got %d infraction(s)", rep.TotalInfractions)
	}
}
