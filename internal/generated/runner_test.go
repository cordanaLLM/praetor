// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Marker (#696): positive, the default marker on branch and title, a breaking title and a
// refs/heads/ branch; negative, either part missing or another type or scope; boundary, the bare
// prefix and a marker without a scope, which admits any scope of its type.
func TestMarkerMatches_3D(t *testing.T) {
	marker := Marker{BranchPrefix: "regen/", TitleType: "chore(generated)"}
	for _, tc := range []struct {
		branch, title string
		want          bool
	}{
		{"regen/batch-7", "chore(generated): render batch 7", true},
		{"refs/heads/regen/batch-7", "chore(generated)!: render batch 7", true},
		{"feat/batch-7", "chore(generated): render batch 7", false},
		{"regen/batch-7", "chore: render batch 7", false},
		{"regen/batch-7", "chore(docs): render batch 7", false},
		{"regen/batch-7", "fix(generated): render batch 7", false},
		{"regen/batch-7", "", false},
		{"", "chore(generated): render", false},
		{"regen/", "chore(generated): render", false},
		{"regen/batch-7", "chore(generated):render", false},
	} {
		if got := marker.Matches(tc.branch, tc.title); got != tc.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", tc.branch, tc.title, got, tc.want)
		}
	}
	anyScope := Marker{BranchPrefix: "regen/", TitleType: "build"}
	if !anyScope.Matches("regen/x", "build(docs): render") || !anyScope.Matches("regen/x", "build: render") {
		t.Fatal("a marker without a scope must admit its type with any scope")
	}
	if (Marker{BranchPrefix: "regen/"}).Matches("regen/x", "build: render") {
		t.Fatal("a marker without a title type admits nothing")
	}
}

// execCommand (#696): positive, a declared variable reaches the command and praetorctl resolves
// to the running binary; negative, an unresolvable binary and a failing command are errors.
func TestExecCommand_3D(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := execCommand(ctx, t.TempDir(), []string{"git", "var", "GIT_AUTHOR_IDENT"},
		[]string{"GIT_AUTHOR_NAME=Render Bot", "GIT_AUTHOR_EMAIL=render@example.invalid"})
	if err != nil || !strings.HasPrefix(string(result.Stdout), "Render Bot <render@example.invalid>") {
		t.Fatalf("declared environment: %q, %v", result.Stdout, err)
	}
	original := selfExecutable
	t.Cleanup(func() { selfExecutable = original })
	selfExecutable = func() (string, error) { return git, nil }
	if result, err = execCommand(ctx, t.TempDir(), []string{SelfCommand, "--version"}, nil); err != nil || !strings.Contains(string(result.Stdout), "git version") {
		t.Fatalf("praetorctl must run the running binary: %q, %v", result.Stdout, err)
	}
	selfExecutable = func() (string, error) { return "", errors.New("no executable") }
	if _, err := execCommand(ctx, t.TempDir(), []string{SelfCommand, "audit"}, nil); err == nil || !strings.Contains(err.Error(), "resolve the running praetorctl") {
		t.Fatalf("an unresolvable praetorctl: %v", err)
	}
	if _, err := execCommand(ctx, t.TempDir(), []string{"git", "no-such-subcommand"}, nil); err == nil {
		t.Fatal("a failing command must be an error")
	}
}

// renderJobs and runJobs (#696): positive, artefacts sharing a command and environment render
// once under the longest timeout; negative, a failure names the command and its output for every
// artefact of the job and a later job still runs; boundary, a job past its timeout is reported as
// cut off, and an expired run stops before the next job.
func TestRenderJobs_3D(t *testing.T) {
	artefacts := []Artefact{
		{Name: "a", Command: []string{"gen"}, timeout: time.Second},
		{Name: "b", Command: []string{"gen"}, timeout: 2 * time.Second},
		{Name: "c", Command: []string{"gen"}, Env: []string{"X=1"}, timeout: time.Second},
		{Name: "d", Command: []string{"slow"}, timeout: 20 * time.Millisecond},
	}
	jobs := renderJobs(artefacts)
	if len(jobs) != 3 || strings.Join(jobs[0].names, ",") != "a,b" || jobs[0].timeout != 2*time.Second {
		t.Fatalf("jobs = %+v", jobs)
	}
	var ran []string
	run := func(ctx context.Context, _ string, argv, env []string) (util.CommandBytes, error) {
		ran = append(ran, strings.Join(append(argv, env...), " "))
		switch argv[0] {
		case "slow":
			<-ctx.Done()
			return util.CommandBytes{}, ctx.Err()
		case "gen":
			if len(env) == 0 {
				return util.CommandBytes{Stdout: []byte("partial"), Stderr: []byte("boom\n")}, errors.New("exit status 2")
			}
		}
		return util.CommandBytes{}, nil
	}
	failures, err := runJobs(context.Background(), run, t.TempDir(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	if failures["a"] != "gen failed: exit status 2: boom" || failures["b"] != failures["a"] || failures["c"] != "" {
		t.Fatalf("failures = %q", failures)
	}
	if !strings.Contains(failures["d"], "slow failed: did not finish within 20ms") {
		t.Fatalf("a cut-off job: %q", failures["d"])
	}
	if strings.Join(ran, ";") != "gen;gen X=1;slow" {
		t.Fatalf("ran = %q", ran)
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runJobs(expired, run, t.TempDir(), jobs); err == nil || !strings.Contains(err.Error(), "rendering stopped before gen") {
		t.Fatalf("an expired run must stop: %v", err)
	}
}

// Blocks (#696): positive, a block is extracted and replaced line for line; negative, an
// unbalanced marker is an error and a file without the block cannot take a rendering; boundary,
// a marker quoted inside fenced code is not a marker.
func TestBlocks_3D(t *testing.T) {
	block := &Block{Start: "<!-- s -->", End: "<!-- e -->"}
	content := "head\n<!-- s -->\nold\n<!-- e -->\ntail\n"
	got, found, err := extractBlock(content, block)
	if err != nil || !found || got != "<!-- s -->\nold\n<!-- e -->" {
		t.Fatalf("extract = %q, %v, %v", got, found, err)
	}
	replaced, err := replaceBlock(content, "<!-- s -->\nnew\nlines\n<!-- e -->", block)
	if err != nil || replaced != "head\n<!-- s -->\nnew\nlines\n<!-- e -->\ntail\n" {
		t.Fatalf("replace = %q, %v", replaced, err)
	}
	if _, _, err := extractBlock("<!-- s -->\nonly a start\n", block); err == nil {
		t.Fatal("an unbalanced block must be an error")
	}
	if _, err := replaceBlock("no block here\n", "x", block); err == nil {
		t.Fatal("a file without the block cannot take a rendering")
	}
	if _, found, err := extractBlock("```\n<!-- s -->\n<!-- e -->\n```\n", block); err != nil || found {
		t.Fatalf("a fenced marker is no marker: %v, %v", found, err)
	}
}
