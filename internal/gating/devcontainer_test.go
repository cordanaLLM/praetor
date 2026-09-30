// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

var fakeImageID = "sha256:" + strings.Repeat("7", 64)

// containerCall is one command a fake runtime ran inside a container.
type containerCall struct {
	name, workdir string
	args          []string
	runArgs       []string
}

// fakeRuntime stands in for docker: it builds, inspects, runs and removes containers without a
// runtime, and answers the commands run inside them the way the image under test would.
type fakeRuntime struct {
	calls    []containerCall
	host     []recordedCommand
	removed  []string
	built    [][]string      // the build command lines
	released []string        // the image tags removed
	has      map[string]bool // commands on the image's PATH
	failTest error           // what go test fails with inside the container
	failRun  map[string]error
}

func newFakeRuntime(onImagePath ...string) *fakeRuntime {
	has := map[string]bool{}
	for _, name := range onImagePath {
		has[name] = true
	}
	return &fakeRuntime{has: has, failRun: map[string]error{}}
}

func (f *fakeRuntime) run(_ context.Context, dir, name string, args ...string) (string, error) {
	if filepath.Base(name) != "docker" || len(args) == 0 {
		f.host = append(f.host, recordedCommand{dir: dir, name: name, args: args})
		return "", nil
	}
	if err := f.failRun[args[0]]; err != nil {
		return "", err
	}
	switch args[0] {
	case "build":
		f.built = append(f.built, args)
		return "", nil
	case "image":
		if args[1] == "rm" {
			f.released = append(f.released, args[len(args)-1])
			return "", f.failRun["image rm"]
		}
		return fakeImageID + "\n", nil
	case "rm":
		f.removed = append(f.removed, args[len(args)-1])
		return "", nil
	case "run":
		return f.inContainer(args)
	}
	return "", nil
}

// inContainer answers the command a `docker run` line carries after its --workdir and image.
func (f *fakeRuntime) inContainer(args []string) (string, error) {
	at := slices.Index(args, "--workdir")
	if at < 0 || at+3 > len(args) || args[at+2] != fakeImageID {
		return "", errors.New("run line names no workdir and image: " + strings.Join(args, " "))
	}
	call := containerCall{workdir: args[at+1], name: args[at+3], args: args[at+4:], runArgs: args[:at]}
	f.calls = append(f.calls, call)
	switch {
	case call.name == "sh":
		if f.has[call.args[len(call.args)-1]] {
			return "/usr/bin/" + call.args[len(call.args)-1] + "\n", nil
		}
		return "", nil
	case call.name == "go" && len(call.args) == 2 && call.args[0] == "env":
		return map[string]string{"CGO_ENABLED": "1\n", "CC": "gcc\n"}[call.args[1]], nil
	case call.name == "go" && call.args[0] == "test":
		return "FAIL example.test", f.failTest
	}
	return "", nil
}

// inside returns the commands run in containers whose name is name.
func (f *fakeRuntime) inside(name string) []containerCall {
	var out []containerCall
	for _, call := range f.calls {
		if call.name == name {
			out = append(out, call)
		}
	}
	return out
}

// devcontainerRepo returns a Go repository with a Dockerfile devcontainer and lockfiles, and its
// resolved path.
func devcontainerRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := newHermeticGitRepo(t)
	seedGoModule(t, repo)
	writeLockfiles(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".devcontainer", "devcontainer.json"),
		`{"name": "fixture", "build": {"dockerfile": "Dockerfile"}}`)
	writeFile(t, filepath.Join(repo, ".devcontainer", "Dockerfile"), "FROM scratch\n")
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, resolved
}

// isolateUserCache points the user cache directory (os.UserCacheDir) at a temporary directory on
// every platform and returns the resolved devcontainer HOME the gate places below it.
func isolateUserCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	switch hostMachine().GOOS {
	case "windows":
		t.Setenv("LocalAppData", dir)
	case "darwin", "ios":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CACHE_HOME", dir)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(cache, filepath.FromSlash(devcontainerHomeRel))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// devcontainerConfig is a stage configuration for repo on a Linux host with docker on PATH,
// starting every command through runtime, with the user cache directory isolated.
func devcontainerConfig(t *testing.T, repo string, runtime *fakeRuntime) *stageConfig {
	t.Helper()
	isolateUserCache(t)
	t.Setenv(DevcontainerEnv, "")
	t.Setenv("CGO_ENABLED", "")
	cfg, _ := newTestConfig(t, repo, false)
	cfg.goos = "linux"
	cfg.run = runtime.run
	cfg.lookPath = func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New(name + " not found")
	}
	return cfg
}

// Positive: with a devcontainer this host can build, the Go prefetch and race stages run inside
// the built image by its ID, the race stage in the stage worktree mounted at its own path with
// HOME in the user cache directory, and the signed stage output names the image.
func TestGoStagesRunInTheDevcontainer(t *testing.T) {
	repo, resolved := devcontainerRepo(t)
	runtime := newFakeRuntime("go", "gcc")
	cfg := devcontainerConfig(t, repo, runtime)
	home := isolateUserCache(t)

	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	exec := cfg.rep.Execution
	if exec == nil || exec.Environment != executionDevcontainer || exec.ImageID != fakeImageID || exec.Builder != "docker build" {
		t.Fatalf("execution = %+v, want the devcontainer image %s", exec, fakeImageID)
	}
	if _, err := runPrefetchStage(t.Context(), cfg); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if _, err := runTestStage(t.Context(), cfg); err != nil {
		t.Fatalf("race stage: %v", err)
	}
	var sawVerify, sawTest bool
	for _, call := range runtime.inside("go") {
		switch strings.Join(call.args, " ") {
		case "mod verify":
			sawVerify = call.workdir == resolved
		case "test -race -timeout " + TestStageTimeout.String() + " ./...":
			sawTest = strings.HasPrefix(call.workdir, filepath.Join(resolved, ".standards", "worktrees")+string(filepath.Separator))
			if !slices.Contains(call.runArgs, "type=bind,src="+resolved+",dst="+resolved) {
				t.Errorf("the checkout must be mounted at its own path: %q", call.runArgs)
			}
			if !slices.Contains(call.runArgs, "HOME="+home) || !slices.Contains(call.runArgs, "type=bind,src="+home+",dst="+home) {
				t.Errorf("HOME must persist, mounted, in the user cache directory %s: %q", home, call.runArgs)
			}
		}
	}
	if !sawVerify || !sawTest {
		t.Errorf("go mod verify in the checkout (%v) and go test in the worktree (%v) must run in the container: %+v",
			sawVerify, sawTest, runtime.calls)
	}
	for _, host := range runtime.host {
		if host.name == "go" {
			t.Errorf("no go command may run on the host once the devcontainer is entered: %+v", host)
		}
	}
	line := lockdown.ExecutionLine(executionDevcontainer, "runtime=docker", "image="+fakeImageID, "builder=docker build",
		"stages="+stagePrefetch+", "+stageTests+" (go)")
	if output := string(cfg.rep.StageOutput()); !strings.Contains(output, "\n"+line+"\n") {
		t.Errorf("stage output must carry %q:\n%s", line, output)
	}
}

// Negative: without a plan the stages run on the host, nothing reaches a runtime, and the recorded
// reason names why.
func TestExecutionRunsOnTheHostWithAReason(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	cases := []struct {
		name, env, goos string
		dryRun          bool
		prepare         func(t *testing.T)
		want            string
	}{
		{name: "opted out", env: "off", goos: "linux", want: DevcontainerEnv + "=off"},
		{name: "unknown value", env: "of", goos: "linux", want: `"of" is neither auto nor off`},
		{name: "dry run", goos: "linux", dryRun: true, want: "dry run"},
		{name: "windows", goos: "windows", want: "Windows checkout"},
		{name: "no go.mod", goos: "linux", want: "no go.mod", prepare: func(t *testing.T) {
			if err := os.Remove(filepath.Join(repo, "go.mod")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { seedGoModule(t, repo) })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := newFakeRuntime("go")
			cfg := devcontainerConfig(t, repo, runtime)
			t.Setenv(DevcontainerEnv, tc.env)
			cfg.goos, cfg.dryRun = tc.goos, tc.dryRun
			if tc.prepare != nil {
				tc.prepare(t)
			}
			if err := resolveExecution(t.Context(), cfg); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			exec := cfg.rep.Execution
			if exec == nil || exec.Environment != executionHost || !strings.Contains(exec.Reason, tc.want) {
				t.Fatalf("execution = %+v, want the host because %q", exec, tc.want)
			}
			if len(runtime.calls) != 0 || cfg.container != nil {
				t.Errorf("a host run must reach no runtime: %+v", runtime.calls)
			}
			if !strings.Contains(string(cfg.rep.StageOutput()), "execution\thost\treason=") {
				t.Errorf("the host and its reason must be signed:\n%s", cfg.rep.StageOutput())
			}
		})
	}
}

// Boundary: an image that builds but has no go runs the stages on the host and says so, while one
// whose race toolchain lacks the compiler runs them in the image and skips the race tests there,
// naming the devcontainer; the host's CGO_ENABLED does not reach the container.
func TestDevcontainerToolchainBoundaries(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	noGo := devcontainerConfig(t, repo, newFakeRuntime())
	if err := resolveExecution(t.Context(), noGo); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if exec := noGo.rep.Execution; exec.Environment != executionHost || !strings.Contains(exec.Reason, "has no go on its PATH") {
		t.Errorf("an image without go must run on the host and say so: %+v", exec)
	}

	noCC := devcontainerConfig(t, repo, newFakeRuntime("go"))
	if err := resolveExecution(t.Context(), noCC); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Setenv("CGO_ENABLED", "0")
	available, reason := raceDetectorAvailable(t.Context(), noCC)
	if available || reason != `the C compiler "gcc" named by go env is not on PATH in the devcontainer` {
		t.Errorf("race detector in an image without gcc = %v %q", available, reason)
	}
}

// Negative: a planned devcontainer that cannot be built rejects the run through a failed
// Devcontainer Image stage naming the opt-out; nothing falls back to the host.
func TestDevcontainerBuildFailureRejectsTheRun(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go")
	runtime.failRun["build"] = errors.New("pull access denied")
	cfg := devcontainerConfig(t, repo, runtime)

	rep := runPipeline(t.Context(), cfg, time.Now())
	if rep.Status != StatusRejected || len(rep.Stages) != 1 {
		t.Fatalf("a failed build must reject the run before any stage: %+v", rep)
	}
	stage := rep.Stages[0]
	if stage.Name != DevcontainerStage || stage.Status != StageFailed ||
		!strings.Contains(stage.Message, "pull access denied") || !strings.Contains(stage.Message, DevcontainerEnv+"=off") {
		t.Errorf("stage = %+v, want a failed %s naming the cause and the opt-out", stage, DevcontainerStage)
	}
}

// A container whose command failed is removed by the name it ran under, since a killed runtime
// CLI leaves its container running; a directory outside every mount is refused before any run.
func TestDevcontainerRunnerRemovesFailedContainers(t *testing.T) {
	repo, resolved := devcontainerRepo(t)
	runtime := newFakeRuntime("go", "gcc")
	runtime.failTest = errors.New("exit status 1")
	cfg := devcontainerConfig(t, repo, runtime)
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := runTestStage(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "tests failed") {
		t.Fatalf("a failing suite in the container must fail the stage, got %v", err)
	}
	tests := runtime.inside("go")
	last := tests[len(tests)-1]
	name := last.runArgs[slices.Index(last.runArgs, "--name")+1]
	if !slices.Contains(runtime.removed, name) {
		t.Errorf("container %s ran the failing suite but was not removed: %q", name, runtime.removed)
	}

	outside := t.TempDir()
	if _, err := cfg.goToolchain().run(t.Context(), outside, "go", "version"); err == nil ||
		!strings.Contains(err.Error(), "outside the directories the devcontainer mounts") {
		t.Errorf("a directory outside %s must be refused, got %v", resolved, err)
	}
}

// The run deadline reserves the image build exactly when the run would build one: a Go repository
// with a devcontainer and docker on PATH gets the bound, and opting out removes it.
func TestEnvRunBudgetReservesTheImageBuild(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "docker", "package main\n\nfunc main() {}\n")
	t.Setenv("PATH", bin)
	t.Setenv(DevcontainerEnv, "")
	want := devcontainer.ImageBuildTimeout
	if hostMachine().GOOS == "windows" {
		want = 0 // a Windows host runs the stages on the host and builds nothing
	}
	budget := EnvRunBudget(repo)
	if budget.Devcontainer != want || budget.Timeout() != budget.StageBound+budget.Allowance+want {
		t.Errorf("budget = %+v, want %s reserved for the image build", budget, want)
	}
	if want > 0 && !strings.Contains(budget.String(), "to build the devcontainer image") {
		t.Errorf("the deadline must say it includes the image build: %s", budget)
	}
	t.Setenv(DevcontainerEnv, "off")
	if off := EnvRunBudget(repo); off.Devcontainer != 0 || off != stageBudget(repo) {
		t.Errorf("an opted-out run must reserve nothing: %+v", off)
	}
}

// The report form names where the stages ran.
func TestExecutionString(t *testing.T) {
	dc := &Execution{Environment: executionDevcontainer, Runtime: "podman", ImageID: fakeImageID,
		Builder: "devcontainer build", Stages: devcontainerStages}
	if got := dc.String(); !strings.HasPrefix(got, "devcontainer "+fakeImageID+" (podman, devcontainer build)") {
		t.Errorf("devcontainer execution renders as %q", got)
	}
	if got := hostExecution("no go.mod").String(); got != "host: no go.mod" {
		t.Errorf("host execution renders as %q", got)
	}
}

// The pre-push hook gates a fresh clone in a temporary directory. Two clones of one origin, at
// different paths, share the stable image tag and the container HOME, so neither piles up nor
// starts cold per push, while each run reads its image through a per-run tag of its own; a
// repository without an origin shares the local tag, and another origin gets another tag.
func TestImageTagAndHomeOutliveTheCheckout(t *testing.T) {
	first, _ := devcontainerRepo(t)
	second, _ := devcontainerRepo(t)
	other, _ := devcontainerRepo(t)
	noOrigin, _ := devcontainerRepo(t)
	for repo, url := range map[string]string{first: "https://forge.example/acme/app.git", second: "git@forge.example:acme/app.git",
		other: "https://forge.example/acme/other.git"} {
		treeGit(t, repo, "remote", "add", "origin", url)
	}
	a, b := imageRefs(t.Context(), first), imageRefs(t.Context(), second)
	if a.Stable != b.Stable || !strings.HasPrefix(a.Stable, "praetor-gate-") || !strings.HasSuffix(a.Stable, ":latest") {
		t.Errorf("two clones of one origin must share one stable tag: %q, %q", a.Stable, b.Stable)
	}
	if a.Run == b.Run || !strings.HasPrefix(a.Run, strings.TrimSuffix(a.Stable, ":latest")+":run-") {
		t.Errorf("each run needs a per-run tag of its own beside the stable one: %q, %q", a.Run, b.Run)
	}
	if o := imageRefs(t.Context(), other); o.Stable == a.Stable {
		t.Errorf("another origin must get another stable tag: %q", o.Stable)
	}
	local, again := imageRefs(t.Context(), noOrigin), imageRefs(t.Context(), t.TempDir())
	if local.Stable != again.Stable || local.Stable == a.Stable {
		t.Errorf("repositories without an origin share one local tag: %q, %q", local.Stable, again.Stable)
	}

	home := isolateUserCache(t)
	for _, repo := range []string{first, second} {
		resolved, err := filepath.EvalSymlinks(repo)
		if err != nil {
			t.Fatal(err)
		}
		dc, err := newDevcontainerExec(t.Context(), resolved, devcontainer.Image{})
		if err != nil {
			t.Fatalf("enter %s: %v", repo, err)
		}
		if !slices.Contains(dc.env, "HOME="+home) || !slices.Contains(dc.mounts, home) || util.WithinRoot(resolved, home) {
			t.Errorf("HOME must be the user-cache directory %s, mounted, outside the checkout: %q %q", home, dc.env, dc.mounts)
		}
	}
}

// Positive and negative: a run that entered the devcontainer removes its per-run tag once the
// stages are done, whatever they concluded; a removal that fails is reported as the execution's
// cleanup note, outside the signed line, and does not change the verdict.
func TestPipelineReleasesTheRunTag(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go", "gcc")
	cfg := devcontainerConfig(t, repo, runtime)
	rep := runPipeline(t.Context(), cfg, time.Now())
	if len(runtime.built) != 1 || len(runtime.released) != 1 {
		t.Fatalf("one build and one release, got builds %q and releases %q", runtime.built, runtime.released)
	}
	build := runtime.built[0]
	runTag := build[slices.Index(build, "--tag")+3]
	if runtime.released[0] != runTag || !strings.Contains(runTag, ":run-") {
		t.Errorf("the release must remove the per-run tag %s, removed %q", runTag, runtime.released)
	}
	if rep.Execution == nil || rep.Execution.Cleanup != "" {
		t.Errorf("a clean release leaves no cleanup note: %+v", rep.Execution)
	}

	failing := newFakeRuntime("go", "gcc")
	failing.failRun["image rm"] = errors.New("daemon went away")
	cfg = devcontainerConfig(t, repo, failing)
	failed := runPipeline(t.Context(), cfg, time.Now())
	if failed.Status != rep.Status || !strings.Contains(failed.Execution.Cleanup, "daemon went away") {
		t.Errorf("a failed release is a cleanup note, not a verdict: status %s vs %s, %+v", failed.Status, rep.Status, failed.Execution)
	}
	if strings.Contains(string(failed.StageOutput()), "daemon went away") {
		t.Errorf("the cleanup note must stay out of the signed output:\n%s", failed.StageOutput())
	}
	if !strings.Contains(failed.Execution.String(), "; cleanup: ") {
		t.Errorf("the report must print the cleanup note: %s", failed.Execution)
	}
}

// Boundary: an image that was built but not entered, one without go or one whose go probe could
// not run, has its per-run tag removed at once, since no later stage will.
func TestUnenteredImageReleasesItsRunTag(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime()
	cfg := devcontainerConfig(t, repo, runtime)
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.container != nil || len(runtime.released) != 1 || !strings.Contains(runtime.released[0], ":run-") {
		t.Errorf("an image without go must release its run tag at once: container %v, released %q", cfg.container, runtime.released)
	}

	broken := newFakeRuntime("go")
	broken.failRun["run"] = errors.New("OCI runtime create failed")
	broken.failRun["image rm"] = errors.New("image is in use")
	cfg = devcontainerConfig(t, repo, broken)
	err := resolveExecution(t.Context(), cfg)
	if err == nil || !strings.Contains(err.Error(), "OCI runtime create failed") || !strings.Contains(err.Error(), "image is in use") ||
		len(broken.released) != 1 {
		t.Errorf("a failed go probe must reject the run and release the tag, joining a failed release: %v, released %q",
			err, broken.released)
	}
}

// Negative: docker on PATH whose daemon does not answer is no runtime. Nothing is built, the
// stages run on the host with the probe's answer as the reason, and the run deadline reserves no
// image build.
func TestUnansweringRuntimeRunsOnTheHost(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go")
	runtime.failRun["info"] = errors.New("exit status 1: failed to connect to the docker API")
	cfg := devcontainerConfig(t, repo, runtime)
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("a runtime that does not answer must not reject the run: %v", err)
	}
	exec := cfg.rep.Execution
	if exec.Environment != executionHost || !strings.Contains(exec.Reason, "docker is on PATH but `docker info` failed") ||
		len(runtime.built) != 0 {
		t.Errorf("execution = %+v, builds %q; want the host and the probe's answer", exec, runtime.built)
	}

	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "docker", "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(1) }\n")
	t.Setenv("PATH", bin)
	if budget := EnvRunBudget(repo); budget.Devcontainer != 0 {
		t.Errorf("a runtime that does not answer builds nothing, so reserves nothing: %+v", budget)
	}
}
