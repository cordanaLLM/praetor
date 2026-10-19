// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"testing"
)

// TestObjectiveCpp_Positive_GotoReportedHISS01 verifies that a .mm file with a one-line
// forward goto is reported under HISS-01.
func TestObjectiveCpp_Positive_GotoReportedHISS01(t *testing.T) {
	root := t.TempDir()
	src := "int f(int rc) {\n    if (rc) goto out;\n    return 0;\nout:\n    return rc;\n}\n"
	writeFixture(t, root, "handler.mm", src)
	rep := scanFixture(t, root, ScanOptions{})
	if rep.Breakdown["HISS-01"] != 1 {
		t.Fatalf("handler.mm: want 1 HISS-01 finding, got %d (breakdown: %+v)", rep.Breakdown["HISS-01"], rep.Breakdown)
	}
}

// TestObjectiveCpp_Positive_BannedLibcReportedHISS08 verifies that a .mm file with a banned libc
// call is reported under HISS-08 like a .cpp file.
func TestObjectiveCpp_Positive_BannedLibcReportedHISS08(t *testing.T) {
	root := t.TempDir()
	src := "void copy(char *d, const char *s) { strcpy(d, s); }\n"
	writeFixture(t, root, "widget.mm", src)
	rep := scanFixture(t, root, ScanOptions{})
	if rep.Breakdown["HISS-08"] != 1 {
		t.Fatalf("widget.mm: want 1 HISS-08 finding, got %d (breakdown: %+v)", rep.Breakdown["HISS-08"], rep.Breakdown)
	}
}

// TestObjectiveCpp_Negative_NoGotoNoHISS01 verifies that a .mm file without goto has no HISS-01 finding.
func TestObjectiveCpp_Negative_NoGotoNoHISS01(t *testing.T) {
	root := t.TempDir()
	src := "int f(int rc) {\n    if (rc) { return rc; }\n    return 0;\n}\n"
	writeFixture(t, root, "clean.mm", src)
	rep := scanFixture(t, root, ScanOptions{})
	if rep.Breakdown["HISS-01"] != 0 {
		t.Fatalf("clean.mm: want 0 HISS-01 findings, got %d", rep.Breakdown["HISS-01"])
	}
}

// TestObjectiveC_Negative_MFileNotScannedEvenWithGoto verifies that a .m file is not scanned
// even when it contains a goto statement.
func TestObjectiveC_Negative_MFileNotScannedEvenWithGoto(t *testing.T) {
	root := t.TempDir()
	src := "int f(int rc) {\n    if (rc) goto out;\n    return 0;\nout:\n    return rc;\n}\n"
	writeFixture(t, root, "legacy.m", src)
	rep := scanFixture(t, root, ScanOptions{})
	if len(rep.Violations) != 0 || rep.Breakdown["HISS-01"] != 0 {
		t.Fatalf("legacy.m: must not be scanned, got violations: %+v", rep.Violations)
	}
	if rep.Coverage.FilesRead != 0 || rep.Coverage.UnscannedFiles != 1 {
		t.Fatalf("legacy.m: want 0 files read, 1 unscanned, got %+v", rep.Coverage)
	}
}

type objCCoverageCase struct {
	name         string
	setup        func(t *testing.T, root string)
	wantObjCCnt  int
	wantExtCount int
}

func checkObjCCoverageCase(t *testing.T, tc objCCoverageCase) {
	t.Helper()
	root := t.TempDir()
	tc.setup(t, root)
	rep := scanFixture(t, root, ScanOptions{})
	c := rep.Coverage
	if c == nil {
		t.Fatal("nil coverage")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("ScanCoverage.Validate() failed: %v", err)
	}
	if got := c.UnscannedLanguages["objective-c"]; got != tc.wantObjCCnt {
		t.Errorf("UnscannedLanguages[objective-c] = %d, want %d", got, tc.wantObjCCnt)
	}
	if got := c.UnscannedByExtension[".m"]; got != tc.wantExtCount {
		t.Errorf("UnscannedByExtension[.m] = %d, want %d", got, tc.wantExtCount)
	}
}

// TestObjectiveC_Coverage_MarkerLabelsUnscannedObjectiveC verifies that .m files are labelled as
// unscanned objective-c only when an Objective-C marker is present, and that Validate passes.
func TestObjectiveC_Coverage_MarkerLabelsUnscannedObjectiveC(t *testing.T) {
	cases := []objCCoverageCase{
		{
			name: ".m next to a .mm",
			setup: func(t *testing.T, root string) {
				writeFixture(t, root, "bridge.mm", "void bridge() {}\n")
				writeFixture(t, root, "impl.m", "void impl() {}\n")
			},
			wantObjCCnt:  1,
			wantExtCount: 1,
		},
		{
			name: ".m with a foo.xcodeproj directory",
			setup: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "foo.xcodeproj"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, root, "impl.m", "void impl() {}\n")
			},
			wantObjCCnt:  1,
			wantExtCount: 1,
		},
		{
			name: ".m with a .metal file",
			setup: func(t *testing.T, root string) {
				writeFixture(t, root, "shader.metal", "kernel void compute() {}\n")
				writeFixture(t, root, "impl.m", "void impl() {}\n")
			},
			wantObjCCnt:  1,
			wantExtCount: 1,
		},
		{
			name: ".m alone is not labelled objective-c",
			setup: func(t *testing.T, root string) {
				writeFixture(t, root, "math.m", "function y = math(x)\ny = x + 1;\n")
			},
			wantObjCCnt:  0,
			wantExtCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkObjCCoverageCase(t, tc) })
	}
}
