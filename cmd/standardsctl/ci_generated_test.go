// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/generated"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// rendererSource is a stand-in generator: "upper <src> <dst>" writes src upper-cased to dst, and
// any other word fails the way a broken generator does.
const rendererSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "upper" {
		data, err := os.ReadFile(os.Args[2])
		if err == nil {
			err = os.WriteFile(os.Args[3], []byte(strings.ToUpper(string(data))), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "renderer refused")
	os.Exit(3)
}
`

// generatedFixture is an adopter repository declaring one generated artefact, out/a.txt,
// rendered from src/a.txt by the stand-in generator.
type generatedFixture struct {
	dir  string
	env  []string
	main string
}

// newGeneratedFixture commits the manifest, the source and its fresh rendering on main; with
// fail, the declared command fails.
func newGeneratedFixture(t *testing.T, fail bool) generatedFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	renderer := testsupport.BuildExecutable(t, t.TempDir(), "renderer", rendererSource)
	verb := "upper"
	if fail {
		verb = "explode"
	}
	f := generatedFixture{dir: t.TempDir(), env: testsupport.HermeticGitEnv(t)}
	writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\ngenerated:\n  artefacts:\n    - name: \"upper copy\"\n"+
		"      paths: [\"out/a.txt\"]\n      command: ["+strconv.Quote(renderer)+", \""+verb+"\", \"src/a.txt\", \"out/a.txt\"]\n"+
		"      sources: [\"src/*.txt\"]\n")
	writeFixtureFile(t, f.dir, ".gitignore", "/.standards/worktrees/\n")
	writeFixtureFile(t, f.dir, "src/a.txt", "alpha\n")
	writeFixtureFile(t, f.dir, "out/a.txt", "ALPHA\n")
	f.git(t, "init", "-q", "-b", "main")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "base")
	f.main = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
	return f
}

func (f generatedFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runFixtureGit(t, f.dir, f.env, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return out
}

// change commits files on a new branch from main.
func (f generatedFixture) change(t *testing.T, branch string, files map[string]string) {
	t.Helper()
	f.git(t, "checkout", "-q", "-B", branch, f.main)
	for rel, content := range files {
		writeFixtureFile(t, f.dir, rel, content)
	}
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", branch)
}

// run runs one ci generated subcommand on the fixture and returns its output and error.
func (f generatedFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stderr string
	stdout, err := captureStdout(t, func() error {
		out, runErr := captureStderr(t, func() error {
			return dispatchCommand("ci", append([]string{"generated", args[0], "--dir=" + f.dir}, args[1:]...))
		})
		stderr = out
		return runErr
	})
	return stdout + stderr, err
}

// Positive (#696): list prints the declared artefact and the default marker, as text and as the
// JSON a merge train reads; a change to the source alone passes the pull-request check, and on a
// fresh main render --check passes.
func TestCIGenerated_Positive_ListCheckAndRender(t *testing.T) {
	f := newGeneratedFixture(t, false)
	out, err := f.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	mustContain(t, out, "=== Generated Artefacts: 9 declared ===", "branch regen/<name> and a title chore(generated): <summary>",
		"[active] upper copy (manifest)", "paths:   out/a.txt", "files:   1", "[inactive] debt baseline (builtin): .standards-baseline.json is absent")
	out, err = f.run(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var set generated.Set
	if err := json.Unmarshal([]byte(out), &set); err != nil || set.Marker.BranchPrefix != "regen/" || len(set.Artefacts) != 9 {
		t.Fatalf("list --json = %+v, %v\n%s", set, err, out)
	}
	if out, err = f.run(t, "render", "--check"); err != nil {
		t.Fatalf("render --check on a fresh main: %v\n%s", err, out)
	}
	mustContain(t, out, "upper copy: rendered ok", "[PASS] every declared generated artefact passed.")
	f.change(t, "feat/beta", map[string]string{"src/a.txt": "beta\n"})
	if out, err = f.run(t, "check", "--base=main", "--branch=feat/beta", "--title=feat: beta"); err != nil {
		t.Fatalf("a source change: %v\n%s", err, out)
	}
	mustContain(t, out, "Regeneration change: false", "upper copy: rendered ok; sources changed src/a.txt; differs from its rendering out/a.txt")
}

// Negative (#696): a pull request that edits the artefact is refused, a failing generator fails
// closed, and render --check fails on a stale main without writing.
func TestCIGenerated_Negative_EditFailureAndStale(t *testing.T) {
	f := newGeneratedFixture(t, false)
	f.change(t, "feat/edit", map[string]string{"out/a.txt": "HAND EDIT\n"})
	out, err := f.run(t, "check", "--base=main", "--branch=feat/edit", "--title=feat: edit")
	if err == nil || !strings.Contains(err.Error(), "1 generated-artefact problem") {
		t.Fatalf("an artefact edit must fail: %v\n%s", err, out)
	}
	mustContain(t, out, "upper copy: the change edits out/a.txt; only a regeneration change")
	f.git(t, "checkout", "-q", "main")
	writeFixtureFile(t, f.dir, "src/a.txt", "gamma\n")
	f.git(t, "commit", "-q", "-am", "feat: gamma")
	if out, err = f.run(t, "render", "--check"); err == nil {
		t.Fatalf("render --check on a stale main must fail:\n%s", out)
	}
	mustContain(t, out, "upper copy: stale: out/a.txt differ from their rendering")
	if got := readFixtureFile(t, f.dir, "out/a.txt"); got != "ALPHA\n" {
		t.Fatalf("render --check wrote %q", got)
	}
	failing := newGeneratedFixture(t, true)
	failing.change(t, "feat/beta", map[string]string{"src/a.txt": "beta\n"})
	out, err = failing.run(t, "check", "--base=main", "--json")
	if err == nil || !strings.Contains(out, `"rendered": "failed"`) || !strings.Contains(out, "renderer refused") {
		t.Fatalf("a failing generator must fail the check: %v\n%s", err, out)
	}
}

// Boundary (#696): the regeneration marker admits exactly the rendering, and render without
// --check writes it; an unknown subcommand, a positional argument and no subcommand at all are
// handled without running anything.
func TestCIGenerated_Boundary_MarkerRenderAndUsage(t *testing.T) {
	f := newGeneratedFixture(t, false)
	writeFixtureFile(t, f.dir, "src/a.txt", "beta\n")
	f.git(t, "commit", "-q", "-am", "feat: beta")
	f.main = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
	if out, err := f.run(t, "render"); err != nil || readFixtureFile(t, f.dir, "out/a.txt") != "BETA\n" {
		t.Fatalf("render: %v\n%s", err, out)
	}
	f.git(t, "checkout", "-q", "-b", "regen/batch-1")
	f.git(t, "commit", "-q", "-am", "chore(generated): render batch 1")
	out, err := f.run(t, "check", "--base="+f.main, "--branch=regen/batch-1", "--title=chore(generated): render batch 1")
	if err != nil {
		t.Fatalf("a regeneration change carrying the rendering must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "Regeneration change: true", "upper copy: rendered ok; edited out/a.txt")
	if out, err = f.run(t, "check", "--base="+f.main, "--branch=regen/batch-1", "--title=feat: render"); err == nil {
		t.Fatalf("a title without the marker's type must not admit the edit:\n%s", out)
	}
	if err := dispatchCommand("ci", []string{"generated", "nope"}); err == nil || !strings.Contains(err.Error(), "unknown ci generated subcommand") {
		t.Fatalf("unknown subcommand: %v", err)
	}
	if err := dispatchCommand("ci", []string{"generated", "list", "extra"}); err == nil || !strings.Contains(err.Error(), "accepts no positional arguments") {
		t.Fatalf("positional argument: %v", err)
	}
	usage, err := captureStdout(t, func() error { return dispatchCommand("ci", []string{"generated"}) })
	if err != nil || !strings.Contains(usage, "check [--dir=.] [--base=origin/main]") {
		t.Fatalf("usage: %v\n%s", err, usage)
	}
}
