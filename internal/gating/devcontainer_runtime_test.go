// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// standInToolchain is a static program installed in a FROM scratch image as go, sh and gcc. As sh
// it answers the gate's PATH probe; as go it answers go env and, for go test, records the working
// directory, the arguments and the user it ran as in $HOME, after proving that the worktree's
// gitdir link resolves inside the container.
const standInToolchain = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]
	switch filepath.Base(os.Args[0]) {
	case "sh":
		name := args[len(args)-1]
		if _, err := os.Stat("/usr/local/bin/" + name); err == nil {
			fmt.Println("/usr/local/bin/" + name)
		}
	case "go":
		if len(args) == 2 && args[0] == "env" {
			fmt.Println(map[string]string{"CGO_ENABLED": "1", "CC": "gcc"}[args[1]])
			return
		}
		if err := test(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}
}

func test(args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	link, err := os.ReadFile(filepath.Join(wd, ".git"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(strings.TrimSpace(strings.TrimPrefix(string(link), "gitdir:"))); err != nil {
		return err
	}
	record := fmt.Sprintf("%s\n%s\n%d\n", wd, strings.Join(args, " "), os.Getuid())
	return os.WriteFile(filepath.Join(os.Getenv("HOME"), "gate-ran"), []byte(record), 0o600)
}
`

// The race stage end to end through a real container runtime: the gate builds the repository's
// devcontainer from its Dockerfile, finds go in it, and runs the suite in the stage worktree, mounted
// at its own path with its git common dir, as the host user, with HOME in the persistent user-cache
// directory. Releasing the run removes its per-run tag and keeps the stable one. The image is FROM
// scratch around a stand-in toolchain, so the test pulls nothing.
func TestRaceStageRunsInARealDevcontainer(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test builds and runs a container image")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the stand-in toolchain is built for the host, and only a Linux host runs it inside a Linux container")
	}
	rt, reason := realRuntime(t)
	if rt.Path == "" {
		t.Skip(reason)
	}
	repo, resolved := devcontainerRepo(t)
	// A unique origin gives the test a stable tag of its own, which its cleanup removes.
	treeGit(t, repo, "remote", "add", "origin", "https://forge.example/praetor-test/"+filepath.Base(resolved)+".git")
	home := isolateUserCache(t)
	t.Setenv("CGO_ENABLED", "0")
	testsupport.BuildExecutable(t, filepath.Join(repo, ".devcontainer"), "toolchain", standInToolchain)
	writeFile(t, filepath.Join(repo, ".devcontainer", "Dockerfile"), "FROM scratch\n"+
		"COPY toolchain /usr/local/bin/go\nCOPY toolchain /usr/local/bin/sh\nCOPY toolchain /usr/local/bin/gcc\n"+
		"ENV PATH=/usr/local/bin\n")
	// Named now: t.Context is already canceled when cleanups run, and the origin cannot be read then.
	stable := imageRefs(t.Context(), resolved).Stable
	t.Cleanup(func() { removeImage(t, rt.Path, stable) })
	t.Setenv(DevcontainerEnv, "")

	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := newStageConfig(repo, false, rep)
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("resolve: %v (%+v)", err, rep.Stages)
	}
	if rep.Execution.Environment != executionDevcontainer || !strings.HasPrefix(rep.Execution.ImageID, "sha256:") {
		t.Fatalf("execution = %+v, want the devcontainer", rep.Execution)
	}
	if _, err := runTestStage(t.Context(), cfg); err != nil {
		t.Fatalf("race stage in the devcontainer: %v", err)
	}
	record, err := os.ReadFile(filepath.Join(home, "gate-ran"))
	if err != nil {
		t.Fatalf("the suite left no record in the persistent HOME %s: %v", home, err)
	}
	lines := strings.Split(string(record), "\n")
	worktrees := filepath.Join(resolved, ".standards", "worktrees") + string(filepath.Separator)
	if len(lines) < 3 || !strings.HasPrefix(lines[0], worktrees) || lines[1] != "test -race -timeout "+TestStageTimeout.String()+" ./..." ||
		lines[2] != strconv.Itoa(os.Getuid()) {
		t.Errorf("the suite ran as %q; want the stage worktree under %s, go test -race and uid %d", lines, worktrees, os.Getuid())
	}

	image := cfg.container.image
	releaseDevcontainer(t.Context(), cfg)
	if rep.Execution.Cleanup != "" {
		t.Fatalf("release: %s", rep.Execution.Cleanup)
	}
	if _, err := util.RunCommand(t.Context(), resolved, rt.Path, "image", "inspect", image.RunRef); err == nil {
		t.Errorf("the per-run tag %s must be gone after the release", image.RunRef)
	}
	if _, err := util.RunCommand(t.Context(), resolved, rt.Path, "image", "inspect", image.Ref); err != nil {
		t.Errorf("the stable tag %s must survive the release: %v", image.Ref, err)
	}
}

// praetorDevcontainerE2EEnv opts into TestPraetorsOwnDevcontainerRunsTheGoStages, which fetches
// the pinned Node and devcontainer CLI, pulls the base images and installs the features: minutes
// and a few hundred megabytes on a first run.
const praetorDevcontainerE2EEnv = "PRAETOR_DEVCONTAINER_E2E"

// The case that motivated #652, end to end: Praetor's own devcontainer declares the common-utils,
// go and node features, so only the devcontainer CLI builds it as declared. The gate fetches the
// pinned Node and CLI into the user's tool cache, builds the image with the real runtime, and runs
// the prefetch stage and a race-detector test in it, with the node feature's Node 24 on the
// container's PATH. It uses the real user cache directory, as the gate does, and removes only its
// per-run tag.
func TestPraetorsOwnDevcontainerRunsTheGoStages(t *testing.T) {
	if os.Getenv(praetorDevcontainerE2EEnv) != "1" {
		t.Skipf("set %s=1 to build Praetor's own devcontainer with the pinned CLI and run the Go stages in it", praetorDevcontainerE2EEnv)
	}
	if rt, reason := realRuntime(t); rt.Path == "" {
		t.Skip(reason)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(DevcontainerEnv, "")
	ctx, cancel := context.WithTimeout(t.Context(), devcontainer.ImageBuildTimeout+DefaultPrefetchTimeout+TestStageTimeout)
	defer cancel()
	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := newStageConfig(repo, false, rep)
	if err := resolveExecution(ctx, cfg); err != nil {
		t.Fatalf("resolve: %v (%+v)", err, rep.Stages)
	}
	t.Cleanup(func() { releaseDevcontainer(context.Background(), cfg) })
	pins, err := devcontainer.LoadCLIPins()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Execution.Environment != executionDevcontainer || !strings.Contains(rep.Execution.Builder, "@devcontainers/cli "+pins.CLIVersion) {
		t.Fatalf("execution = %+v, want praetor's devcontainer built by the pinned CLI", rep.Execution)
	}
	t.Logf("execution: %s", rep.Execution)
	if _, err := runPrefetchStage(ctx, cfg); err != nil {
		t.Fatalf("prefetch in the devcontainer: %v", err)
	}
	run := cfg.goToolchain().run
	// The util tests that wait for a killed process group to disappear pass only where an init
	// reaps orphans, which the gate's --init provides (Image.RunArgs).
	for _, command := range [][]string{{"go", "version"}, {"node", "--version"}, {"go", "test", "-race", "-count=1", "./internal/lockdown"},
		{"go", "test", "-race", "-count=1", "-run", "StubbornCommandKilled", "./internal/util"}} {
		out, err := run(ctx, cfg.repoDir, command[0], command[1:]...)
		if err != nil {
			t.Fatalf("%s in the devcontainer: %v\n%s", strings.Join(command, " "), err, out)
		}
		t.Logf("%s: %s", strings.Join(command, " "), util.TruncateExcerpt(out, 400))
		if command[0] == "node" && !strings.HasPrefix(out, "v24.") {
			t.Errorf("the node feature must put Node 24 on the container's PATH, got %q", out)
		}
	}
}

// realRuntime returns the container runtime this host would build with, asked the way the gate asks
// (devcontainer.FindRuntime), or the reason the test cannot run here.
func realRuntime(t *testing.T) (devcontainer.Runtime, string) {
	t.Helper()
	rt, err := devcontainer.FindRuntime(t.Context(), t.TempDir(), hostMachine())
	if err != nil {
		return devcontainer.Runtime{}, err.Error()
	}
	return rt, ""
}

// removeImage removes the image the test built.
func removeImage(t *testing.T, runtimePath, ref string) {
	ctx, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cancel()
	if _, err := util.RunCommand(ctx, t.TempDir(), runtimePath, "image", "rm", "--force", ref); err != nil {
		t.Logf("remove test image %s: %v", ref, err)
	}
}
