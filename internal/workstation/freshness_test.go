// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const engineModule = "example.com/engine"

// engineCheckout is a real git repository declaring engineModule, with one committed Go
// source, so CheckBuildCurrent has an engine checkout and a revision to compare against.
type engineCheckout struct {
	t    *testing.T
	root string
	ctx  context.Context
}

func newEngineCheckout(t *testing.T) *engineCheckout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; the engine-build check compares revisions through git on this leg")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	c := &engineCheckout{t: t, root: root, ctx: ctx}
	c.git("init", "-q")
	c.write("go.mod", "module "+engineModule+"\n\ngo 1.27\n")
	c.write("cmd/engine/main.go", "package main\n\nfunc main() {}\n")
	c.commit("init")
	return c
}

func (c *engineCheckout) git(args ...string) string {
	c.t.Helper()
	out, err := util.RunGit(c.ctx, c.root, args...)
	if err != nil {
		c.t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func (c *engineCheckout) write(rel, content string) {
	c.t.Helper()
	path := filepath.Join(c.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

// commit stages everything and returns the new HEAD.
func (c *engineCheckout) commit(message string) string {
	c.t.Helper()
	c.git("add", "-A")
	c.git("-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", message)
	return c.git("rev-parse", "HEAD")
}

// setTime pins rel's modification time, so a test states which side of the build it lies on.
func (c *engineCheckout) setTime(rel string, at time.Time) {
	c.t.Helper()
	if err := os.Chtimes(filepath.Join(c.root, filepath.FromSlash(rel)), at, at); err != nil {
		c.t.Fatal(err)
	}
}

// build is a fake engine binary stamped with revision; built inside the checkout's bin/.
func (c *engineCheckout) build(revision string, modified bool, at time.Time) Build {
	return Build{Module: engineModule, Revision: revision, Modified: modified,
		Executable: filepath.Join(c.root, "bin", "praetorctl"), ModTime: at}
}

func requireStale(t *testing.T, err error, fragment string) {
	t.Helper()
	if !errors.Is(err, ErrStaleEngine) {
		t.Fatalf("want ErrStaleEngine, got %v", err)
	}
	if !strings.Contains(err.Error(), fragment) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("want one line naming %q, got %q", fragment, err)
	}
	if !strings.Contains(err.Error(), "go run ./cmd/standardsctl compile-context") {
		t.Fatalf("refusal must name the checkout command that writes instead: %q", err)
	}
}

// Positive: a clean build of HEAD, and a clean build of an older revision when only files
// outside the Go build changed since, both match the checkout.
func TestCheckBuildCurrent_Positive_MatchingBuilds(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	if err := CheckBuildCurrent(context.Background(), c.root, c.build(head, false, time.Now())); err != nil {
		t.Fatalf("clean build of HEAD: %v", err)
	}
	c.write("README.md", "docs only\n")
	c.write("AGENTS.md", "agent text\n")
	c.commit("docs")
	if err := CheckBuildCurrent(context.Background(), c.root, c.build(head, false, time.Now())); err != nil {
		t.Fatalf("docs-only commits since the build: %v", err)
	}
}

// Positive: the binary the checkout's own build or hook produced from a modified tree matches
// while every changed input is older than it.
func TestCheckBuildCurrent_Positive_DirtyBuildInsideCheckout(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	built := time.Now().Add(time.Hour)
	c.write("cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	c.write("internal/extra/extra.go", "package extra\n")
	if err := CheckBuildCurrent(context.Background(), c.root, c.build(head, true, built)); err != nil {
		t.Fatalf("dirty build newer than every change: %v", err)
	}
}

// Positive: any other repository, and an unstamped build (go run), are not judged at all.
func TestCheckBuildCurrent_Positive_NotApplicable(t *testing.T) {
	c := newEngineCheckout(t)
	foreign := Build{Module: "example.com/other", Revision: strings.Repeat("0", 40)}
	if err := CheckBuildCurrent(context.Background(), c.root, foreign); err != nil {
		t.Fatalf("another module's checkout: %v", err)
	}
	if err := CheckBuildCurrent(context.Background(), t.TempDir(), c.build(strings.Repeat("0", 40), false, time.Now())); err != nil {
		t.Fatalf("directory without go.mod: %v", err)
	}
	if err := CheckBuildCurrent(context.Background(), c.root, Build{Module: engineModule}); err != nil {
		t.Fatalf("unstamped build: %v", err)
	}
}

// Negative: the reported defect. A clean install of an older revision is refused once a Go
// source changed in the checkout, committed or untracked.
func TestCheckBuildCurrent_Negative_CleanBuildLagsCheckout(t *testing.T) {
	c := newEngineCheckout(t)
	installed := c.git("rev-parse", "HEAD")
	c.write("internal/render/render.go", "package render\n")
	c.commit("new renderer")
	err := CheckBuildCurrent(context.Background(), c.root, c.build(installed, false, time.Now().Add(time.Hour)))
	requireStale(t, err, "lacks 1 changed Go build inputs (first internal/render/render.go)")

	head := c.git("rev-parse", "HEAD")
	c.write("internal/render/untracked.go", "package render\n")
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, c.build(head, false, time.Now())),
		"first internal/render/untracked.go")
}

// Negative: a build of a modified tree elsewhere carries edits its stamp does not name, so it
// is refused even when the checkout sits at its revision; a revision the checkout does not
// hold and a malformed stamp cannot be compared at all.
func TestCheckBuildCurrent_Negative_UncomparableBuilds(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	elsewhere := c.build(head, true, time.Now())
	elsewhere.Executable = filepath.Join(t.TempDir(), "praetorctl")
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, elsewhere), "modified tree outside this checkout")

	unknown := c.build(strings.Repeat("ab", 20), false, time.Now())
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, unknown), "cannot be compared")

	malformed := c.build("--output=/tmp/x", false, time.Now())
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, malformed), "stamps no comparable revision")

	var nilCtx context.Context
	if err := CheckBuildCurrent(nilCtx, c.root, c.build(head, false, time.Now())); err == nil || errors.Is(err, ErrStaleEngine) {
		t.Fatalf("nil context must be refused as a caller error, got %v", err)
	}
}

// Boundary: a changed input modified exactly at the build time can be in the build; one a
// second later, a deleted one, or any change against a build of unknown age cannot.
func TestCheckBuildCurrent_Boundary_ModificationTime(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	built := time.Now().Truncate(time.Second)
	c.write("cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	c.setTime("cmd/engine/main.go", built)
	if err := CheckBuildCurrent(context.Background(), c.root, c.build(head, true, built)); err != nil {
		t.Fatalf("input modified at the build time: %v", err)
	}
	c.setTime("cmd/engine/main.go", built.Add(time.Second))
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, c.build(head, true, built)), "cannot be shown to contain changed input cmd/engine/main.go")

	c.setTime("cmd/engine/main.go", built)
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, c.build(head, true, time.Time{})), "cannot be shown to contain changed input cmd/engine/main.go")

	c.git("checkout", "-q", "--", "cmd/engine/main.go")
	if err := os.Remove(filepath.Join(c.root, "cmd", "engine", "main.go")); err != nil {
		t.Fatal(err)
	}
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, c.build(head, true, built.Add(time.Hour))), "cannot be shown to contain changed input cmd/engine/main.go")
}

// Boundary: only what go build compiles counts. Test files and testdata never make a build
// stale; go.sum, which pins what it links, does.
func TestCheckBuildCurrent_Boundary_BuildInputSet(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	c.write("cmd/engine/main_test.go", "package main\n")
	c.write("internal/fixture/testdata/sample.go", "package sample\n")
	c.commit("tests")
	if err := CheckBuildCurrent(context.Background(), c.root, c.build(head, false, time.Now())); err != nil {
		t.Fatalf("test-only changes: %v", err)
	}
	c.write("go.sum", "example.org/dep v1.0.0 h1:x=\n")
	c.commit("dependency")
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, c.build(head, false, time.Now())), "first go.sum")
}

// Positive: the stamp reader returns the full revision and the dirty flag.
func TestBuildStamp_Positive(t *testing.T) {
	revision := strings.Repeat("a", 40)
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "true"},
	}}
	if got, modified := BuildStamp(info); got != revision || !modified {
		t.Fatalf("got %q modified=%v", got, modified)
	}
}

// Negative: no build information, or none carrying a VCS stamp, stamps nothing.
func TestBuildStamp_Negative_Unstamped(t *testing.T) {
	if got, modified := BuildStamp(nil); got != "" || modified {
		t.Fatalf("nil info: %q %v", got, modified)
	}
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-trimpath", Value: "true"}}}
	if got, modified := BuildStamp(info); got != "" || modified {
		t.Fatalf("unstamped info: %q %v", got, modified)
	}
}

// Boundary: the scan stops at maxBuildSettings, so a stamp past it is not read.
func TestBuildStamp_Boundary_ScanBound(t *testing.T) {
	settings := make([]debug.BuildSetting, maxBuildSettings, maxBuildSettings+1)
	settings[maxBuildSettings-1] = debug.BuildSetting{Key: "vcs.revision", Value: "last"}
	info := &debug.BuildInfo{Settings: settings}
	if got, _ := BuildStamp(info); got != "last" {
		t.Fatalf("stamp on the last scanned setting: %q", got)
	}
	info.Settings = append(settings[:maxBuildSettings-1], debug.BuildSetting{}, debug.BuildSetting{Key: "vcs.revision", Value: "beyond"})
	if got, _ := BuildStamp(info); got != "" {
		t.Fatalf("stamp past the bound: %q", got)
	}
}

// Positive: the running test binary resolves its own executable to an absolute path.
func TestRunningBuild_ResolvesExecutable(t *testing.T) {
	build := RunningBuild()
	if !filepath.IsAbs(build.Executable) || build.ModTime.IsZero() {
		t.Fatalf("executable %q modtime %v", build.Executable, build.ModTime)
	}
}
