// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestCSourceObservationDecidesC states the C/C++ rule (#549): hisscatalog.LanguageC is C and
// C++, and every source the audit's native scan reads selects it. Positive: a `.c`, a C++, CUDA
// or HIP source, in any case (GCC's capital `.C` included, which the scan folds to `.c`), a header
// beside C++, and a header with no other source (a header-only library) are C/C++. Negative: a
// `.h` beside an Objective-C or Objective-C++ source is theirs, Objective-C alone is not C/C++,
// and neither is a repository with no such file. Boundary: C-family spellings the scan has no
// dispatch for (`.hh`, `.hxx`, `.cuh`) count for nothing, a `.c` beside Objective-C still counts,
// and sources under a directory the audit ignores count for nothing, so a vendored Objective-C
// tree does not claim the repository's own header either.
func TestCSourceObservationDecidesC(t *testing.T) {
	cases := map[string]struct {
		files []string
		want  bool
	}{
		"c source":                    {[]string{"bpf/probe.c"}, true},
		"header-only c":               {[]string{"include/widget.h"}, true},
		"c++ source":                  {[]string{"src/widget.cpp"}, true},
		"header beside c++":           {[]string{"src/widget.cc", "include/widget.h"}, true},
		"cuda":                        {[]string{"kernels/sum.cu", "kernels/sum.h"}, true},
		"hip":                         {[]string{"kernels/sum.hip"}, true},
		"c++ header":                  {[]string{"include/widget.hpp"}, true},
		"upper-case extension":        {[]string{"src/LEGACY.CPP"}, true},
		"gcc capital C":               {[]string{"src/widget.C", "src/widget.h"}, true},
		"header beside objective-c":   {[]string{"src/view.m", "src/view.h"}, false},
		"header beside objective-c++": {[]string{"src/view.mm", "src/view.h"}, false},
		"objective-c alone":           {[]string{"src/view.m"}, false},
		"no c-family source":          {[]string{"main.go", "README.md", "Makefile"}, false},
		"nothing":                     {nil, false},
		"unscanned spellings":         {[]string{"src/b.hxx", "kernels/c.cuh"}, false},
		"scanned .hh header":          {[]string{"src/a.hh"}, true},
		"c beside objective-c":        {[]string{"src/view.m", "src/view.h", "src/util.c"}, true},
		"third_party c":               {[]string{"third_party/zlib/inflate.c"}, false},
		"testdata c++":                {[]string{"internal/scan/testdata/goto.cpp"}, false},
		"ignored objc claims no .h":   {[]string{"include/widget.h", "third_party/kit/view.m"}, true},
	}
	for name, tc := range cases {
		var observed cSourceObservation
		for _, rel := range tc.files {
			observed.observe(rel)
		}
		if got := observed.carriesC(nil); got != tc.want {
			t.Errorf("%s: carriesC(%q) = %v, want %v", name, tc.files, got, tc.want)
		}
	}
}

// TestPlanLanguagesReadsSourceLanguages: Positive: a C source language joins the runtimes'
// languages. Negative: a plan without one keeps exactly its runtimes' languages. Boundary: C
// sources alone make the set known (C), no longer the unknown set.
func TestPlanLanguagesReadsSourceLanguages(t *testing.T) {
	cases := []struct {
		plan *VerificationPlan
		want hisscatalog.Language
	}{
		{&VerificationPlan{Runtimes: []string{"cargo", "python"}, SourceLanguages: []string{sourceLanguageC}}, hisscatalog.LanguageRust | hisscatalog.LanguagePython | hisscatalog.LanguageC},
		{&VerificationPlan{Runtimes: []string{"cargo"}}, hisscatalog.LanguageRust},
		{&VerificationPlan{Runtimes: []string{}, SourceLanguages: []string{sourceLanguageC}}, hisscatalog.LanguageC},
	}
	for _, tc := range cases {
		if got := planLanguages(tc.plan); got != tc.want {
			t.Errorf("planLanguages(%+v) = %b, want %b", tc.plan, got, tc.want)
		}
	}
}

// writeRepo writes files, slash-separated paths mapped to content, under a fresh directory.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	return root
}

const (
	bpfMakefile = "all:\n\tclang -O2 -target bpf -c bpf/probe.c -o bpf/probe.o\n"
	cppMakefile = "all:\n\tclang++ -O2 -Iinclude -c src/widget.cpp -o widget.o\n"
	cargoToml   = "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n"
	cSource     = "int probe(void) { return 0; }\n"
)

// TestRepositoryHISSFactsDetectsCSources reads C/C++ from sources through the verification walk,
// as adoption does (#549). Positive: a Makefile compiling bpf/*.c beside a Cargo crate is Rust +
// C/C++, a Makefile alone compiling C is C/C++, and so is a Makefile compiling C++ beside its
// header. Negative: the same shape without sources is Rust only, and a header beside Objective-C
// sources only is not C/C++. Boundary: sources under vendor (skipped by the walk) or third_party
// and testdata (ignored by the audit's scan) are not the repository's own.
func TestRepositoryHISSFactsDetectsCSources(t *testing.T) {
	rustC := hisscatalog.LanguageRust | hisscatalog.LanguageC
	cases := map[string]struct {
		files map[string]string
		want  hisscatalog.Language
	}{
		"makefile bpf beside cargo": {map[string]string{"Makefile": bpfMakefile, "Cargo.toml": cargoToml, "bpf/probe.c": cSource, "bpf/probe.h": "#pragma once\n"}, rustC},
		"makefile bpf alone":        {map[string]string{"Makefile": bpfMakefile, "bpf/probe.c": cSource}, hisscatalog.LanguageC},
		"makefile c++ beside cargo": {map[string]string{"Makefile": cppMakefile, "Cargo.toml": cargoToml, "src/widget.cpp": "int w;\n", "include/widget.h": "int w;\n"}, rustC},
		"cargo without c":           {map[string]string{"Makefile": bpfMakefile, "Cargo.toml": cargoToml}, hisscatalog.LanguageRust},
		"header beside objective-c": {map[string]string{"Cargo.toml": cargoToml, "src/view.m": "int v;\n", "include/view.h": "int v;\n"}, hisscatalog.LanguageRust},
		"vendored c":                {map[string]string{"Cargo.toml": cargoToml, "vendor/lib/lib.c": cSource}, hisscatalog.LanguageRust},
		"third_party c++":           {map[string]string{"Cargo.toml": cargoToml, "third_party/lib/lib.cc": cSource}, hisscatalog.LanguageRust},
		"testdata c":                {map[string]string{"Cargo.toml": cargoToml, "tests/testdata/goto.c": cSource}, hisscatalog.LanguageRust},
	}
	for name, tc := range cases {
		want := hisscatalog.Facts{Languages: tc.want, CeilingFuncLOC: config.AuditMaxFuncLOC}
		got, warnings, err := RepositoryHISSFacts(t.Context(), writeRepo(t, tc.files), nil)
		if err != nil || got != want || len(warnings) != 0 {
			t.Errorf("%s: RepositoryHISSFacts = %+v, %q, %v; want %+v", name, got, warnings, err, want)
		}
	}
}

// TestCSourceDetectionStaysInsideTheWalkBounds: the source observation adds no walk of its own.
// Boundary: a C source counted at exactly the entry bound, or at exactly the depth bound, is
// detected. Negative: one entry or one level past the bound stops the walk with the error naming
// the flag that raises it, never a language set read from part of the tree.
func TestCSourceDetectionStaysInsideTheWalkBounds(t *testing.T) {
	// Makefile, bpf and bpf/probe.c are three entries.
	atBound := writeRepo(t, map[string]string{"Makefile": bpfMakefile, "bpf/probe.c": cSource})
	limits := DefaultVerificationLimits()
	limits.MaxEntries = 3
	plan, err := ObserveVerificationPlanWithLimits(t.Context(), atBound, &limits)
	if err != nil || !slices.Equal(plan.SourceLanguages, []string{sourceLanguageC}) {
		t.Fatalf("C at the entry bound: %+v, %v; want source language c", plan, err)
	}
	mustWrite(t, filepath.Join(atBound, "bpf", "second.c"), cSource)
	if _, err := ObserveVerificationPlanWithLimits(t.Context(), atBound, &limits); err == nil || !strings.Contains(err.Error(), "--"+VerificationEntriesFlag) {
		t.Fatalf("C past the entry bound: %v; want the entries flag named", err)
	}
	deep := writeRepo(t, map[string]string{"Cargo.toml": cargoToml, "a/b/probe.c": cSource})
	limits = DefaultVerificationLimits()
	limits.MaxDepth = 2
	plan, err = ObserveVerificationPlanWithLimits(t.Context(), deep, &limits)
	if err != nil || !slices.Equal(plan.SourceLanguages, []string{sourceLanguageC}) {
		t.Fatalf("C at the depth bound: %+v, %v; want source language c", plan, err)
	}
	mustWrite(t, filepath.Join(deep, "a", "b", "c", "probe.c"), cSource)
	if _, err := ObserveVerificationPlanWithLimits(t.Context(), deep, &limits); err == nil || !strings.Contains(err.Error(), "--"+VerificationDepthFlag) {
		t.Fatalf("C past the depth bound: %v; want the depth flag named", err)
	}
}

// TestAdoptedHarnessRendersCClausesFromSources adopts Rust crates whose Makefile compiles native
// sources (#549). Positive: eBPF objects from bpf/*.c, and a C++ library from src/widget.cpp with
// its header, each state HISS-01's zero-goto and HISS-08's banned-libc clauses for C/C++ beside the
// Rust clauses. Negative: the same crate with a header beside Objective-C sources only states
// neither.
func TestAdoptedHarnessRendersCClausesFromSources(t *testing.T) {
	cClauses := []string{"HISS-01: recursion prohibited; call graph = DAG; C/C++: zero `goto`", "C/C++: zero banned libc (`gets` / `strcpy` / `sprintf`)"}
	for name, files := range map[string]map[string]string{
		"bpf": {"Makefile": bpfMakefile, "Cargo.toml": cargoToml, "bpf/probe.c": cSource},
		"c++": {"Makefile": cppMakefile, "Cargo.toml": cargoToml, "src/widget.cpp": "int w;\n", "include/widget.h": "int w;\n"},
	} {
		cells := directiveCells(t, adoptedHarness(t, "native-gpu-systems", files))
		for _, want := range append(cClauses, "Rust: zero `.unwrap()` / `.expect()` outside tests") {
			if !strings.Contains(cells, want) {
				t.Errorf("%s crate harness lacks %q:\n%s", name, want, cells)
			}
		}
	}
	objC := directiveCells(t, adoptedHarness(t, "native-gpu-systems", map[string]string{
		"Cargo.toml": cargoToml, "src/view.m": "int v;\n", "include/view.h": "int v;\n",
	}))
	for _, word := range []string{"`goto`", "libc", "C/C++:"} {
		if strings.Contains(objC, word) {
			t.Errorf("Objective-C crate harness carries %q:\n%s", word, objC)
		}
	}
}

// TestCSourceDetectionFollowsTheAuditScan: the C/C++ clauses render exactly where the audit's
// scan enforces them, for a Makefile-built repository with no meson or CMake marker. Positive:
// for every native extension the scan reports a banned strcpy (HISS-08) and a goto (HISS-01) in a
// Makefile-built source, and the facts carry C/C++. Negative: the same source as Objective-C, or
// as a CUDA header the scan has no dispatch for, is neither scanned nor C/C++. Boundary: a C++
// header alone (`.hpp`) is scanned and selects the clauses.
func TestCSourceDetectionFollowsTheAuditScan(t *testing.T) {
	const native = "void copy(char *d, const char *s) {\nretry:\n\tstrcpy(d, s);\n\tgoto retry;\n}\n"
	cases := map[string]bool{
		".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".cu": true, ".hip": true, ".h": true, ".hpp": true,
		".m": false, ".cuh": false,
	}
	for ext, want := range cases {
		root := writeRepo(t, map[string]string{"Makefile": cppMakefile, "src/widget" + ext: native})
		rep, err := hiss.Scan(t.Context(), root, hiss.ScanOptions{})
		if err != nil {
			t.Fatalf("scan %s: %v", ext, err)
		}
		enforced := rep.Breakdown["HISS-01"] > 0 && rep.Breakdown["HISS-08"] > 0
		facts, _, err := RepositoryHISSFacts(t.Context(), root, nil)
		if err != nil {
			t.Fatalf("facts %s: %v", ext, err)
		}
		if detected := facts.Languages&hisscatalog.LanguageC != 0; enforced != want || detected != want {
			t.Errorf("%s: audit enforces = %v, C/C++ detected = %v; want both %v (breakdown %v)", ext, enforced, detected, want, rep.Breakdown)
		}
	}
}

// TestRepositoryHISSFactsReadsOnlyGitVisibleCSources: in a work tree, C counts only from the files
// git reports as the repository's own, the set the audit's HISS scan reads
// (hiss.GitVisiblePaths). Positive: a tracked `.c`, and an untracked one git does not ignore, is
// C. Negative: an in-place Cython `.c` a `*.c` pattern ignores, and C under ignored IDE build
// output, is not. Boundary: a force-added `.c` an ignore pattern matches is tracked and counts,
// and ignored C++ build output does not claim the repository's own header.
func TestRepositoryHISSFactsReadsOnlyGitVisibleCSources(t *testing.T) {
	const goMod = "module example.com/widget\n\ngo 1.27\n"
	goC := hisscatalog.LanguageGo | hisscatalog.LanguageC
	cases := map[string]struct {
		files map[string]string
		git   []string
		want  hisscatalog.Language
	}{
		"tracked c":             {map[string]string{"go.mod": goMod, "bpf/probe.c": cSource}, []string{"add", "-A"}, goC},
		"untracked visible c":   {map[string]string{"go.mod": goMod, "bpf/probe.c": cSource}, nil, goC},
		"ignored cython c":      {map[string]string{"pyproject.toml": "[project]\nname = \"fast\"\n", ".gitignore": "*.c\n", "pkg/fast.pyx": "", "pkg/fast.c": cSource}, nil, hisscatalog.LanguagePython},
		"ignored ide build c":   {map[string]string{"go.mod": goMod, ".gitignore": "/cmake-build-debug/\n", "cmake-build-debug/CMakeFiles/probe.c": cSource}, nil, hisscatalog.LanguageGo},
		"force-added ignored c": {map[string]string{"go.mod": goMod, ".gitignore": "*.c\n", "bpf/probe.c": cSource}, []string{"add", "-f", "bpf/probe.c"}, goC},
		"ignored c++ output":    {map[string]string{"go.mod": goMod, ".gitignore": "/out/\n", "include/widget.h": "int w;\n", "out/gen.cpp": "int w;\n"}, nil, goC},
	}
	for name, tc := range cases {
		root := writeRepo(t, tc.files)
		testsupport.InitGitRepoWithOrigin(t, root, "")
		if tc.git != nil {
			fixtureGit(t, root, tc.git...)
		}
		want := hisscatalog.Facts{Languages: tc.want, CeilingFuncLOC: config.AuditMaxFuncLOC}
		got, warnings, err := RepositoryHISSFacts(t.Context(), root, nil)
		if err != nil || got != want || len(warnings) != 0 {
			t.Errorf("%s: RepositoryHISSFacts = %+v, %q, %v; want %+v", name, got, warnings, err, want)
		}
	}
}
