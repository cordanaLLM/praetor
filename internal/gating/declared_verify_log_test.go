// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// localeSensitiveMake prints translated failures without needing installed locale catalogs.
const localeSensitiveMake = `package main

import (
	"fmt"
	"os"
)

// main models make's message locale and records the child environment relevant to the contract.
func main() {
	word := "Fehler"
	if os.Getenv("LC_ALL") == "C" || (os.Getenv("LC_ALL") == "" && os.Getenv("LC_MESSAGES") == "C") {
		word = "Error"
	}
	_, language := os.LookupEnv("LANGUAGE")
	all, allSet := os.LookupEnv("LC_ALL")
	fmt.Fprintf(os.Stderr, "LANG=%s LC_NUMERIC=%s LC_ALL=%s/%t LC_MESSAGES=%s LANGUAGE=%t guard=%s LC_CTYPE=%s LC_COLLATE=%s\n",
		os.Getenv("LANG"), os.Getenv("LC_NUMERIC"), all, allSet, os.Getenv("LC_MESSAGES"), language,
		os.Getenv("PRAETOR_GATE_DECLARED_VERIFY"), os.Getenv("LC_CTYPE"), os.Getenv("LC_COLLATE"))
	fmt.Fprintf(os.Stderr, "make: *** [Makefile:9: lint] %s 2\n", word)
	os.Exit(2)
}
`

// TestDeclaredVerifyLogGermanParent proves the child emits parseable failures with and without LC_ALL.
func TestDeclaredVerifyLogGermanParent(t *testing.T) {
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "make", localeSensitiveMake)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, all := range []string{"unset", "", "de_DE.UTF-8"} {
		t.Run("LC_ALL="+all, func(t *testing.T) {
			assertDeclaredVerifyGermanParent(t, all)
		})
	}
}

// assertDeclaredVerifyGermanParent checks target extraction, child locale boundaries and unchanged parent state.
func assertDeclaredVerifyGermanParent(t *testing.T, all string) {
	t.Helper()
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	t.Setenv("LC_NUMERIC", "de_DE.UTF-8")
	t.Setenv("LC_CTYPE", "fr_FR.UTF-8")
	t.Setenv("LC_COLLATE", "sv_SE.UTF-8")
	t.Setenv("LC_ALL", all)
	wantAll := "/false"
	wantCType, wantCollate := "fr_FR.UTF-8", "sv_SE.UTF-8"
	switch all {
	case "", "unset":
	default:
		wantCType, wantCollate = all, all
	}
	if all == "unset" {
		if err := os.Unsetenv("LC_ALL"); err != nil {
			t.Fatal(err)
		}
		wantAll = "/false"
	}
	repo, stageDir := declaredLogRepo(t, true, true), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	err := executeDeclaredVerify(ctx, &stageConfig{repoDir: repo}, stageDir, time.Minute, "default")
	if err == nil {
		t.Fatal("planted make failure passed")
	}
	for _, want := range []string{"make verify-all exited 2", "failing make target: lint",
		"LANG=de_DE.UTF-8 LC_NUMERIC=de_DE.UTF-8 LC_ALL=" + wantAll + " LC_MESSAGES=C LANGUAGE=true guard=1 LC_CTYPE=" + wantCType + " LC_COLLATE=" + wantCollate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("stage reason missing %q: %s", want, err)
		}
	}
	if os.Getenv("LC_MESSAGES") != "de_DE.UTF-8" || os.Getenv("LANGUAGE") != "de" || os.Getenv("LC_ALL") != strings.TrimPrefix(all, "unset") {
		t.Fatal("child locale normalization changed the parent environment")
	}
}

// declaredLogRepo needs no commits, identity or signing configuration. Preserve every
// inherited non-Git variable; strip every GIT_* before the two allowed config barriers.
func declaredLogRepo(t *testing.T, ignored, ledger bool) string {
	t.Helper()
	dir := t.TempDir()
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmdCtx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(cmdCtx, dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if ignored {
		writeFile(t, filepath.Join(dir, ".gitignore"), ".workingdir/\n")
	}
	if ledger {
		if err := util.MkdirConfined(dir, ".workingdir", 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// declaredLogPaths locates declared verification logs beneath the checkout's private ledger.
func declaredLogPaths(t *testing.T, repo string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repo, ".workingdir", "gate", "declared-verify-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// plantedVerifyMakefile supplies a failing lint target with progress updates and output beyond the tail limit.
func plantedVerifyMakefile() string {
	var body strings.Builder
	body.WriteString(".PHONY: verify-all lint\nverify-all: lint\nlint:\n\t@printf 'old progress\\rprogress done\\n'\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&body, "\t@printf 'line-%03d\\n'\n", i)
	}
	body.WriteString("\t@exit 2\n")
	return body.String()
}

// TestDeclaredVerifyLogPlantedMakeFailure proves a failed stage names its target and saves its tail in the checkout.
func TestDeclaredVerifyLogPlantedMakeFailure(t *testing.T) {
	requireMake(t)
	repo, stageDir := declaredLogRepo(t, true, true), t.TempDir()
	writeFile(t, filepath.Join(stageDir, "Makefile"), plantedVerifyMakefile())
	cfg := &stageConfig{repoDir: repo, rep: &PipelineReport{}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	run := stage{stageDeclaredVerify, func(ctx context.Context, cfg *stageConfig) (string, error) {
		return "", executeDeclaredVerify(ctx, cfg, stageDir, time.Minute, "default")
	}}
	err := executeStage(ctx, run, cfg)
	if err == nil {
		t.Fatal("planted make failure passed")
	}
	paths := declaredLogPaths(t, repo)
	if len(paths) != 1 {
		t.Fatalf("gate logs = %v; want one under original checkout", paths)
	}
	message := cfg.rep.Stages[0].Message
	for _, want := range []string{"make verify-all exited 2", "failing make target: lint", paths[0], "line-100"} {
		if !strings.Contains(message, want) {
			t.Errorf("stage reason missing %q: %s", want, message)
		}
	}
	assertDeclaredLog(t, paths[0], "line-042", "line-100", declaredVerifyTailLines)
	if paths := declaredLogPaths(t, stageDir); len(paths) != 0 {
		t.Fatalf("logs leaked into disposable stage: %v", paths)
	}
}

// assertDeclaredLog checks the failure header, bounded tail, collapsed progress and private log metadata.
func assertDeclaredLog(t *testing.T, path, first, last string, count int) {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	header, text, found := strings.Cut(string(data), "\n\n")
	if !found || !strings.Contains(header, "failing make target: lint") || len(data) > maxDeclaredVerifyLogBytes {
		t.Fatalf("bad log header or size: %q, %d bytes", header, len(data))
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != count || lines[0] != first || !strings.Contains(text, last) || strings.ContainsRune(text, '\r') {
		t.Fatalf("log tail = %q; want %d collapsed lines beginning %q and containing %q", text, count, first, last)
	}
	assertDeclaredLogModes(t, path)
}

// assertDeclaredLogModes checks UTC log naming and private file and directory permissions.
func assertDeclaredLogModes(t *testing.T, path string) {
	t.Helper()
	stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "declared-verify-"), ".log")
	if _, err := time.Parse("20060102T150405.000000000Z", stamp); err != nil {
		t.Fatalf("log filename lacks UTC timestamp: %v", err)
	}
	for path, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		private, other, privacyErr := util.OwnerOnlyAccess(path)
		if privacyErr != nil || !private {
			t.Errorf("%s privacy: owner-only=%v other=%q err=%v", path, private, other, privacyErr)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != mode {
			t.Errorf("%s mode = %o; want %o", path, info.Mode().Perm(), mode)
		}
	}
}

// TestDeclaredVerifyPassWritesNoLog proves successful verification leaves no failure log.
func TestDeclaredVerifyPassWritesNoLog(t *testing.T) {
	requireMake(t)
	repo, stageDir := declaredLogRepo(t, true, true), t.TempDir()
	writeFile(t, filepath.Join(stageDir, "Makefile"), "verify-all:\n\t@printf 'success\\n'\n")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := executeDeclaredVerify(ctx, &stageConfig{repoDir: repo}, stageDir, time.Minute, "default"); err != nil {
		t.Fatal(err)
	}
	if paths := declaredLogPaths(t, repo); len(paths) != 0 {
		t.Fatalf("passing make wrote logs: %v", paths)
	}
}

// TestDeclaredVerifyDiagnosticBoundaries checks empty output and tail limits below, at and above sixty lines.
func TestDeclaredVerifyDiagnosticBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 59, 60, 61, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			assertDeclaredDiagnosticBoundary(t, count)
		})
	}
}

// assertDeclaredDiagnosticBoundary checks that normalized diagnostics retain the expected final lines.
func assertDeclaredDiagnosticBoundary(t *testing.T, count int) {
	t.Helper()
	var output strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&output, "discarded\rline-%03d\n", i)
	}
	target, tail := declaredVerifyDiagnostic(util.CommandBytes{Stdout: []byte(output.String())})
	want := min(count, declaredVerifyTailLines)
	if target != "make named no failing target" || strings.ContainsRune(tail, '\r') {
		t.Fatalf("diagnostic = %q: %q", target, tail)
	}
	if count == 0 {
		if tail != "" {
			t.Fatalf("empty output has tail %q", tail)
		}
		return
	}
	lines := strings.Split(tail, "\n")
	if len(lines) != want || lines[0] != fmt.Sprintf("line-%03d", count-want+1) || lines[want-1] != fmt.Sprintf("line-%03d", count) {
		t.Fatalf("tail = %q; want last %d of %d lines", tail, want, count)
	}
}

// TestDeclaredVerifyMakeTargets proves valid make failures select the innermost target and malformed lines are ignored.
func TestDeclaredVerifyMakeTargets(t *testing.T) {
	cases := []struct{ output, want string }{
		{"make: *** [Makefile:7: lint] Error 2\n", "lint"},
		{"make[2]: *** [nested.mk:8: unit-test] Error 1\nmake[1]: *** [Makefile:3: lint] Error 2\nmake: *** [Makefile:1: verify-all] Error 2\n", "unit-test"},
		{"make: *** [verify-all] Error 1", "verify-all"},
		{"make: *** [C:/source/check.mk:12: lint] Error 2\r\n", "lint"},
		{"make[999999999999999999999999999999]: *** [Makefile:2: bad] Error 1", ""},
		{"make: *** [Makefile:2: lint] not an error", ""},
		{"make[2]: *** [nested.mk:8: inner] Segmentation fault\nmake: *** [Makefile:2: verify-all] Error 2", "inner"},
		{"make[3]: [nested.mk:9: ignored] Error 1 (ignored)\nmake: *** [Makefile:2: lint] Error 2", "lint"},
		{"make[3]: [nested.mk:9: ignored] Segmentation fault (ignored)\nmake: *** [Makefile:2: lint] Error 2", "lint"},
		{"make[2]: *** [inner] Aborted (core dumped)", "inner"},
		{"make: *** No rule to make target 'absent', needed by 'verify-all'.  Stop.", "absent"},
		{"make[2]: *** No rule to make target `absent'.  Stop.\nmake: *** [verify-all] Error 2", "absent"},
		{
			"make: *** No rule to make target 'absent-prerequisite', needed by 'verify-all'.\n" +
				"make: Target 'verify-all' not remade because of errors.\n",
			"absent-prerequisite",
		},
		{"an unrelated tool failed\n", ""},
	}
	for i, tc := range cases {
		target, _ := declaredVerifyDiagnostic(util.CommandBytes{Stderr: []byte(tc.output)})
		want := "make named no failing target"
		if tc.want != "" {
			want = "failing make target: " + tc.want
		}
		if target != want {
			t.Errorf("case %d target = %q; want %q", i, target, want)
		}
	}
}

// TestDeclaredVerifyRefusesUnsafeLogLocations proves absent, public or unwritable ledger locations receive no logs.
func TestDeclaredVerifyRefusesUnsafeLogLocations(t *testing.T) {
	cases := []struct {
		name            string
		ignored, ledger bool
		want            string
	}{
		{"absent", true, false, ".workingdir unavailable"},
		{"unignored", false, true, ".workingdir is not git-ignored"},
		{"write failed", true, true, "create .workingdir/gate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := declaredLogRepo(t, tc.ignored, tc.ledger)
			if tc.name == "write failed" {
				writeFile(t, filepath.Join(repo, ".workingdir", "gate"), "blocked")
			}
			assertDeclaredLogRefusal(t, repo, tc.want)
			assertDeclaredLedgerPresence(t, repo, tc.ledger)
		})
	}
}

// assertDeclaredLogRefusal checks that refused log writes preserve the refusal reason and failure diagnostics.
func assertDeclaredLogRefusal(t *testing.T, repo, why string) {
	t.Helper()
	err := declaredVerifyFailure(t.Context(), repo, time.Minute, "default", util.CommandBytes{Stdout: []byte("first\nlast")}, exitCodeError{2})
	for _, want := range []string{"no gate log written", why, "make named no failing target", "first\nlast"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reason missing %q: %s", want, err)
		}
	}
	if paths := declaredLogPaths(t, repo); len(paths) != 0 {
		t.Fatalf("unsafe location got logs: %v", paths)
	}
}

// assertDeclaredLedgerPresence checks that log refusal does not create a missing ledger.
func assertDeclaredLedgerPresence(t *testing.T, repo string, present bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(repo, ".workingdir"))
	if !present && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing ledger was created: %v", err)
	}
}

// TestDeclaredVerifyLogLineBoundaries proves persisted logs retain sixty normalized lines at the truncation boundary.
func TestDeclaredVerifyLogLineBoundaries(t *testing.T) {
	for _, count := range []int{60, 61} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo := declaredLogRepo(t, true, true)
			var output strings.Builder
			for i := 1; i <= count; i++ {
				fmt.Fprintf(&output, "old\rline-%03d\n", i)
			}
			_, diagnostic := declaredVerifyDiagnostic(util.CommandBytes{Stdout: []byte(output.String())})
			path, err := writeDeclaredVerifyLog(t.Context(), repo, "failing make target: lint", diagnostic)
			if err != nil {
				t.Fatal(err)
			}
			assertDeclaredLog(t, path, fmt.Sprintf("line-%03d", count-59), fmt.Sprintf("line-%03d", count), 60)
		})
	}
}

// TestDeclaredVerifyDiagnosticKeepsTargetBeforeTailAndBothStreams proves target discovery precedes truncation and both streams contribute.
func TestDeclaredVerifyDiagnosticKeepsTargetBeforeTailAndBothStreams(t *testing.T) {
	stdout := "old\rstdout-last\nmake[2]: *** [nested.mk:4: lint] Error 1\n" + strings.Repeat("other\n", 61)
	stderr := strings.Repeat("old stderr progress\r", 8000) + "stderr-last\nmake: *** [Makefile:2: verify-all] Error 2\n"
	target, diagnostic := declaredVerifyDiagnostic(util.CommandBytes{Stdout: []byte(stdout), Stderr: []byte(stderr)})
	if target != "failing make target: lint" || !strings.Contains(diagnostic, "stderr-last\nmake:") || strings.Contains(diagnostic, "old stderr progress") {
		t.Fatalf("target = %q; diagnostic = %q", target, diagnostic)
	}
	_, both := declaredVerifyDiagnostic(util.CommandBytes{Stdout: []byte("old\rstdout-last"), Stderr: []byte("stderr-last")})
	if both != "stdout-last\nstderr-last" {
		t.Fatalf("combined collapsed output = %q", both)
	}
}

// TestDeclaredVerifyFailureLogBoundsAndCause proves cancellation preserves the cause and bounded diagnostic tails in reasons and logs.
func TestDeclaredVerifyFailureLogBoundsAndCause(t *testing.T) {
	repo := declaredLogRepo(t, true, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cause := exitCodeError{2}
	output := util.CommandBytes{Stdout: []byte(strings.Repeat("x", 2*maxDeclaredVerifyLogBytes) + "last line")}
	err := declaredVerifyFailure(ctx, repo, time.Minute, "default", output, cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "last line") {
		t.Fatalf("cause or bounded reason lost: %v", err)
	}
	assertDeclaredReasonTailBound(t, err.Error())
	paths := declaredLogPaths(t, repo)
	if len(paths) != 1 {
		t.Fatalf("cancelled stage got logs %v; want one", paths)
	}
	data, readErr := fs.ReadFile(os.DirFS(filepath.Dir(paths[0])), filepath.Base(paths[0]))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(data) > maxDeclaredVerifyLogBytes || !strings.HasSuffix(string(data), "last line\n") {
		t.Fatalf("log size = %d or tail lost", len(data))
	}
}

// assertDeclaredReasonTailBound checks that a failure reason includes diagnostics within the byte limit.
func assertDeclaredReasonTailBound(t *testing.T, reason string) {
	t.Helper()
	_, diagnostic, found := strings.Cut(reason, "\n")
	if !found || len(diagnostic) > maxDeclaredVerifyDiagnostic {
		t.Fatalf("diagnostic of %d bytes exceeds %d-byte bound, or no diagnostic separator", len(diagnostic), maxDeclaredVerifyDiagnostic)
	}
}

// TestDeclaredVerifyLogRefusesRedirectedDirectories proves symlinked ledger and gate directories cannot redirect logs.
func TestDeclaredVerifyLogRefusesRedirectedDirectories(t *testing.T) {
	for _, leaf := range []string{".workingdir", ".workingdir/gate"} {
		t.Run(leaf, func(t *testing.T) {
			assertDeclaredRedirectedDirectory(t, leaf)
		})
	}
}

// assertDeclaredRedirectedDirectory checks that symlink refusal leaves the redirect destination empty.
func assertDeclaredRedirectedDirectory(t *testing.T, leaf string) {
	t.Helper()
	repo := declaredLogRepo(t, true, leaf != ".workingdir")
	if err := util.MkdirConfined(repo, "public", 0o700); err != nil {
		t.Fatal(err)
	}
	target, why := "public", ".workingdir is not a directory"
	if leaf != ".workingdir" {
		target, why = "../public", "create .workingdir/gate"
	}
	if err := os.Symlink(target, filepath.Join(repo, filepath.FromSlash(leaf))); err != nil {
		if runtime.GOOS == "windows" && isSymlinkPermissionError(err) {
			t.Skipf("Windows symlink permission unavailable: %v", err)
		}
		t.Fatal(err)
	}
	assertDeclaredLogRefusal(t, repo, why)
	entries, err := os.ReadDir(filepath.Join(repo, "public"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("redirected directory got files: %v", entries)
	}
}

// TestDeclaredVerifyFailureUTF8 proves byte bounds keep repeated multibyte text intact.
func TestDeclaredVerifyFailureUTF8(t *testing.T) {
	repo := declaredLogRepo(t, true, true)
	output := util.CommandBytes{Stdout: []byte(strings.Repeat("é", 40000))}
	err := declaredVerifyFailure(t.Context(), repo, time.Minute, "default", output, exitCodeError{2})
	assertDeclaredReasonTailBound(t, err.Error())
	if !utf8.ValidString(err.Error()) {
		t.Fatal("displayed diagnostic split a UTF-8 rune")
	}
	paths := declaredLogPaths(t, repo)
	if len(paths) != 1 {
		t.Fatalf("logs = %v; want one", paths)
	}
	data, readErr := fs.ReadFile(os.DirFS(filepath.Dir(paths[0])), filepath.Base(paths[0]))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !utf8.Valid(data) || len(data) > maxDeclaredVerifyLogBytes {
		t.Fatalf("log UTF-8 valid=%v size=%d", utf8.Valid(data), len(data))
	}
}

// TestDeclaredVerifyExecutionErrorSurvivesTail preserves overflow separately from partial output.
func TestDeclaredVerifyExecutionErrorSurvivesTail(t *testing.T) {
	repo := declaredLogRepo(t, true, true)
	cause := errors.Join(exitCodeError{2}, fmt.Errorf("command output exceeds %d bytes per stream", util.MaxCommandOutputBytes))
	output := util.CommandBytes{Stdout: []byte(strings.Repeat("partial output\n", 100))}
	err := declaredVerifyFailure(t.Context(), repo, time.Minute, "default", output, cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "command output exceeds 16777216 bytes per stream") {
		t.Fatalf("execution error lost: %v", err)
	}
	paths := declaredLogPaths(t, repo)
	if len(paths) != 1 {
		t.Fatalf("logs = %v; want one", paths)
	}
	data, readErr := fs.ReadFile(os.DirFS(filepath.Dir(paths[0])), filepath.Base(paths[0]))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "execution error:") || !strings.Contains(string(data), "command output exceeds 16777216 bytes per stream") || !strings.Contains(string(data), "partial output") {
		t.Fatal("log lost execution error or partial output")
	}
}

// TestDeclaredVerifyConfiguredRunner prevents invoking real make when a runner was supplied.
func TestDeclaredVerifyConfiguredRunner(t *testing.T) {
	repo, dir := declaredLogRepo(t, true, true), t.TempDir()
	writeFile(t, filepath.Join(dir, "Makefile"), "verify-all:\n\t@echo real-make-ran > bypassed\n")
	calls := 0
	cfg := &stageConfig{repoDir: repo, run: func(_ context.Context, gotDir, name string, args ...string) (string, error) {
		calls++
		if gotDir != dir || name != "make" || strings.Join(args, " ") != "verify-all" {
			t.Errorf("runner invocation = %q %q %v", gotDir, name, args)
		}
		return "make: *** [stub.mk:7: injected] Error 2", exitCodeError{2}
	}}
	err := executeDeclaredVerify(t.Context(), cfg, dir, time.Minute, "default")
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "failing make target: injected") {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bypassed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real make ran instead of stub: %v", err)
	}
}

// TestDeclaredVerifyRecipeLocale uses real make to check recipe categories and parseable failures.
func TestDeclaredVerifyRecipeLocale(t *testing.T) {
	requireMake(t)
	for _, all := range []string{"", "C.UTF-8"} {
		t.Run(all, func(t *testing.T) {
			t.Setenv("LC_ALL", all)
			t.Setenv("LC_CTYPE", "C")
			t.Setenv("LC_COLLATE", "POSIX")
			t.Setenv("LANGUAGE", "de")
			repo, dir := declaredLogRepo(t, true, true), t.TempDir()
			writeFile(t, filepath.Join(dir, "Makefile"), "verify-all: locale-check\nlocale-check:\n\t@echo LC_CTYPE=$(LC_CTYPE) LC_COLLATE=$(LC_COLLATE)\n\t@exit 2\n")
			err := executeDeclaredVerify(t.Context(), &stageConfig{repoDir: repo}, dir, time.Minute, "default")
			ctype, collate := "C", "POSIX"
			if all != "" {
				ctype, collate = all, all
			}
			if err == nil || !strings.Contains(err.Error(), "failing make target: locale-check") || !strings.Contains(err.Error(), "LC_CTYPE="+ctype+" LC_COLLATE="+collate) {
				t.Fatalf("recipe locale or target lost: %v", err)
			}
		})
	}
}

// outputLimitMake overflows stdout without requiring a shell or platform-specific utilities.
const outputLimitMake = `package main
import ("bytes"; "os")
func main() {
	_, err := os.Stdout.Write(bytes.Repeat([]byte("partial-output "), 2000000))
	if err != nil { os.Exit(2) }
}
`

// TestDeclaredVerifyRealOutputLimit preserves the real bounded runner's stop cause and output.
func TestDeclaredVerifyRealOutputLimit(t *testing.T) {
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "make", outputLimitMake)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := declaredLogRepo(t, true, true)
	cfg := newStageConfig(repo, false, &PipelineReport{})
	err := executeDeclaredVerify(t.Context(), cfg, t.TempDir(), time.Minute, "default")
	if err == nil || !strings.Contains(err.Error(), "command output exceeds 16777216 bytes per stream") || !strings.Contains(err.Error(), "partial-output") {
		t.Fatalf("capture overflow or output lost: %v", err)
	}
	assertDeclaredReasonTailBound(t, err.Error())
	paths := declaredLogPaths(t, repo)
	if len(paths) != 1 {
		t.Fatalf("logs = %v; want one", paths)
	}
	data, readErr := fs.ReadFile(os.DirFS(filepath.Dir(paths[0])), filepath.Base(paths[0]))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(data) > maxDeclaredVerifyLogBytes || !strings.Contains(string(data), "command output exceeds 16777216 bytes per stream") || !strings.Contains(string(data), "partial-output") {
		t.Fatal("log lost capture stop cause or output")
	}
}

// TestDeclaredVerifyMissingRule names a prerequisite missing from an actual makefile.
func TestDeclaredVerifyMissingRule(t *testing.T) {
	requireMake(t)
	repo, dir := declaredLogRepo(t, true, true), t.TempDir()
	writeFile(t, filepath.Join(dir, "Makefile"), "verify-all: absent-prerequisite\n")
	err := executeDeclaredVerify(t.Context(), &stageConfig{repoDir: repo}, dir, time.Minute, "default")
	if err == nil || !strings.Contains(err.Error(), "failing make target: absent-prerequisite") {
		t.Fatalf("missing prerequisite lost: %v", err)
	}
}

// TestDeclaredVerifyConfiguredRunnerStderr preserves the string runner's stderr-in-error contract.
func TestDeclaredVerifyConfiguredRunnerStderr(t *testing.T) {
	repo := declaredLogRepo(t, true, true)
	cfg := &stageConfig{repoDir: repo, run: func(context.Context, string, string, ...string) (string, error) {
		return "", errors.New("make: *** [stub.mk:7: stderr-target] Error 2")
	}}
	err := executeDeclaredVerify(t.Context(), cfg, t.TempDir(), time.Minute, "default")
	if err == nil || !strings.Contains(err.Error(), "failing make target: stderr-target") {
		t.Fatalf("configured runner stderr target lost: %v", err)
	}
}

// TestDeclaredVerifyRunnerOutputBounds covers success, exact capture bound and injected overflow.
func TestDeclaredVerifyRunnerOutputBounds(t *testing.T) {
	for _, size := range []int{0, util.MaxCommandOutputBytes, util.MaxCommandOutputBytes + 1} {
		cfg := &stageConfig{run: func(context.Context, string, string, ...string) (string, error) {
			return strings.Repeat("x", size), nil
		}}
		output, err := runDeclaredVerifyCommand(t.Context(), cfg, t.TempDir())
		if len(output.Stdout) > util.MaxCommandOutputBytes || (err != nil) != (size > util.MaxCommandOutputBytes) {
			t.Errorf("size=%d captured=%d error=%v", size, len(output.Stdout), err)
		}
	}
}

// swappingLedgerGit replaces the checked ledger before answering the ignore query.
const swappingLedgerGit = `package main
import ("fmt"; "os")
// main plants a replacement entirely inside the temporary checkout.
func main() {
 if err := os.Rename(".workingdir", "saved-ledger"); err != nil { panic(err) }
 if err := os.Symlink("public", ".workingdir"); err != nil { panic(err) }
 fmt.Print(".gitignore\x001\x00.workingdir/\x00.workingdir/\x00")
}
`

// TestDeclaredVerifyLogReplacementDuringIgnore reproduces the review's valid git answer after a ledger swap.
func TestDeclaredVerifyLogReplacementDuringIgnore(t *testing.T) {
	t.Run("ledger replaced after first check", func(t *testing.T) {
		repo := declaredLogRepo(t, true, true)
		public := filepath.Join(repo, "public")
		if err := util.MkdirSecure(public, 0o700); err != nil {
			t.Fatal(err)
		}
		requireDeclaredLogSymlink(t, repo)
		bin := t.TempDir()
		testsupport.BuildExecutable(t, bin, "git", swappingLedgerGit)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		_, err := writeDeclaredVerifyLog(t.Context(), repo, "failing make target: lint", "last line")
		if err == nil || !strings.Contains(err.Error(), "replaced") {
			t.Errorf("replacement refusal = %v; want replacement named", err)
		}
		entries, readErr := os.ReadDir(public)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("replacement destination changed: %v, %v", entries, readErr)
		}
	})
}

// requireDeclaredLogSymlink skips only platforms denying fixture symlink creation.
func requireDeclaredLogSymlink(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "link-permission-check")
	if err := os.Symlink("public", path); err != nil {
		if runtime.GOOS == "windows" && isSymlinkPermissionError(err) {
			t.Skipf("Windows symlink permission unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// TestDeclaredVerifyLogPinnedWrites checks successful writes and replacement between filesystem steps.
func TestDeclaredVerifyLogPinnedWrites(t *testing.T) {
	for _, phase := range []string{"plain success", "after directory creation", "at write"} {
		t.Run(phase, func(t *testing.T) { assertDeclaredLogPinnedWrite(t, phase) })
	}
}

// assertDeclaredLogPinnedWrite plants swaps before or inside the write seam, after all earlier checks.
func assertDeclaredLogPinnedWrite(t *testing.T, phase string) {
	t.Helper()
	repo := declaredLogRepo(t, true, true)
	public := filepath.Join(repo, "public")
	if err := util.MkdirSecure(filepath.Join(public, "gate"), 0o700); err != nil {
		t.Fatal(err)
	}
	disk := declaredLogSwapIO(t, repo, phase)
	ctx, cancel := context.WithTimeout(t.Context(), util.GitProbeTimeout)
	defer cancel()
	path, err := writeDeclaredVerifyLogWithIO(ctx, repo, "failing make target: lint", "last line", disk)
	if phase == "plain success" {
		if err != nil || filepath.Dir(path) != filepath.Join(repo, ".workingdir", "gate") {
			t.Fatalf("success path=%q error=%v", path, err)
		}
		assertDeclaredLog(t, path, "last line", "last line", 1)
	} else if err == nil || !strings.Contains(err.Error(), "replaced") || path != "" {
		t.Errorf("replacement path=%q error=%v", path, err)
	}
	entries, readErr := os.ReadDir(filepath.Join(public, "gate"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("replacement destination changed: %v, %v", entries, readErr)
	}
}

// declaredLogSwapIO plants one replacement at the requested filesystem seam.
func declaredLogSwapIO(t *testing.T, repo, phase string) declaredVerifyLogIO {
	t.Helper()
	if phase != "plain success" {
		requireDeclaredLogSymlink(t, repo)
	}
	disk := declaredVerifyLogIO{mkdir: util.MkdirIn, write: util.WriteFileExclusiveIn}
	disk.mkdir = func(root *os.Root, rel string, mode os.FileMode) error {
		err := util.MkdirIn(root, rel, mode)
		if err == nil && phase == "after directory creation" {
			err = replaceDeclaredLogLedger(repo)
		}
		return err
	}
	disk.write = func(root *os.Root, rel string, body []byte, mode os.FileMode) error {
		if phase == "at write" {
			if err := replaceDeclaredLogLedger(repo); err != nil {
				return err
			}
		}
		return util.WriteFileExclusiveIn(root, rel, body, mode)
	}
	return disk
}

// replaceDeclaredLogLedger swaps only a fixture directory, preserving the original for readback.
func replaceDeclaredLogLedger(repo string) error {
	if err := os.Rename(filepath.Join(repo, ".workingdir"), filepath.Join(repo, "saved-ledger")); err != nil {
		return fmt.Errorf("rename fixture ledger: %w", err)
	}
	if err := os.Symlink("public", filepath.Join(repo, ".workingdir")); err != nil {
		return fmt.Errorf("link fixture replacement: %w", err)
	}
	return nil
}

// TestDeclaredVerifyLogStalledIO proves both blocking steps return while their fixture remains stalled.
func TestDeclaredVerifyLogStalledIO(t *testing.T) {
	for _, step := range []string{"directory creation", "write and sync"} {
		t.Run(step, func(t *testing.T) { assertDeclaredLogStalledIO(t, step) })
	}
}

// assertDeclaredLogStalledIO releases the worker only after the deadline assertion, also on mutant failure.
func assertDeclaredLogStalledIO(t *testing.T, step string) {
	t.Helper()
	repo := declaredLogRepo(t, true, true)
	release, entered, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stall := func() error { close(entered); <-release; return nil }
	disk := declaredVerifyLogIO{mkdir: util.MkdirIn, write: util.WriteFileExclusiveIn}
	want := "create .workingdir/gate"
	if step == "directory creation" {
		disk.mkdir = func(*os.Root, string, os.FileMode) error { return stall() }
	} else {
		want = "write " + filepath.Join(".workingdir", "gate") + string(os.PathSeparator)
		disk.write = func(*os.Root, string, []byte, os.FileMode) error { return stall() }
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		defer close(finished)
		_, err := writeDeclaredVerifyLogWithIO(ctx, repo, "failing make target: lint", "last line", disk)
		result <- err
	}()
	t.Cleanup(func() {
		close(release)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("writer failed to finish after releasing fixture")
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("filesystem seam not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), want) {
			t.Fatalf("stalled %s error=%v", step, err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("stalled %s exceeded logging deadline plus margin", step)
	}
}
