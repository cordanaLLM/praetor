package hiss

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanReportsUnanalyzedCSharp(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Plugin.cs", "class Plugin { void Run() { throw new Exception(); } }")
	report := scanFixture(t, root, ScanOptions{})
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Coverage *struct {
			FilesRead      int            `json:"files_read"`
			UnscannedFiles int            `json:"unscanned_files"`
			Extensions     map[string]int `json:"unscanned_by_extension"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Coverage == nil || result.Coverage.FilesRead != 0 || result.Coverage.UnscannedFiles != 1 || result.Coverage.Extensions[".cs"] != 1 {
		t.Fatalf("unsupported C# source must retain explicit unscanned scope: %s", data)
	}
}

func TestScanCoverageMixedAndEmptyInputs(t *testing.T) {
	empty := scanFixture(t, t.TempDir(), ScanOptions{})
	if empty.Coverage == nil || empty.Coverage.FilesRead != 0 || empty.Coverage.UnscannedFiles != 0 {
		t.Fatalf("empty scope must be explicit: %+v", empty.Coverage)
	}
	root := t.TempDir()
	for path, data := range map[string]string{
		"main.go": "package main\n", "helper.py": "answer = 42\n",
		"Plugin.cs": "class Plugin {}", "More.CS": "class More {}",
		"README.md": "documentation", "LICENSE": "license",
		"vendor/Ignored.cs": "class Ignored {}",
	} {
		writeFixture(t, root, path, data)
	}
	report := scanFixture(t, root, ScanOptions{})
	c := report.Coverage
	if c.FilesRead != 2 || c.UnscannedFiles != 4 || c.UnscannedByExtension[".cs"] != 2 || c.UnscannedByExtension[""] != 1 || c.UnscannedByExtension[".md"] != 1 || report.Skips.DirCount != 1 {
		t.Fatalf("mixed scope differs: %+v; skips %+v", c, report.Skips)
	}
}

func TestScanCoverageExtensionBounds(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxCoverageExtensions; i++ {
		writeFixture(t, root, fmt.Sprintf("a%02d.e%02d", i, i), "")
	}
	at := scanFixture(t, root, ScanOptions{}).Coverage
	if len(at.UnscannedByExtension) != maxCoverageExtensions || at.UnlistedUnscannedFiles != 0 {
		t.Fatalf("exact extension bound: %+v", at)
	}
	writeFixture(t, root, "z.overflow", "")
	writeFixture(t, root, "zz.e00", "")
	over := scanFixture(t, root, ScanOptions{}).Coverage
	if len(over.UnscannedByExtension) != maxCoverageExtensions || over.UnlistedUnscannedFiles != 1 || over.UnscannedByExtension[".e00"] != 2 || over.UnscannedFiles != maxCoverageExtensions+2 {
		t.Fatalf("overflow must retain counts and known extensions: %+v", over)
	}
	root = t.TempDir()
	writeFixture(t, root, "a."+strings.Repeat("a", maxCoverageExtensionBytes-1), "")
	writeFixture(t, root, "b."+strings.Repeat("b", maxCoverageExtensionBytes), "")
	c := scanFixture(t, root, ScanOptions{}).Coverage
	if c.UnscannedFiles != 2 || len(c.UnscannedByExtension) != 1 || c.UnlistedUnscannedFiles != 1 {
		t.Fatalf("extension byte bound: %+v", c)
	}
}

func TestScanCoverageSkipsAndTruncation(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "large.py", strings.Repeat("x", MaxScanFileSize+1))
	if err := os.Symlink("large.py", filepath.Join(root, "link.py")); err != nil {
		t.Fatal(err)
	}
	r := scanFixture(t, root, ScanOptions{})
	if r.Coverage.FilesRead != 0 || r.Coverage.UnscannedFiles != 0 || r.Skips.Oversize != 1 || r.Skips.Symlinks != 1 {
		t.Fatalf("skipped files must not count as read: %+v", r)
	}
	root = t.TempDir()
	writeFixture(t, root, "a.go", "package p\nfunc f() { panic(1); panic(2) }\n")
	writeFixture(t, root, "z.cs", "class Unvisited {}")
	r = scanFixture(t, root, ScanOptions{Cap: 1})
	if !r.Truncated || r.Coverage.FilesRead != 1 || r.Coverage.UnscannedFiles != 0 {
		t.Fatalf("partial scan must retain traversal limits: %+v", r)
	}
}

func TestHistoricalScanCoverageIsUnknown(t *testing.T) {
	var report ScanReport
	if err := json.Unmarshal([]byte(`{"total_infractions":0}`), &report); err != nil {
		t.Fatal(err)
	}
	if report.Coverage != nil {
		t.Fatal("old report must not synthesize measured coverage")
	}
	for _, unknown := range []*ScanReport{nil, &report} {
		if !strings.Contains(unknown.CoverageEvidence(), "unknown") {
			t.Fatal("missing scope must remain unknown in text consumers")
		}
	}
	empty := scanFixture(t, t.TempDir(), ScanOptions{})
	if !strings.Contains(empty.CoverageEvidence(), "0 files read") {
		t.Fatal("measured empty scope must remain visible")
	}
}
