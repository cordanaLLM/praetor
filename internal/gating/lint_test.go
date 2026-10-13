// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The findings golangci-lint and reuse lint print for the two defects #1312 planted.
const (
	plantedGocognit = "internal/plant/plant.go:9:1: cognitive complexity 30 of func `Tangled` is high (> 16) (gocognit)"
	plantedSPDX     = "'internal/plant/plant.go' contains invalid SPDX License Expressions"
)

// lintRunner answers the lint stage's commands: golangci-lint's run with golangciErr and the
// planted finding, reuse --version with version, reuse lint with reuseErr and the planted finding.
func lintRunner(recorded *[]recordedCommand, golangciErr, reuseErr error, version string) commandRunner {
	return func(_ context.Context, dir, name string, args ...string) (string, error) {
		*recorded = append(*recorded, recordedCommand{dir: dir, name: name, args: args})
		switch {
		case name == "go" && len(args) > 1 && args[1] == golangciLintModule:
			if golangciErr != nil {
				return plantedGocognit + "\n1 issues:\n* gocognit: 1\n", golangciErr
			}
			return "0 issues.\n", nil
		case name == reuseBinary && slices.Equal(args, []string{"--version"}):
			return version, nil
		case name == reuseBinary && slices.Equal(args, []string{"lint"}):
			if reuseErr != nil {
				return plantedSPDX + "\n* Invalid SPDX License Expressions: 1\n", reuseErr
			}
			return "Congratulations! Your project is compliant\n", nil
		}
		return "", nil
	}
}

// lintRepo is a git repository holding go.mod, a golangci-lint configuration and REUSE.toml, as
// files picks; it records no origin default branch.
func lintRepo(t *testing.T, files ...string) string {
	t.Helper()
	repo := newHermeticGitRepo(t)
	for _, name := range files {
		writeFile(t, filepath.Join(repo, name), "\n")
	}
	return repo
}

// Positive and negative (#1312): a golangci-lint finding and a reuse lint finding each fail the
// stage with the finding itself in the message; with both clean the stage passes and names both.
func TestRunLintStage_FindingsFailTheStage(t *testing.T) {
	repo := lintRepo(t, "go.mod", ".golangci.yml", "REUSE.toml")
	failed := errors.New("exit status 1")
	cases := []struct {
		name                  string
		golangciErr, reuseErr error
		want                  string
	}{
		{"gocognit planted", failed, nil, plantedGocognit},
		{"invalid SPDX planted", nil, failed, plantedSPDX},
	}
	for _, tc := range cases {
		cfg, recorded := newTestConfig(t, repo, false)
		cfg.run = lintRunner(recorded, tc.golangciErr, tc.reuseErr, "reuse, version 6.2.0\n")
		_, err := runLintStage(t.Context(), cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want the finding %q", tc.name, err, tc.want)
		}
	}
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = lintRunner(recorded, nil, nil, "reuse, version 6.2.0\n")
	msg, err := runLintStage(t.Context(), cfg)
	if err != nil {
		t.Fatalf("clean lint: %v", err)
	}
	for _, want := range []string{"golangci-lint passed on every package (no origin default branch recorded", "reuse lint passed (reuse, version 6.2.0)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}

// Negative (#1312): a missing reuse fails closed like a missing scanner, and another release line
// than CI's is refused; a dry run runs nothing.
func TestRunLintStage_RefusesWhatItCannotRun(t *testing.T) {
	repo := lintRepo(t, "REUSE.toml")
	cfg, _ := newTestConfig(t, repo, false)
	cfg.lookPath = func(name string) (string, error) { return "", exec.ErrNotFound }
	if _, err := runLintStage(t.Context(), cfg); !errors.Is(err, ErrMissingScanner) {
		t.Fatalf("missing reuse: err %v, want ErrMissingScanner", err)
	}
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = lintRunner(recorded, nil, nil, "reuse, version 5.0.2\n")
	if _, err := runLintStage(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "the gate runs reuse 6.x") {
		t.Fatalf("reuse 5: err %v", err)
	}
	cfg, recorded = newTestConfig(t, repo, true)
	_, err := runLintStage(t.Context(), cfg)
	var skip *stageSkip
	if !errors.As(err, &skip) || skip.status != StageSkipped || len(*recorded) != 0 {
		t.Fatalf("dry run: err %v, commands %+v", err, *recorded)
	}
}

// Boundary (#1312): a repository with neither a golangci-lint configuration nor REUSE gets a
// not-applicable stage that names both checks it did not run; a go.mod without a configuration
// says golangci-lint was not run.
func TestRunLintStage_NotApplicableNamesWhatDidNotRun(t *testing.T) {
	repo := lintRepo(t, "go.mod")
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = lintRunner(recorded, nil, nil, "")
	_, err := runLintStage(t.Context(), cfg)
	var skip *stageSkip
	if !errors.As(err, &skip) || skip.status != StageNotApplicable {
		t.Fatalf("err %v, want not applicable", err)
	}
	for _, want := range []string{"golangci-lint: not run, no .golangci.yml", "reuse lint: no REUSE.toml or LICENSES/"} {
		if !strings.Contains(skip.reason, want) {
			t.Errorf("reason %q lacks %q", skip.reason, want)
		}
	}
	if len(*recorded) != 0 {
		t.Fatalf("a stage with nothing to run ran %+v", *recorded)
	}
}

// Positive (#1312): with the origin default branch recorded, golangci-lint runs on the packages
// the head changed since the merge base, as the pre-push hook does.
func TestRunLintStage_LintsChangedPackages(t *testing.T) {
	repo := lintRepo(t)
	env := testsupport.HermeticGitEnv(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repo, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/lint\n")
	writeFile(t, filepath.Join(repo, ".golangci.yml"), "version: \"2\"\n")
	writeFile(t, filepath.Join(repo, "pkg/old/old.go"), "package old\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	writeFile(t, filepath.Join(repo, "pkg/a/a.go"), "package a\n")
	writeFile(t, filepath.Join(repo, "pkg/b/testdata/x.go"), "package x\n")
	writeFile(t, filepath.Join(repo, "docs/readme.md"), "# docs\n")
	git("add", "-A")
	git("commit", "-q", "-m", "change")

	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = lintRunner(recorded, nil, nil, "")
	msg, err := runLintStage(t.Context(), cfg)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(msg, "golangci-lint passed on 1 changed package(s) since origin/main") {
		t.Fatalf("message %q", msg)
	}
	if len(*recorded) != 1 || !slices.Equal((*recorded)[0].args, []string{"run", golangciLintModule, "run", "./pkg/a"}) {
		t.Fatalf("commands %+v, want golangci-lint on ./pkg/a only", *recorded)
	}
}

// Boundary (#1312): a changed go.mod or configuration lints every package, a removed package and
// testdata are left out, and a head without Go changes lints nothing.
func TestLintPackages(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"a", "b", "c/d"} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name    string
		changed []string
		want    []string
		scope   string
	}{
		{"packages", []string{"b/x.go", "a/y.go", "a/z.go", "c/d/e.go"}, []string{"./a", "./b", "./c/d"}, "3 changed package(s) since base"},
		{"go.mod", []string{"a/y.go", "go.mod"}, []string{allGoPackages}, "every package (go.mod changed since base)"},
		{"nested go.mod is a file", []string{"a/go.mod"}, nil, "no Go package changed since base"},
		{"config", []string{".golangci.yml"}, []string{allGoPackages}, "every package (.golangci.yml changed since base)"},
		{"removed package", []string{"gone/x.go", "a/y.go"}, []string{"./a"}, "1 changed package(s) since base"},
		{"testdata", []string{"a/testdata/x.go"}, nil, "no Go package changed since base"},
		{"no Go", []string{"README.md"}, nil, "no Go package changed since base"},
	}
	for _, tc := range cases {
		got, scope, err := lintPackages(repo, tc.changed, "base")
		if err != nil || !slices.Equal(got, tc.want) || scope != tc.scope {
			t.Errorf("%s: %v %q %v, want %v %q", tc.name, got, scope, err, tc.want, tc.scope)
		}
	}
}

// reuseVersionMajor reads the major from reuse's own version line.
func TestReuseVersionMajor(t *testing.T) {
	for out, want := range map[string]string{"reuse, version 6.2.0\n": "6", "reuse, version 10.0.1": "10", "": "", "garbage": "garbage"} {
		if got := reuseVersionMajor(out); got != want {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
}
