// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tidycoverage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// today is the fixed day every test judges expiry against.
var today = time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)

// cpuLane is a lane reading the compile database build/compile_commands.json.
var cpuLane = config.ClangTidyLane{Name: "cpu", CompileDatabase: "build/compile_commands.json"}

// fixtureRepo makes a git repository holding files (path to content) with every file staged,
// so git tracks it, and returns its root.
func fixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	for rel, content := range files {
		writeFile(t, root, rel, content)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "-A"})
	return root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeCompileDatabase writes build/compile_commands.json, untracked like a build's output,
// with one entry per unit: absolute, as CMake and Meson write them.
func writeCompileDatabase(t *testing.T, root string, units ...string) {
	t.Helper()
	entries := make([]compileCommand, 0, len(units))
	for _, unit := range units {
		entries = append(entries, compileCommand{Directory: filepath.Join(root, "build"), File: filepath.Join(root, filepath.FromSlash(unit))})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "build/compile_commands.json", string(data))
}

func check(t *testing.T, root string, lanes []config.ClangTidyLane, exceptions ...config.Exception) Report {
	t.Helper()
	var policy *config.ClangTidyPolicy
	if lanes != nil {
		policy = &config.ClangTidyPolicy{Lanes: lanes}
	}
	report, err := Check(t.Context(), Options{Root: root, Policy: policy, Exceptions: exceptions, Today: today})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return report
}

func exception(target, expires string) config.Exception {
	entry := config.Exception{Rule: Rule, Reason: "built only on a platform no lane configures", Expires: expires}
	if strings.ContainsAny(target, "*?[") {
		entry.Glob = target
	} else {
		entry.Path = target
	}
	return entry
}

func findingsText(report Report) string {
	return strings.Join(report.Findings, "\n")
}

// Positive: every tracked unit read by a lane passes; a compile database lane and a file list
// lane add up, headers and other files are not units, and entries for files outside the
// repository are left out.
func TestCheck_Positive_EveryUnitReadByALane(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"src/a.c": "int a;\n", "src/b.cpp": "int b;\n", "src/a.h": "int h;\n", "README.md": "# r\n",
		"kernels/k.cu": "int k;\n", "lanes/gpu.txt": "# gpu lane\n\nkernels/k.cu\n./src/b.cpp\n",
	})
	writeCompileDatabase(t, root, "src/a.c", "src/b.cpp", "../outside/dep.c")
	report := check(t, root, []config.ClangTidyLane{cpuLane, {Name: "gpu", Files: "lanes/gpu.txt"}})
	if !report.Passed() || report.Skipped != "" || report.Units != 3 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Lanes) != 2 || report.Lanes[0].Units != 2 || report.Lanes[1].Units != 2 {
		t.Fatalf("lanes = %+v", report.Lanes)
	}
}

// Positive: a relative entry resolves against its directory, and a unit no lane reads passes
// while a live path or glob exception names it.
func TestCheck_Positive_RelativeEntriesAndLiveExceptions(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"src/a.c": "int a;\n", "src/win32/getopt.c": "int g;\n", "fixtures/rule/x.c": "int x;\n",
	})
	data := `[{"directory": ` + jsonString(t, filepath.Join(root, "build")) + `, "file": "../src/a.c", "command": "cc -c ../src/a.c"}]`
	writeFile(t, root, "build/compile_commands.json", data)
	report := check(t, root, []config.ClangTidyLane{cpuLane},
		exception("src/win32/getopt.c", "2026-12-31"), exception("fixtures/**/*.c", "2026-10-06"))
	if !report.Passed() {
		t.Fatalf("findings:\n%s", findingsText(report))
	}
	if strings.Join(report.Excepted, ",") != "fixtures/rule/x.c,src/win32/getopt.c" {
		t.Fatalf("excepted = %v", report.Excepted)
	}
}

// Negative (planted defect): a tracked unit outside every lane fails and is named, and so is
// every unit when no lane is declared at all.
func TestCheck_Negative_PlantedUnitOutsideEveryLane(t *testing.T) {
	root := fixtureRepo(t, map[string]string{"src/a.c": "int a;\n", "src/planted.cc": "int p;\n"})
	writeCompileDatabase(t, root, "src/a.c")
	report := check(t, root, []config.ClangTidyLane{cpuLane})
	if report.Passed() || findingsText(report) != "src/planted.cc: read by no clang-tidy lane and named by no exceptions entry" {
		t.Fatalf("findings:\n%s", findingsText(report))
	}
	undeclared := check(t, root, nil)
	if len(undeclared.Findings) != 2 || !strings.Contains(findingsText(undeclared), "src/a.c: read by no clang-tidy lane") {
		t.Fatalf("with no lane declared, every unit must fail:\n%s", findingsText(undeclared))
	}
}

// Negative: an expired exception excuses nothing, and an entry that excuses no unread unit is
// stale and fails, whether its file is read by a lane or not tracked at all.
func TestCheck_Negative_ExpiredAndStaleExceptions(t *testing.T) {
	root := fixtureRepo(t, map[string]string{"src/a.c": "int a;\n", "src/win32/getopt.c": "int g;\n"})
	writeCompileDatabase(t, root, "src/a.c")
	report := check(t, root, []config.ClangTidyLane{cpuLane},
		exception("src/win32/getopt.c", "2026-10-05"), exception("src/a.c", "2026-12-31"), exception("src/gone.c", "2026-12-31"))
	want := []string{
		"src/win32/getopt.c: read by no clang-tidy lane; its exception expired on 2026-10-05",
		"exceptions entry src/a.c (clang-tidy-coverage): excuses no translation unit that every lane leaves unread; remove the entry",
		"exceptions entry src/gone.c (clang-tidy-coverage): excuses no translation unit that every lane leaves unread; remove the entry",
	}
	if findingsText(report) != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", findingsText(report), strings.Join(want, "\n"))
	}
}

// Negative: a lane whose input is missing, malformed or empty fails closed with the lane named,
// instead of counting as a lane that reads nothing; so does an invalid declaration.
func TestCheck_Negative_UnreadableLaneFailsClosed(t *testing.T) {
	root := fixtureRepo(t, map[string]string{"src/a.c": "int a;\n", "lanes/bad.txt": "../escape.c\n", "lanes/empty.txt": "# none\n"})
	cases := map[string]struct {
		lane   config.ClangTidyLane
		setup  string
		reason string
	}{
		"missing database":   {cpuLane, "", "clang-tidy lane cpu: compile database build/compile_commands.json cannot be read"},
		"malformed database": {cpuLane, "{", "is not a JSON compilation database"},
		"empty database":     {cpuLane, "[]", "lists 0 entries"},
		"entry without file": {cpuLane, `[{"directory": "/"}]`, "entry 0 names no file"},
		"missing file list":  {config.ClangTidyLane{Name: "gpu", Files: "lanes/gpu.txt"}, "", "clang-tidy lane gpu: file list lanes/gpu.txt cannot be read"},
		"escaping file list": {config.ClangTidyLane{Name: "gpu", Files: "lanes/bad.txt"}, "", `line 1: "../escape.c" is not a repository-relative path`},
		"empty file list":    {config.ClangTidyLane{Name: "gpu", Files: "lanes/empty.txt"}, "", "names no file"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.RemoveAll(filepath.Join(root, "build")); err != nil {
				t.Fatal(err)
			}
			if tc.setup != "" {
				writeFile(t, root, "build/compile_commands.json", tc.setup)
			}
			_, err := Check(t.Context(), Options{Root: root, Policy: &config.ClangTidyPolicy{Lanes: []config.ClangTidyLane{tc.lane}}, Today: today})
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("Check = %v, want an error containing %q", err, tc.reason)
			}
		})
	}
	invalid := []config.Exception{exception("src/a.c", "2027-06-30")}
	if _, err := Check(t.Context(), Options{Root: root, Exceptions: invalid, Today: today}); err == nil ||
		!strings.Contains(err.Error(), "more than 90 days after today") {
		t.Fatalf("an exception expiring too far ahead must be refused, got %v", err)
	}
	if _, err := Check(t.Context(), Options{Root: t.TempDir(), Today: today}); err == nil {
		t.Fatal("a root outside any git repository must fail closed")
	}
}

// Boundary: a repository tracking no translation unit skips with the reason, lanes and
// exceptions unread; an untracked unit does not count; an upper-case suffix does; and an
// exception holds on its expires day.
func TestCheck_Boundary_SkipAndEdges(t *testing.T) {
	root := fixtureRepo(t, map[string]string{"main.go": "package main\n", "include/a.h": "int h;\n"})
	writeFile(t, root, "scratch/untracked.c", "int u;\n")
	report := check(t, root, []config.ClangTidyLane{cpuLane}, exception("src/a.c", "2026-12-31"))
	if !report.Passed() || !strings.Contains(report.Skipped, "tracks no C, C++, CUDA, HIP or Objective-C++ translation unit") {
		t.Fatalf("report = %+v", report)
	}
	upper := fixtureRepo(t, map[string]string{"src/Engine.CPP": "int e;\n", "src/view.mm": "int v;\n", "src/k.hip": "int k;\n"})
	units, err := trackedUnits(t.Context(), upper)
	if err != nil || strings.Join(units, ",") != "src/Engine.CPP,src/k.hip,src/view.mm" {
		t.Fatalf("trackedUnits = %v, %v", units, err)
	}
	onExpiryDay := check(t, upper, nil, exception("src/**", "2026-10-06"))
	if !onExpiryDay.Passed() || len(onExpiryDay.Excepted) != 3 {
		t.Fatalf("an exception must hold on its expires day: %+v", onExpiryDay)
	}
	dayAfter, err := Check(t.Context(), Options{Root: upper, Exceptions: []config.Exception{exception("src/**", "2026-10-06")},
		Today: today.AddDate(0, 0, 1)})
	if err != nil || dayAfter.Passed() || len(dayAfter.Findings) != 3 {
		t.Fatalf("an exception must stop holding the day after its expires day: %+v, %v", dayAfter, err)
	}
}

// Boundary (review of #778): under SkipUnbuilt, as the audit runs it in hooks before any
// build, a compile database that does not exist skips the run with every such lane named,
// even over a unit no lane reads, while the other lanes are still read and fail closed. The
// skip covers only absence: a written database is judged, a malformed one fails, and a files
// list, which is tracked rather than built, fails when missing. Without SkipUnbuilt, as
// ci tidy-coverage runs it, the absent database fails.
func TestCheck_Boundary_SkipUnbuiltCompileDatabase(t *testing.T) {
	root := fixtureRepo(t, map[string]string{"src/a.c": "int a;\n", "src/planted.cc": "int p;\n", "lanes/gpu.txt": "src/a.c\n"})
	gpuLane := config.ClangTidyLane{Name: "gpu", Files: "lanes/gpu.txt"}
	hipLane := config.ClangTidyLane{Name: "hip", CompileDatabase: "build-hip/compile_commands.json"}
	run := func(skipUnbuilt bool, lanes ...config.ClangTidyLane) (Report, error) {
		return Check(t.Context(), Options{Root: root, Policy: &config.ClangTidyPolicy{Lanes: lanes}, Today: today, SkipUnbuilt: skipUnbuilt})
	}
	report, err := run(true, cpuLane, gpuLane, hipLane)
	want := "no compile database written in this checkout for lane cpu (build/compile_commands.json), hip (build-hip/compile_commands.json); " +
		"praetorctl ci tidy-coverage judges coverage where a build writes it"
	if err != nil || report.Skipped != want || !report.Passed() || report.Units != 0 {
		t.Fatalf("unbuilt lanes must skip, naming each: %+v, %v", report, err)
	}
	if _, err := run(false, cpuLane, gpuLane); err == nil || !strings.Contains(err.Error(), "clang-tidy lane cpu: compile database build/compile_commands.json cannot be read") {
		t.Fatalf("without SkipUnbuilt an absent compile database must fail closed, got %v", err)
	}
	missingList := config.ClangTidyLane{Name: "metal", Files: "lanes/metal.txt"}
	if _, err := run(true, cpuLane, missingList); err == nil || !strings.Contains(err.Error(), "clang-tidy lane metal: file list lanes/metal.txt cannot be read") {
		t.Fatalf("a missing files list must fail closed beside an unbuilt lane, got %v", err)
	}
	writeFile(t, root, "build/compile_commands.json", "{")
	if _, err := run(true, cpuLane); err == nil || !strings.Contains(err.Error(), "is not a JSON compilation database") {
		t.Fatalf("a written but malformed compile database must fail closed, got %v", err)
	}
	writeCompileDatabase(t, root, "src/a.c")
	report, err = run(true, cpuLane, gpuLane)
	if err != nil || report.Skipped != "" || findingsText(report) != "src/planted.cc: read by no clang-tidy lane and named by no exceptions entry" {
		t.Fatalf("a written compile database must be judged: %+v, %v", report, err)
	}
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
