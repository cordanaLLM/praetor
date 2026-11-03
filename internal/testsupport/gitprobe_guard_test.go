// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// writeProbeFixtureFile writes source code to a fixture file within a test directory.
func writeProbeFixtureFile(t *testing.T, dir, name string, src []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertSingleFinding verifies that scanning source produces exactly one finding with the expected subcommand.
func assertSingleFinding(t *testing.T, label, file string, src []byte, wantSubcmd string) {
	t.Helper()
	findings, err := ScanProbeMutations(file, src)
	if err != nil || len(findings) != 1 || findings[0].Subcommand != wantSubcmd {
		t.Fatalf("%s: want 1 finding %q, got err=%v findings=%v", label, wantSubcmd, err, findings)
	}
}

// assertNoFindings verifies that scanning source produces zero probe mutation findings.
func assertNoFindings(t *testing.T, label, file string, src []byte) {
	t.Helper()
	findings, err := ScanProbeMutations(file, src)
	if err != nil || len(findings) != 0 {
		t.Fatalf("%s: want 0 findings, got err=%v findings=%v", label, err, findings)
	}
}

// TestGitProbeGuard_Planted tests detection of planted mutating and read-only probe calls
// through both ScanProbeMutations and ScanTrackedTestGitProbes.
func TestGitProbeGuard_Planted(t *testing.T) {
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")

	initSrc := []byte("package sample_test\n\nimport \"github.com/cordanaLLM/praetor/internal/util\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"init\", \"-q\")\n}\n")
	readSrc := []byte("package sample_test\n\nimport \"github.com/cordanaLLM/praetor/internal/util\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"rev-parse\", \"HEAD\")\n}\n")
	initFile := writeProbeFixtureFile(t, dir, "init_test.go", initSrc)
	readFile := writeProbeFixtureFile(t, dir, "read_test.go", readSrc)
	writeProbeFixtureFile(t, dir, "untracked_test.go", initSrc)
	RunFixtureGit(t, dir, []string{"add", "--", "init_test.go", "read_test.go"})

	assertSingleFinding(t, "planted init", initFile, initSrc, "init")
	assertNoFindings(t, "planted read probe", readFile, readSrc)

	trackedFindings, err := ScanTrackedTestGitProbes(t.Context(), dir)
	if err != nil || len(trackedFindings) != 1 || trackedFindings[0].Subcommand != "init" || trackedFindings[0].File != "init_test.go" {
		t.Fatalf("tracked planted scan: err=%v findings=%v", err, trackedFindings)
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

// TestGitProbeGuard_AllowedReadOnlySubcommands verifies every allowed read-only subcommand passes.
func TestGitProbeGuard_AllowedReadOnlySubcommands(t *testing.T) {
	for _, subcmd := range readOnlyGitProbeSubcommands {
		src := []byte(fmt.Sprintf("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, %q)\n}\n", subcmd))
		findings, err := ScanProbeMutations("sample_test.go", src)
		if err != nil {
			t.Fatalf("scan allowed %s: %v", subcmd, err)
		}
		if len(findings) != 0 {
			t.Fatalf("allowed read-only subcommand %q must pass, got %v", subcmd, findings)
		}
	}
}

// TestGitProbeGuard_DisallowedSubcommandsRefused verifies that non-allow-listed subcommands are refused.
func TestGitProbeGuard_DisallowedSubcommandsRefused(t *testing.T) {
	for _, subcmd := range []string{"commit", "reset", "checkout", "push", "config --add", "unknown"} {
		src := []byte(fmt.Sprintf("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, %q)\n}\n", subcmd))
		findings, err := ScanProbeMutations("sample_test.go", src)
		if err != nil {
			t.Fatalf("scan disallowed %s: %v", subcmd, err)
		}
		if len(findings) != 1 || findings[0].Subcommand != subcmd {
			t.Fatalf("subcommand %s: want 1 finding with %q, got %v", subcmd, subcmd, findings)
		}
		if !strings.Contains(findings[0].String(), "testsupport.RunFixtureGit") {
			t.Fatalf("finding string must name testsupport.RunFixtureGit: %s", findings[0].String())
		}
		if !strings.Contains(findings[0].String(), subcmd) {
			t.Fatalf("finding string must name subcommand %q: %s", subcmd, findings[0].String())
		}
	}

	// config --add passed as separate arguments: "config", "--add"
	separateSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"config\", \"--add\")\n}\n")
	separateFindings, err := ScanProbeMutations("sample_test.go", separateSrc)
	if err != nil {
		t.Fatalf("scan separate config --add: %v", err)
	}
	if len(separateFindings) != 1 || separateFindings[0].Subcommand != "config --add" {
		t.Fatalf("separate config --add: want 1 finding with \"config --add\", got %v", separateFindings)
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

// probeCallSource constructs Go test source code containing a call to RunGitProbe with the given arguments.
func probeCallSource(pkgExpr string, args ...string) []byte {
	quoted := make([]string, 0, len(args))
	for i := 0; i < len(args) && i < maxCallArgs+8; i++ {
		quoted = append(quoted, fmt.Sprintf("%q", args[i]))
	}
	return []byte(fmt.Sprintf("package sample_test\n\nfunc f() {\n\t%s.RunGitProbe(ctx, dir, 4096, %s)\n}\n",
		pkgExpr, strings.Join(quoted, ", ")))
}

// TestGitProbeGuard_WriteInvocationsOfReadOnlySubcommands verifies write forms of hash-object,
// remote, and symbolic-ref are rejected while read-only forms pass.
func TestGitProbeGuard_WriteInvocationsOfReadOnlySubcommands(t *testing.T) {
	disallowed := []struct {
		args []string
		want string
	}{
		{[]string{"hash-object", "-w", "file.txt"}, "hash-object -w"},
		{[]string{"hash-object", "-t", "blob", "-w", "file.txt"}, "hash-object -w"},
		{[]string{"hash-object", "-wt", "blob", "file.txt"}, "hash-object -wt"},
		{[]string{"remote", "add", "origin", "https://example.invalid/repo.git"}, "remote add"},
		{[]string{"remote", "-v", "add", "origin", "https://example.invalid/repo.git"}, "remote add"},
		{[]string{"remote", "set-url", "origin", "https://example.invalid/repo.git"}, "remote set-url"},
		{[]string{"remote", "remove", "origin"}, "remote remove"},
		{[]string{"remote", "rename", "origin", "upstream"}, "remote rename"},
		{[]string{"symbolic-ref", "HEAD", "refs/heads/other"}, "symbolic-ref HEAD refs/heads/other"},
		{[]string{"symbolic-ref", "-d", "HEAD"}, "symbolic-ref -d"},
		{[]string{"symbolic-ref", "--delete", "HEAD"}, "symbolic-ref --delete"},
		{[]string{"symbolic-ref", "-m", "reason", "HEAD", "refs/heads/other"}, "symbolic-ref -m"},
	}
	for i := 0; i < len(disallowed) && i < maxCallArgs; i++ {
		tc := disallowed[i]
		findings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", tc.args...))
		if err != nil || len(findings) != 1 || findings[0].Subcommand != tc.want {
			t.Fatalf("write form %v: want 1 finding %q, got err=%v findings=%v", tc.args, tc.want, err, findings)
		}
	}

	allowed := [][]string{
		{"hash-object", "-t", "blob", "--stdin"},
		{"hash-object", "--path=a.go", "--", "a.go"},
		{"remote", "-v"},
		{"remote", "get-url", "--push", "origin"},
		{"remote", "show", "-n", "origin"},
		{"symbolic-ref", "--quiet", "--short", "HEAD"},
	}
	for i := 0; i < len(allowed) && i < maxCallArgs; i++ {
		findings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", allowed[i]...))
		if err != nil || len(findings) != 0 {
			t.Fatalf("read-only form %v: want 0 findings, got err=%v findings=%v", allowed[i], err, findings)
		}
	}
}

// TestGitProbeGuard_ImportAlias verifies aliased imports of internal/util are recognized.
func TestGitProbeGuard_ImportAlias(t *testing.T) {
	badSrc := []byte("package sample_test\n\nimport gitutil \"github.com/cordanaLLM/praetor/internal/util\"\n\nfunc f() {\n\tgitutil.RunGitProbe(ctx, dir, 4096, \"init\")\n}\n")
	findings, err := ScanProbeMutations("alias_test.go", badSrc)
	if err != nil || len(findings) != 1 || findings[0].Subcommand != "init" {
		t.Fatalf("aliased util.RunGitProbe(\"init\"): want 1 finding, got err=%v findings=%v", err, findings)
	}

	goodSrc := []byte("package sample_test\n\nimport gitutil \"github.com/cordanaLLM/praetor/internal/util\"\n\nfunc f() {\n\tgitutil.RunGitProbe(ctx, dir, 4096, \"rev-parse\", \"HEAD\")\n}\n")
	goodFindings, err := ScanProbeMutations("alias_test.go", goodSrc)
	if err != nil || len(goodFindings) != 0 {
		t.Fatalf("aliased util.RunGitProbe(\"rev-parse\"): want 0 findings, got err=%v findings=%v", err, goodFindings)
	}

	otherSrc := []byte("package sample_test\n\nimport other \"example.com/other\"\n\nfunc f() {\n\tother.RunGitProbe(ctx, dir, 4096, \"init\")\n}\n")
	otherFindings, err := ScanProbeMutations("other_test.go", otherSrc)
	if err != nil || len(otherFindings) != 0 {
		t.Fatalf("unrelated package RunGitProbe: want 0 findings, got err=%v findings=%v", err, otherFindings)
	}
}

// TestGitProbeGuard_GlobalOptions verifies operand-taking and flag Git global options before the subcommand.
func TestGitProbeGuard_GlobalOptions(t *testing.T) {
	disallowed := [][]string{
		{"--git-dir", "status", "init"},
		{"--git-dir=status", "init"},
		{"--work-tree", "status", "init"},
		{"--work-tree=status", "init"},
		{"-C", "status", "init"},
		{"-Cstatus", "init"},
		{"-c", "status=true", "init"},
		{"-cstatus=true", "init"},
		{"--namespace", "status", "init"},
		{"--config-env", "core.bare=status", "init"},
		{"--attr-source", "status", "init"},
		{"--super-prefix", "status", "init"},
	}
	for i := 0; i < len(disallowed) && i < maxCallArgs; i++ {
		findings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", disallowed[i]...))
		if err != nil || len(findings) != 1 || findings[0].Subcommand != "init" {
			t.Fatalf("global option %v: want 1 finding \"init\", got err=%v findings=%v", disallowed[i], err, findings)
		}
	}

	allowed := []string{"--git-dir", "status", "--work-tree=.", "-C", "sub", "-c", "core.bare=false", "--no-pager", "rev-parse", "HEAD"}
	findings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", allowed...))
	if err != nil || len(findings) != 0 {
		t.Fatalf("allowed global options: want 0 findings, got err=%v findings=%v", err, findings)
	}
}

// TestGitProbeGuard_ArgumentScanExhaustion verifies that exceeding maxCallArgs global options fails closed.
func TestGitProbeGuard_ArgumentScanExhaustion(t *testing.T) {
	exceeded := make([]string, 0, maxCallArgs+1)
	for i := 0; i < maxCallArgs; i++ {
		exceeded = append(exceeded, "--no-pager")
	}
	exceeded = append(exceeded, "init")
	findings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", exceeded...))
	if err != nil || len(findings) != 1 || findings[0].Subcommand != "<argument bound exceeded>" {
		t.Fatalf("64 global options before init: want 1 exhaustion finding, got err=%v findings=%v", err, findings)
	}

	withinBad := append(exceeded[:maxCallArgs-1:maxCallArgs-1], "init")
	withinFindings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", withinBad...))
	if err != nil || len(withinFindings) != 1 || withinFindings[0].Subcommand != "init" {
		t.Fatalf("63 global options before init: want 1 \"init\" finding, got err=%v findings=%v", err, withinFindings)
	}

	withinGood := append(exceeded[:maxCallArgs-1:maxCallArgs-1], "rev-parse")
	goodFindings, err := ScanProbeMutations("sample_test.go", probeCallSource("util", withinGood...))
	if err != nil || len(goodFindings) != 0 {
		t.Fatalf("63 global options before rev-parse: want 0 findings, got err=%v findings=%v", err, goodFindings)
	}
}

// TestGitProbeGuard_NonLiteralCommand verifies non-literal commands fail closed and constants resolve.
func TestGitProbeGuard_NonLiteralCommand(t *testing.T) {
	varSrc := []byte("package sample_test\n\nfunc f() {\n\tcmd := \"init\"\n\tutil.RunGitProbe(ctx, dir, 4096, cmd)\n}\n")
	assertSingleFinding(t, "variable subcommand", "var_test.go", varSrc, "<non-literal>")

	sliceSrc := []byte("package sample_test\n\nfunc f(args ...string) {\n\tutil.RunGitProbe(ctx, dir, 4096, args...)\n}\n")
	assertSingleFinding(t, "variadic subcommand", "slice_test.go", sliceSrc, "<non-literal>")

	constBadSrc := []byte("package sample_test\n\nconst sub = \"init\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n")
	assertSingleFinding(t, "const mutating subcommand", "const_bad_test.go", constBadSrc, "init")

	constGoodSrc := []byte("package sample_test\n\nconst sub = \"rev-parse\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub, \"HEAD\")\n}\n")
	assertNoFindings(t, "const read-only subcommand", "const_good_test.go", constGoodSrc)

	fnValSrc := []byte("package sample_test\n\nfunc f() {\n\tprobe := util.RunGitProbe\n\tprobe(ctx, dir, 4096, \"init\")\n}\n")
	assertSingleFinding(t, "function value assignment", "fnval_test.go", fnValSrc, "<non-literal>")

	aliasFnValSrc := []byte("package sample_test\n\nimport gitutil \"github.com/cordanaLLM/praetor/internal/util\"\n\nfunc f() {\n\tprobe := gitutil.RunGitProbe\n\tprobe(ctx, dir, 4096, \"init\")\n}\n")
	assertSingleFinding(t, "aliased function value", "alias_fnval_test.go", aliasFnValSrc, "<non-literal>")

	mixedSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"rev-parse\", \"HEAD\")\n\tprobe := util.RunGitProbe\n\tprobe(ctx, dir, 4096, \"init\")\n}\n")
	assertSingleFinding(t, "direct read-only plus function value", "mixed_test.go", mixedSrc, "<non-literal>")

	otherFnValSrc := []byte("package sample_test\n\nimport other \"example.com/other\"\n\nfunc f() {\n\tprobe := other.RunGitProbe\n\t_ = probe\n}\n")
	assertNoFindings(t, "unrelated package function value", "other_fnval_test.go", otherFnValSrc)
}

// TestGitProbeGuard_TrackedFileBound verifies ScanTrackedTestGitProbes returns an error
// when the repository exceeds maxTrackedTestFiles tracked test files.
func TestGitProbeGuard_TrackedFileBound(t *testing.T) {
	dir := t.TempDir()
	InitGitRepoWithOrigin(t, dir, "")
	for i := 0; i < maxTrackedTestFiles; i++ {
		name := filepath.Join(dir, fmt.Sprintf("f%04d_test.go", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	initSrc := []byte("package sample_test\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"init\")\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "zzz_test.go"), initSrc, 0o600); err != nil {
		t.Fatal(err)
	}
	RunFixtureGit(t, dir, []string{"add", "."})

	findings, err := ScanTrackedTestGitProbes(t.Context(), dir)
	if err == nil {
		t.Fatalf("exceeding maxTrackedTestFiles (%d): want error, got err=<nil> findings=%v", maxTrackedTestFiles, findings)
	}
}

// TestGitProbeGuard_ConstantDeclarations_Positive covers constant definitions that resolve
// to allowed read-only subcommands across grouped, multi-spec, inherited, chained, and local forms.
func TestGitProbeGuard_ConstantDeclarations_Positive(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "grouped const",
			src:  "package sample_test\n\nconst (\n\tsub = \"rev-parse\"\n)\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub, \"HEAD\")\n}\n",
		},
		{
			name: "multi-spec const",
			src:  "package sample_test\n\nconst a, sub = \"extra\", \"diff\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
		},
		{
			name: "inherited const value",
			src:  "package sample_test\n\nconst (\n\tfirst = \"cat-file\"\n\tsecond\n)\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, second, \"blob\", \"HEAD\")\n}\n",
		},
		{
			name: "chained const",
			src:  "package sample_test\n\nconst base = \"ls-files\"\nconst sub = base\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
		},
		{
			name: "local func const",
			src:  "package sample_test\n\nfunc f() {\n\tconst sub = \"status\"\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
		},
	}
	for i := 0; i < len(cases) && i < maxCallArgs; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertNoFindings(t, tc.name, "sample_test.go", []byte(tc.src))
		})
	}
}

// TestGitProbeGuard_ConstantDeclarations_Negative covers constant definitions that resolve
// to disallowed mutating subcommands across grouped, multi-spec, inherited, chained, and local forms.
func TestGitProbeGuard_ConstantDeclarations_Negative(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantSubcmd string
	}{
		{
			name:       "grouped mutating const",
			src:        "package sample_test\n\nconst (\n\tsub = \"commit\"\n)\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
			wantSubcmd: "commit",
		},
		{
			name:       "multi-spec mutating const",
			src:        "package sample_test\n\nconst a, sub = \"rev-parse\", \"push\"\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
			wantSubcmd: "push",
		},
		{
			name:       "inherited mutating const value",
			src:        "package sample_test\n\nconst (\n\tfirst = \"checkout\"\n\tsecond\n)\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, second)\n}\n",
			wantSubcmd: "checkout",
		},
		{
			name:       "chained mutating const",
			src:        "package sample_test\n\nconst base = \"rebase\"\nconst sub = base\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
			wantSubcmd: "rebase",
		},
		{
			name:       "local func mutating const",
			src:        "package sample_test\n\nfunc f() {\n\tconst sub = \"init\"\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n",
			wantSubcmd: "init",
		},
	}
	for i := 0; i < len(cases) && i < maxCallArgs; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertSingleFinding(t, tc.name, "sample_test.go", []byte(tc.src), tc.wantSubcmd)
		})
	}
}

// TestGitProbeGuard_ConstantDeclarations_Boundary covers boundary conditions in constant resolution:
// non-string constants, empty groups, circular references, and ambiguous redeclarations.
func TestGitProbeGuard_ConstantDeclarations_Boundary(t *testing.T) {
	nonStringSrc := []byte("package sample_test\n\nconst sub = 12345\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n")
	assertSingleFinding(t, "non-string const", "nonstring_test.go", nonStringSrc, "<non-literal>")

	emptyGroupSrc := []byte("package sample_test\n\nconst ()\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, \"rev-parse\", \"HEAD\")\n}\n")
	assertNoFindings(t, "empty const group", "empty_group_test.go", emptyGroupSrc)

	circularSrc := []byte("package sample_test\n\nconst a = b\nconst b = a\n\nfunc f() {\n\tutil.RunGitProbe(ctx, dir, 4096, a)\n}\n")
	assertSingleFinding(t, "circular const", "circular_test.go", circularSrc, "<non-literal>")

	ambiguousSrc := []byte("package sample_test\n\nfunc f1() {\n\tconst sub = \"init\"\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n\nfunc f2() {\n\tconst sub = \"rev-parse\"\n\tutil.RunGitProbe(ctx, dir, 4096, sub)\n}\n")
	findings, err := ScanProbeMutations("ambiguous_test.go", ambiguousSrc)
	if err != nil || len(findings) != 2 {
		t.Fatalf("ambiguous consts: want 2 findings, got err=%v findings=%v", err, findings)
	}
	for i := 0; i < len(findings) && i < maxCallArgs; i++ {
		if findings[i].Subcommand != "<non-literal>" {
			t.Fatalf("ambiguous finding %d: want <non-literal>, got %q", i, findings[i].Subcommand)
		}
	}
}
