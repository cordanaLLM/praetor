// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

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

func declaredLogPaths(t *testing.T, repo string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repo, ".workingdir", "gate", "declared-verify-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func plantedVerifyMakefile() string {
	var body strings.Builder
	body.WriteString(".PHONY: verify-all lint\nverify-all: lint\nlint:\n\t@printf 'old progress\\rprogress done\\n'\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&body, "\t@printf 'line-%03d\\n'\n", i)
	}
	body.WriteString("\t@exit 2\n")
	return body.String()
}

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

func assertDeclaredLog(t *testing.T, path, first, last string, count int) {
	t.Helper()
	data, err := os.ReadFile(path)
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
		if runtime.GOOS != "windows" && info.Mode().Perm() != mode {
			t.Errorf("%s mode = %o; want %o", path, info.Mode().Perm(), mode)
		}
	}
}

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

func TestDeclaredVerifyDiagnosticBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 59, 60, 61, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			assertDeclaredDiagnosticBoundary(t, count)
		})
	}
}

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

func TestDeclaredVerifyMakeTargets(t *testing.T) {
	cases := []struct{ output, want string }{
		{"make: *** [Makefile:7: lint] Error 2\n", "lint"},
		{"make[2]: *** [nested.mk:8: unit-test] Error 1\nmake[1]: *** [Makefile:3: lint] Error 2\nmake: *** [Makefile:1: verify-all] Error 2\n", "unit-test"},
		{"make: *** [verify-all] Error 1", "verify-all"},
		{"make: *** [C:/source/check.mk:12: lint] Error 2\r\n", "lint"},
		{"make[999999999999999999999999999999]: *** [Makefile:2: bad] Error 1", ""},
		{"make: *** [Makefile:2: lint] not an error", ""},
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

func assertDeclaredLedgerPresence(t *testing.T, repo string, present bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(repo, ".workingdir"))
	if !present && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing ledger was created: %v", err)
	}
}

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
	data, readErr := os.ReadFile(paths[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(data) > maxDeclaredVerifyLogBytes || !strings.HasSuffix(string(data), "last line\n") {
		t.Fatalf("log size = %d or tail lost", len(data))
	}
}

func assertDeclaredReasonTailBound(t *testing.T, reason string) {
	t.Helper()
	_, diagnostic, found := strings.Cut(reason, "\n")
	if !found || len(diagnostic) > maxDeclaredVerifyDiagnostic {
		t.Fatalf("diagnostic of %d bytes exceeds %d-byte bound, or no diagnostic separator", len(diagnostic), maxDeclaredVerifyDiagnostic)
	}
}

func TestDeclaredVerifyLogRefusesRedirectedDirectories(t *testing.T) {
	for _, leaf := range []string{".workingdir", ".workingdir/gate"} {
		t.Run(leaf, func(t *testing.T) {
			assertDeclaredRedirectedDirectory(t, leaf)
		})
	}
}

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
