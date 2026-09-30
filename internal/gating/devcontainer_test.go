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
	case "image":
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

// devcontainerConfig is a stage configuration for repo on a Linux host with docker on PATH,
// starting every command through runtime.
func devcontainerConfig(t *testing.T, repo string, runtime *fakeRuntime) *stageConfig {
	t.Helper()
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
// the built image by its ID, the race stage in the stage worktree mounted at its own path, and
// the signed stage output names the image.
func TestGoStagesRunInTheDevcontainer(t *testing.T) {
	repo, resolved := devcontainerRepo(t)
	runtime := newFakeRuntime("go", "gcc")
	cfg := devcontainerConfig(t, repo, runtime)

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
			if !slices.Contains(call.runArgs, "HOME="+filepath.Join(resolved, ".git", "praetor", "devcontainer-home")) {
				t.Errorf("HOME must persist below the git common dir: %q", call.runArgs)
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
