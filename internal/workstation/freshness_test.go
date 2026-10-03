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

	"github.com/cordanaLLM/praetor/internal/buildid"
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
	return c.gitIn(c.root, args...)
}

// gitIn runs git in dir, a worktree of the checkout, with the checkout's hermetic environment.
func (c *engineCheckout) gitIn(dir string, args ...string) string {
	c.t.Helper()
	out, err := util.RunGit(c.ctx, dir, args...)
	if err != nil {
		c.t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// renderWorktree mirrors ci generated render (internal/generated/render.go): it ignores bin/ and
// .standards/worktrees/ as the engine's own .gitignore does, commits that, and checks the new
// HEAD out into a temporary worktree below .standards/worktrees. It returns HEAD and the worktree.
func (c *engineCheckout) renderWorktree() (string, string) {
	c.t.Helper()
	c.write(".gitignore", "/bin/\n/.standards/worktrees/\n")
	head := c.commit("ignore build output")
	dir := filepath.Join(c.root, ".standards", "worktrees", "render")
	c.git("worktree", "add", "-q", "--detach", dir, head)
	return head, dir
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

// Positive (#760): after a gate run the untracked receipt marks the checkout's own
// bin/praetorctl modified. ci generated render runs its compile-context in a worktree of HEAD
// below the checkout, and the build matches there: no Go build input of the checkout holding it
// differs from its revision, so only files outside the Go build marked it.
func TestCheckBuildCurrent_Positive_RenderWorktreeOfReceiptCheckout(t *testing.T) {
	c := newEngineCheckout(t)
	head, render := c.renderWorktree()
	c.write(".standards-receipt.json", "{}\n")
	c.write("cmd/engine/main_test.go", "package main\n")
	built := c.build(head, true, time.Now())
	if err := CheckBuildCurrent(context.Background(), render, built); err != nil {
		t.Fatalf("checkout build marked modified by the receipt alone, judged in its render worktree: %v", err)
	}
	if err := CheckBuildCurrent(context.Background(), c.root, built); err != nil {
		t.Fatalf("the same build in its own checkout: %v", err)
	}
	built.Executable = filepath.Join(c.root, "praetorctl")
	if err := CheckBuildCurrent(context.Background(), render, built); err != nil {
		t.Fatalf("a build at the checkout root: %v", err)
	}
}

// Negative (#760): a build outside the judged worktree still fails when it may carry a Go edit:
// its checkout changes a tracked or untracked Go source, even one older than the build that its
// own checkout accepts, or it is a build of another worktree holding a Go edit.
func TestCheckBuildCurrent_Negative_EditedBuildOutsideTheWorktree(t *testing.T) {
	c := newEngineCheckout(t)
	head, render := c.renderWorktree()
	c.write(".standards-receipt.json", "{}\n")
	c.write("cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	built := c.build(head, true, time.Now().Add(time.Hour))
	if err := CheckBuildCurrent(context.Background(), c.root, built); err != nil {
		t.Fatalf("its own checkout accepts the edited build: %v", err)
	}
	requireStale(t, CheckBuildCurrent(context.Background(), render, built),
		"modified tree outside this checkout ("+c.root+" changes 1 Go build inputs, first cmd/engine/main.go)")

	c.git("checkout", "-q", "--", "cmd/engine/main.go")
	c.write("internal/extra/extra.go", "package extra\n")
	requireStale(t, CheckBuildCurrent(context.Background(), render, built), "first internal/extra/extra.go")

	other := filepath.Join(t.TempDir(), "other")
	c.git("worktree", "add", "-q", "--detach", other, head)
	if err := os.WriteFile(filepath.Join(other, "cmd", "engine", "main.go"), []byte("package main\n\nfunc main() { print() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c.root, "internal", "extra", "extra.go")); err != nil {
		t.Fatal(err)
	}
	built.Executable = filepath.Join(other, "bin", "praetorctl")
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, built), "first cmd/engine/main.go")
}

// Boundary (#760): a build judged by the checkout holding it is judged as a clean build, so a Go
// change in the worktree gets no modification-time allowance; an executable in no checkout of the
// module, or in a checkout that lacks the revision, cannot be judged and fails.
func TestCheckBuildCurrent_Boundary_CheckoutHoldingTheBuild(t *testing.T) {
	c := newEngineCheckout(t)
	head, _ := c.renderWorktree()
	c.write(".standards-receipt.json", "{}\n")
	next := filepath.Join(c.root, ".standards", "worktrees", "next")
	c.git("worktree", "add", "-q", "-b", "next", next, head)
	if err := os.MkdirAll(filepath.Join(next, "internal", "render"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "internal", "render", "render.go"), []byte("package render\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.gitIn(next, "add", "-A")
	c.gitIn(next, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "renderer")
	built := c.build(head, true, time.Now().Add(time.Hour))
	requireStale(t, CheckBuildCurrent(context.Background(), next, built), "lacks 1 changed Go build inputs (first internal/render/render.go)")

	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	built.Executable = filepath.Join(foreign, "bin", "praetorctl")
	requireStale(t, CheckBuildCurrent(context.Background(), next, built), "was built from a modified tree outside this checkout; rebuild")

	unrelated := newEngineCheckout(t)
	built.Executable = filepath.Join(unrelated.root, "bin", "praetorctl")
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, built), unrelated.root+" cannot be compared")
}

// Positive: a build is described from the VCS stamp every praetor surface reads
// (buildid.Stamp), and labelled as a binary of that build reports itself (#666).
func TestDescribeBuild_Positive_ReadsTheSharedStamp(t *testing.T) {
	revision := strings.Repeat("a", 40)
	info := &debug.BuildInfo{Main: debug.Module{Path: engineModule}, Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "true"},
	}}
	build := describeBuild(info, "")
	if build.Module != engineModule || build.Revision != revision || !build.Modified {
		t.Fatalf("got %+v", build)
	}
	if got, want := buildLabel(build), buildid.Identify("", info).String(); got != want || got != "aaaaaaaaaaaa-dirty" {
		t.Fatalf("label %q, the build's own identity %q", got, want)
	}
}

// Negative: no build information, or none carrying a VCS stamp, describes no revision.
func TestDescribeBuild_Negative_Unstamped(t *testing.T) {
	if build := describeBuild(nil, ""); build != (Build{}) {
		t.Fatalf("nil info: %+v", build)
	}
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-trimpath", Value: "true"}}}
	if build := describeBuild(info, ""); build.Revision != "" || build.Modified {
		t.Fatalf("unstamped info: %+v", build)
	}
}

// Positive: the running test binary resolves its own executable to an absolute path.
func TestRunningBuild_ResolvesExecutable(t *testing.T) {
	build := RunningBuild()
	if !filepath.IsAbs(build.Executable) || build.ModTime.IsZero() {
		t.Fatalf("executable %q modtime %v", build.Executable, build.ModTime)
	}
}
