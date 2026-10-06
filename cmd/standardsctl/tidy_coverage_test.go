// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// tidyCoverageRepo makes a git repository tracking files (path to content) and returns its root.
func tidyCoverageRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	for rel, content := range files {
		writeTidyCoverageFile(t, root, rel, content)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "-A"})
	return root
}

func writeTidyCoverageFile(t *testing.T, root, rel, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tidyCoverageManifest is a manifest with one compile-database lane and, when excepted is not
// empty, one exceptions entry for it expiring 30 days from now.
func tidyCoverageManifest(excepted string) string {
	manifest := "version: 1\nclang_tidy:\n  lanes:\n    - name: \"cpu\"\n      compile_database: \"build/compile_commands.json\"\n"
	if excepted == "" {
		return manifest
	}
	expires := config.ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	return manifest + fmt.Sprintf("exceptions:\n  - rule: \"clang-tidy-coverage\"\n    path: %q\n    reason: \"Windows-only unit\"\n    expires: %q\n",
		excepted, expires)
}

func writeTidyCoverageDatabase(t *testing.T, root string, units ...string) {
	t.Helper()
	type entry struct {
		Directory string `json:"directory"`
		File      string `json:"file"`
	}
	entries := make([]entry, 0, len(units))
	for _, unit := range units {
		entries = append(entries, entry{Directory: root, File: filepath.Join(root, filepath.FromSlash(unit))})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeTidyCoverageFile(t, root, "build/compile_commands.json", string(data))
}

// Positive: `ci tidy-coverage` passes a repository whose units a lane reads or a live
// exception excuses, and names what each lane read.
func TestRunCITidyCoverage_Positive_LaneAndException(t *testing.T) {
	root := tidyCoverageRepo(t, map[string]string{
		"src/a.c": "int a;\n", "src/win32/getopt.c": "int g;\n", ".standards.yaml": tidyCoverageManifest("src/win32/getopt.c"),
	})
	writeTidyCoverageDatabase(t, root, "src/a.c")
	out, err := captureStdout(t, func() error { return runCITidyCoverage(t.Context(), []string{"--dir=" + root}) })
	if err != nil || !strings.Contains(out, "[PASS] clang-tidy translation-unit coverage: all 2 tracked translation units read by a lane (cpu: 1) or excused by a live exception (1).") {
		t.Fatalf("ci tidy-coverage = %v\n%s", err, out)
	}
}

// Negative: a planted unit outside every lane fails naming the file, a missing compile
// database fails closed, and a stray argument is refused.
func TestRunCITidyCoverage_Negative_PlantedUnitAndMissingDatabase(t *testing.T) {
	root := tidyCoverageRepo(t, map[string]string{
		"src/a.c": "int a;\n", "src/planted.cpp": "int p;\n", ".standards.yaml": tidyCoverageManifest(""),
	})
	writeTidyCoverageDatabase(t, root, "src/a.c")
	out, err := captureStdout(t, func() error { return runCITidyCoverage(t.Context(), []string{"--dir=" + root}) })
	if err == nil || !strings.Contains(out, "  - src/planted.cpp: read by no clang-tidy lane and named by no exceptions entry") ||
		!strings.Contains(err.Error(), "1 problem(s) over 2 tracked translation units") {
		t.Fatalf("ci tidy-coverage = %v\n%s", err, out)
	}
	if err := os.RemoveAll(filepath.Join(root, "build")); err != nil {
		t.Fatal(err)
	}
	_, err = captureStdout(t, func() error { return runCITidyCoverage(t.Context(), []string{"--dir=" + root}) })
	if err == nil || !strings.Contains(err.Error(), "could not run: clang-tidy lane cpu: compile database build/compile_commands.json cannot be read") {
		t.Fatalf("a missing compile database must fail closed, got %v", err)
	}
	if err := runCITidyCoverage(t.Context(), []string{"--dir=" + root, "extra"}); err == nil {
		t.Fatal("a positional argument must be refused")
	}
}

// Boundary: the audit runs the gate only when the resolved linters name clang-tidy or lanes
// are declared, and says so when it does not; enabled over a repository without C/C++ it skips
// with the reason; enabled with no lanes declared it fails on every unit and names the missing
// declaration.
func TestAuditTidyCoverage_Boundary_EnablementAndSkips(t *testing.T) {
	withC := tidyCoverageRepo(t, map[string]string{"src/a.c": "int a;\n"})
	manifest := &config.Manifest{Version: 1}
	out, err := captureStdout(t, func() error {
		return auditTidyCoverage(t.Context(), manifest, withC, &config.ResolvedPolicy{Linters: []string{"golangci-lint"}})
	})
	if err != nil || !strings.Contains(out, "[SKIP] clang-tidy translation-unit coverage not checked: the resolved policy's linters do not name clang-tidy") {
		t.Fatalf("audit without clang-tidy = %v\n%s", err, out)
	}
	native := &config.ResolvedPolicy{Linters: []string{"clang-tidy", "cppcheck"}}
	out, err = captureStdout(t, func() error { return auditTidyCoverage(t.Context(), manifest, withC, native) })
	if err == nil || !strings.Contains(out, "  - src/a.c: read by no clang-tidy lane") ||
		!strings.Contains(err.Error(), ".standards.yaml declares no clang_tidy lanes") {
		t.Fatalf("audit with clang-tidy and no lanes = %v\n%s", err, out)
	}
	withoutC := tidyCoverageRepo(t, map[string]string{"main.go": "package main\n"})
	out, err = captureStdout(t, func() error { return auditTidyCoverage(t.Context(), manifest, withoutC, native) })
	if err != nil || !strings.Contains(out, "[SKIP] clang-tidy translation-unit coverage not checked: the repository tracks no C, C++, CUDA, HIP or Objective-C++ translation unit") {
		t.Fatalf("audit over a repository without C/C++ = %v\n%s", err, out)
	}
	declared := &config.Manifest{Version: 1, ClangTidy: &config.ClangTidyPolicy{Lanes: []config.ClangTidyLane{{Name: "cpu", Files: "lanes/cpu.txt"}}}}
	writeTidyCoverageFile(t, withC, "lanes/cpu.txt", "src/a.c\n")
	out, err = captureStdout(t, func() error { return auditTidyCoverage(t.Context(), declared, withC, &config.ResolvedPolicy{}) })
	if err != nil || !strings.Contains(out, "[PASS] clang-tidy translation-unit coverage: all 1 tracked translation units read by a lane (cpu: 1)") {
		t.Fatalf("declared lanes must enable the gate without the linter = %v\n%s", err, out)
	}
}

// Boundary (review of #778): the audit, which the hooks run before any build, skips the gate
// with the lane named while a compile database lane's database is not written, where
// ci tidy-coverage fails closed; once the build writes it, the audit judges coverage and fails
// on a planted unit.
func TestAuditTidyCoverage_Boundary_UnbuiltCompileDatabaseSkips(t *testing.T) {
	root := tidyCoverageRepo(t, map[string]string{
		"src/a.c": "int a;\n", "src/planted.cpp": "int p;\n", ".standards.yaml": tidyCoverageManifest(""),
	})
	manifest, err := config.LoadManifest(filepath.Join(root, config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	native := &config.ResolvedPolicy{Linters: []string{"clang-tidy"}}
	out, err := captureStdout(t, func() error { return auditTidyCoverage(t.Context(), manifest, root, native) })
	if err != nil || !strings.Contains(out, "[SKIP] clang-tidy translation-unit coverage not checked: no compile database written in this checkout "+
		"for lane cpu (build/compile_commands.json); praetorctl ci tidy-coverage judges coverage where a build writes it.") {
		t.Fatalf("audit before a build = %v\n%s", err, out)
	}
	_, err = captureStdout(t, func() error { return runCITidyCoverage(t.Context(), []string{"--dir=" + root}) })
	if err == nil || !strings.Contains(err.Error(), "compile database build/compile_commands.json cannot be read") {
		t.Fatalf("ci tidy-coverage before a build must fail closed, got %v", err)
	}
	writeTidyCoverageDatabase(t, root, "src/a.c")
	out, err = captureStdout(t, func() error { return auditTidyCoverage(t.Context(), manifest, root, native) })
	if err == nil || !strings.Contains(out, "  - src/planted.cpp: read by no clang-tidy lane") {
		t.Fatalf("audit after a build must judge coverage = %v\n%s", err, out)
	}
}
