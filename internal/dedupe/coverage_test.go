// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package dedupe_test

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
)

// #161: in a polyglot repository with a small Go module the scan read only the Go files and
// reported a clean pass with no word about the rest. The report now counts the source it
// could not read, per language, and marks the verdict partial.

const polyglotGoSource = "package tooling\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"

func TestScanRepo_Positive_PolyglotVerdictIsPartial(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tooling/add.go", polyglotGoSource)
	writeFile(t, dir, "crates/core/src/lib.rs", "pub fn add() {}\n")
	writeFile(t, dir, "crates/core/src/util.rs", "pub fn sub() {}\n")
	writeFile(t, dir, "crates/core/native/shim.c", "int shim(void) { return 0; }\n")
	writeFile(t, dir, "README.md", "# polyglot\n")

	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applicable || !report.Passed || !report.Partial || report.TotalFilesScanned != 1 {
		t.Fatalf("a clean Go module beside Rust and C must pass partially: %+v", report)
	}
	if want := map[string]int{"rust": 2, "c": 1}; !maps.Equal(report.Unscanned, want) {
		t.Fatalf("Unscanned = %v, want %v", report.Unscanned, want)
	}
	if got := report.UnscannedLanguages(); len(got) != 2 || got[0] != "c" || got[1] != "rust" {
		t.Fatalf("UnscannedLanguages() = %v, want [c rust]", got)
	}
}

// A Go tool beside a JVM and JavaScript tree was still reported as a plain pass while the
// language table knew only nine languages: every language the scan cannot read has to count.
func TestScanRepo_Positive_JVMAndModuleSourcesMakeTheVerdictPartial(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tooling/add.go", polyglotGoSource)
	writeFile(t, dir, "java/A.java", "class A {}\n")
	writeFile(t, dir, "java/B.kt", "class B\n")
	writeFile(t, dir, "tools/build.mjs", "export const x = 1;\n")
	writeFile(t, dir, "src/Program.cs", "class Program {}\n")

	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.Partial {
		t.Fatalf("a clean Go tool beside Java, Kotlin, JavaScript and C# must pass partially: %+v", report)
	}
	want := map[string]int{"java": 1, "kotlin": 1, "javascript": 1, "csharp": 1}
	if !maps.Equal(report.Unscanned, want) {
		t.Fatalf("Unscanned = %v, want %v", report.Unscanned, want)
	}
}

func TestScanRepo_Negative_GoOnlyRepositoryIsNotPartial(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "add.go", polyglotGoSource)
	writeFile(t, dir, "add_test.go", "package tooling\n")
	writeFile(t, dir, "docs/guide.md", "# guide\n")
	writeFile(t, dir, "config.yaml", "key: value\n")

	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Partial || len(report.Unscanned) != 0 || !report.Passed {
		t.Fatalf("a Go-only repository must get a full verdict: %+v", report)
	}
}

// Fixture corpora in any language stay out of the count, a repository without Go reports
// its languages without a verdict, and a tracked file deleted from the working tree is not
// counted.
func TestScanRepo_Boundary_UnscannedScopeFollowsTheGoScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/app.ts", "export const a = 1;\n")
	writeFile(t, dir, "src/view.svelte", "<p>hi</p>\n")
	writeFile(t, dir, "testdata/fixture.py", "print('fixture')\n")
	writeFile(t, dir, "node_modules/dep/index.js", "module.exports = 1;\n")

	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Applicable || report.Partial || report.Passed {
		t.Fatalf("a repository without Go has no verdict, partial or not: %+v", report)
	}
	if want := map[string]int{"typescript": 1, "svelte": 1}; !maps.Equal(report.Unscanned, want) {
		t.Fatalf("Unscanned = %v, want %v", report.Unscanned, want)
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeFile(t, repo, "add.go", polyglotGoSource)
	writeFile(t, repo, "gone.rs", "fn gone() {}\n")
	writeFile(t, repo, ".gitignore", "build/\n")
	writeFile(t, repo, "build/out.js", "var x = 1;\n")
	runGit(t, repo, "add", "add.go", "gone.rs", ".gitignore")
	if err := os.Remove(filepath.Join(repo, "gone.rs")); err != nil {
		t.Fatal(err)
	}
	report, err = dedupe.ScanRepoContext(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Partial || len(report.Unscanned) != 0 {
		t.Fatalf("a deleted tracked file and an ignored build output must not be counted: %+v", report)
	}
}

// SourceLanguageCounts is the scan's inventory with Go counted too: the pre-migration epic
// names the languages no needs analyzer detects from it (#296).
func TestSourceLanguageCounts_3D(t *testing.T) {
	// Positive: Go production source and every other language, per language.
	dir := t.TempDir()
	writeFile(t, dir, "tooling/add.go", polyglotGoSource)
	writeFile(t, dir, "tooling/add_test.go", "package tooling\n")
	writeFile(t, dir, "scripts/build.sh", "#!/bin/sh\n")
	writeFile(t, dir, "scripts/release.sh", "#!/bin/sh\n")
	writeFile(t, dir, "tests/test_boot.py", "def test_boot(): pass\n")
	counts, err := dedupe.SourceLanguageCounts(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"go": 1, "shell": 2, "python": 1}; !maps.Equal(counts, want) {
		t.Fatalf("SourceLanguageCounts = %v, want %v", counts, want)
	}
	// Negative: a cancelled context is an error, not an empty inventory.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := dedupe.SourceLanguageCounts(cancelled, dir); err == nil {
		t.Fatal("a cancelled inventory returned counts")
	}
	// Boundary: a repository holding no source counts nothing, and fixtures stay out.
	empty := t.TempDir()
	writeFile(t, empty, "README.md", "# docs\n")
	writeFile(t, empty, "testdata/case.sh", "#!/bin/sh\n")
	if counts, err := dedupe.SourceLanguageCounts(t.Context(), empty); err != nil || len(counts) != 0 {
		t.Fatalf("docs-only inventory = %v, %v; want none", counts, err)
	}
}

// LanguageFileCounts renders the Not Scanned line and the epic's unanalyzed languages.
func TestLanguageFileCounts_3D(t *testing.T) {
	// Positive: sorted by language, plural counts.
	if got := dedupe.LanguageFileCounts(map[string]int{"shell": 9, "python": 11}); got != "python (11 files), shell (9 files)" {
		t.Errorf("LanguageFileCounts = %q", got)
	}
	// Negative: no counts render nothing.
	if got := dedupe.LanguageFileCounts(nil); got != "" {
		t.Errorf("LanguageFileCounts(nil) = %q, want empty", got)
	}
	// Boundary: one file is singular.
	if got := dedupe.LanguageFileCounts(map[string]int{"zig": 1}); got != "zig (1 file)" {
		t.Errorf("LanguageFileCounts(one) = %q", got)
	}
}
