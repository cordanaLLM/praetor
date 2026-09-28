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
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestCSourceObservationDecidesC states the C rule (#549). Positive: a `.c` source is C, and so
// is a header with no other C-family source (a header-only C library). Negative: a `.h` beside
// a C++, Objective-C or CUDA source is theirs, GCC's capital `.C` is C++, and a repository with no
// C-family file is not C. Boundary: sources under a directory the HISS audit ignores count for
// nothing, so a vendored C++ tree does not claim the repository's own header either.
func TestCSourceObservationDecidesC(t *testing.T) {
	cases := map[string]struct {
		files []string
		want  bool
	}{
		"c source":                  {[]string{"bpf/probe.c"}, true},
		"header-only c":             {[]string{"include/widget.h"}, true},
		"c beside c++":              {[]string{"src/a.c", "src/b.cpp", "src/b.h"}, true},
		"c upper-case extension":    {[]string{"src/LEGACY.CPP", "src/boot.c"}, true},
		"header beside c++":         {[]string{"src/widget.cpp", "include/widget.h"}, false},
		"header beside objective-c": {[]string{"src/view.m", "src/view.h"}, false},
		"header beside cuda":        {[]string{"kernels/sum.cu", "kernels/sum.h"}, false},
		"gcc capital C is c++":      {[]string{"src/widget.C", "src/widget.h"}, false},
		"no c-family source":        {[]string{"main.go", "README.md", "Makefile"}, false},
		"nothing":                   {nil, false},
		"third_party c":             {[]string{"third_party/zlib/inflate.c"}, false},
		"testdata c":                {[]string{"internal/scan/testdata/goto.c"}, false},
		"ignored c++ claims no .h":  {[]string{"include/widget.h", "third_party/gtest/gtest.cc"}, true},
	}
	for name, tc := range cases {
		var observed cSourceObservation
		for _, rel := range tc.files {
			observed.observe(rel)
		}
		if got := observed.carriesC(); got != tc.want {
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
	cargoToml   = "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n"
	cSource     = "int probe(void) { return 0; }\n"
)

// TestRepositoryHISSFactsDetectsCSources reads C from sources through the verification walk, as
// adoption does (#549). Positive: a Makefile compiling bpf/*.c beside a Cargo crate is Rust + C,
// and a Makefile alone compiling C is C. Negative: the same shape without C sources is Rust only,
// and a header beside C++ sources only is not C. Boundary: C under vendor (skipped by the walk) or
// third_party and testdata (ignored by the audit's scan) is not the repository's own.
func TestRepositoryHISSFactsDetectsCSources(t *testing.T) {
	rustC := hisscatalog.LanguageRust | hisscatalog.LanguageC
	cases := map[string]struct {
		files map[string]string
		want  hisscatalog.Language
	}{
		"makefile bpf beside cargo": {map[string]string{"Makefile": bpfMakefile, "Cargo.toml": cargoToml, "bpf/probe.c": cSource, "bpf/probe.h": "#pragma once\n"}, rustC},
		"makefile bpf alone":        {map[string]string{"Makefile": bpfMakefile, "bpf/probe.c": cSource}, hisscatalog.LanguageC},
		"cargo without c":           {map[string]string{"Makefile": bpfMakefile, "Cargo.toml": cargoToml}, hisscatalog.LanguageRust},
		"header beside c++ only":    {map[string]string{"Cargo.toml": cargoToml, "src/widget.cpp": "int w;\n", "include/widget.h": "int w;\n"}, hisscatalog.LanguageRust},
		"vendored c":                {map[string]string{"Cargo.toml": cargoToml, "vendor/lib/lib.c": cSource}, hisscatalog.LanguageRust},
		"third_party c":             {map[string]string{"Cargo.toml": cargoToml, "third_party/lib/lib.c": cSource}, hisscatalog.LanguageRust},
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

// TestAdoptedHarnessRendersCClausesFromSources adopts a Rust crate whose Makefile compiles eBPF
// objects from bpf/*.c (#549). Positive: the harness states HISS-01's zero-goto and HISS-08's
// banned-libc clauses for C beside the Rust clauses. Negative: the same crate with a header beside
// C++ sources only states neither.
func TestAdoptedHarnessRendersCClausesFromSources(t *testing.T) {
	cClauses := []string{"HISS-01: recursion prohibited; call graph = DAG; C/C++: zero `goto`", "C/C++: zero banned libc (`gets` / `strcpy` / `sprintf`)"}
	bpf := directiveCells(t, adoptedHarness(t, "native-gpu-systems", map[string]string{
		"Makefile": bpfMakefile, "Cargo.toml": cargoToml, "bpf/probe.c": cSource,
	}))
	for _, want := range append(cClauses, "Rust: zero `.unwrap()` / `.expect()` outside tests") {
		if !strings.Contains(bpf, want) {
			t.Errorf("bpf crate harness lacks %q:\n%s", want, bpf)
		}
	}
	cpp := directiveCells(t, adoptedHarness(t, "native-gpu-systems", map[string]string{
		"Cargo.toml": cargoToml, "src/widget.cpp": "int w;\n", "include/widget.h": "int w;\n",
	}))
	for _, word := range []string{"`goto`", "libc", "C/C++:"} {
		if strings.Contains(cpp, word) {
			t.Errorf("C++ crate harness carries %q:\n%s", word, cpp)
		}
	}
}
