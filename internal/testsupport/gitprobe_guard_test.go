// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestGitProbeGuard_Repository_ScansTrackedTestFiles scans tracked *_test.go files
// for mutating util.RunGitProbe calls (order engine-agenthook-windows-flake-20261028).
func TestGitProbeGuard_Repository_ScansTrackedTestFiles(t *testing.T) {
	findings, err := ScanTrackedTestGitProbes(t.Context(), "")
	if err != nil {
		t.Fatalf("scan tracked test git probes: %v", err)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("%s", f.String())
		}
		t.Fatalf("found %d mutating util.RunGitProbe calls in test fixtures", len(findings))
	}
}

// TestGitProbeGuard_Planted tests detection of planted mutating and read-only probe calls.
func TestGitProbeGuard_Planted(t *testing.T) {
	dir := t.TempDir()

	// Positive planted case: util.RunGitProbe with "init"
	initSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"init\", \"-q\")\n}\n")
	initFile := filepath.Join(dir, "init_test.go")
	if err := os.WriteFile(initFile, initSrc, 0o600); err != nil {
		t.Fatal(err)
	}
	initBytes, err := util.ReadFileLimited(initFile, maxTestFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := ScanProbeMutations(initFile, initBytes)
	if err != nil {
		t.Fatalf("scan init planted: %v", err)
	}
	if len(findings) != 1 || findings[0].Subcommand != "init" {
		t.Fatalf("planted init: want 1 finding with init, got %v", findings)
	}

	// Negative planted case: util.RunGitProbe with "rev-parse" (read probe allowed)
	readSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"rev-parse\", \"HEAD\")\n}\n")
	readFile := filepath.Join(dir, "read_test.go")
	if err := os.WriteFile(readFile, readSrc, 0o600); err != nil {
		t.Fatal(err)
	}
	readBytes, err := util.ReadFileLimited(readFile, maxTestFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	readFindings, err := ScanProbeMutations(readFile, readBytes)
	if err != nil {
		t.Fatalf("scan read planted: %v", err)
	}
	if len(readFindings) != 0 {
		t.Fatalf("planted read probe: want 0 findings, got %v", readFindings)
	}
}

// TestGitProbeGuard_MutatingSubcommands covers commit, add, and config (HISS-15).
func TestGitProbeGuard_MutatingSubcommands(t *testing.T) {
	for _, subcmd := range []string{"commit", "add", "config"} {
		src := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"" + subcmd + "\")\n}\n")
		findings, err := ScanProbeMutations("sample_test.go", src)
		if err != nil {
			t.Fatalf("scan %s: %v", subcmd, err)
		}
		if len(findings) != 1 || findings[0].Subcommand != subcmd {
			t.Fatalf("subcommand %s: want 1 finding, got %v", subcmd, findings)
		}
		if !strings.Contains(findings[0].String(), "testsupport.RunFixtureGit") {
			t.Fatalf("finding string must name testsupport.RunFixtureGit: %s", findings[0].String())
		}
	}
}

// TestGitProbeGuard_Boundaries covers boundary conditions: empty, short args, no probe (HISS-15).
func TestGitProbeGuard_Boundaries(t *testing.T) {
	emptyFindings, err := ScanProbeMutations("empty_test.go", nil)
	if err != nil || len(emptyFindings) != 0 {
		t.Fatalf("empty source: err=%v findings=%v", err, emptyFindings)
	}

	shortSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096)\n}\n")
	shortFindings, err := ScanProbeMutations("short_test.go", shortSrc)
	if err != nil || len(shortFindings) != 0 {
		t.Fatalf("short args: err=%v findings=%v", err, shortFindings)
	}

	otherSrc := []byte("package sample_test\n\nfunc f() {\n\tprintln(\"hello\")\n}\n")
	otherFindings, err := ScanProbeMutations("other_test.go", otherSrc)
	if err != nil || len(otherFindings) != 0 {
		t.Fatalf("no probe: err=%v findings=%v", err, otherFindings)
	}
}
