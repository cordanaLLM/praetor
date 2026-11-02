// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureDatesReplay(t *testing.T) {
	for _, language := range []string{"go", "python"} {
		for _, bucket := range []string{"positive", "negative"} {
			replayDateBucket(t, language, bucket)
		}
	}
}

func replayDateBucket(t *testing.T, language, bucket string) {
	t.Helper()
	root := filepath.Join("..", "..", ".config", "hiss", "testdata", "HISS-15", language, bucket)
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, file := range files {
		if !strings.Contains(file.Name(), "date-") && !strings.Contains(file.Name(), "date_") {
			continue
		}
		count++
		t.Run(language+"/"+bucket+"/"+file.Name(), func(t *testing.T) {
			replayDateFixture(t, root, file.Name(), bucket == "positive")
		})
	}
	if count == 0 {
		t.Fatal("clock fixture corpus is empty")
	}
}

// Exercise the actual scanner, including the reported physical location and fix.
func replayDateFixture(t *testing.T, root, name string, reject bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFixture(t, dir, name, string(data))
	report := scanFixture(t, dir, ScanOptions{})
	if (report.Breakdown["HISS-15"] > 0) != reject {
		t.Fatalf("want rejection %v: %+v", reject, report.Violations)
	}
	lines := strings.Split(string(data), "\n")
	for _, v := range report.Violations {
		if v.RuleID == "HISS-15" {
			assertDateFinding(t, v, name, lines)
		}
	}
}

func assertDateFinding(t *testing.T, v InvariantViolation, name string, lines []string) {
	t.Helper()
	if v.FilePath != name || !strings.Contains(v.Message, "derive it from the test clock") {
		t.Errorf("missing actionable location/fix: %+v", v)
	}
	if v.LineNumber < 1 || v.LineNumber > len(lines) {
		t.Fatalf("line outside fixture: %+v", v)
	}
	if !fixtureDate.MatchString(lines[v.LineNumber-1]) && !strings.Contains(lines[v.LineNumber-1], "date-finding") {
		t.Errorf("finding does not point at its date: %+v", v)
	}
}

// Reuse the source rule locally, without a baseline hiding a newly planted date.
func TestFixtureDatesRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	visible := GitVisiblePaths(t.Context(), root)
	if visible == nil {
		t.Fatal("test date inventory requires the repository's tracked source paths")
	}
	paths := visible.Files()
	if len(paths) >= DefaultMaxScanFiles {
		t.Fatal("test date inventory exceeded its file bound")
	}
	for i := 0; i < len(paths); i++ {
		if isFixtureDateSource(paths[i]) {
			checkRepositoryTestDates(t, root, paths[i])
		}
	}
}

func isFixtureDateSource(rel string) bool {
	if strings.Contains("/"+rel, "/testdata/") {
		return false
	}
	return strings.HasSuffix(rel, "_test.go") || (strings.HasSuffix(rel, ".py") && isPythonTest(rel))
}

func checkRepositoryTestDates(t *testing.T, root, rel string) {
	t.Helper()
	data, err := readBounded(root, rel)
	if err != nil {
		t.Fatal(err)
	}
	report := newScanReport(MaxInfractionsCap)
	src := sourceFile{rel: rel, data: data}
	scanGoFixtureDates(src, report)
	if strings.HasSuffix(rel, ".py") {
		scanPythonFixtureDates(src, report)
	}
	for _, v := range report.Violations {
		t.Errorf("%s:%d: %s", v.FilePath, v.LineNumber, v.Message)
	}
}
