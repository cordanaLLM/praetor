// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
// at its own path with its git common dir, as the host user, with HOME in the persistent directory.
// The image is FROM scratch around a stand-in toolchain, so the test pulls nothing.
func TestRaceStageRunsInARealDevcontainer(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test builds and runs a container image")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the stand-in toolchain is built for the host, and only a Linux host runs it inside a Linux container")
	}
	plan, reason := realRuntimePlan(t)
	if plan == "" {
		t.Skip(reason)
	}
	repo, resolved := devcontainerRepo(t)
	t.Setenv("CGO_ENABLED", "0")
	testsupport.BuildExecutable(t, filepath.Join(repo, ".devcontainer"), "toolchain", standInToolchain)
	writeFile(t, filepath.Join(repo, ".devcontainer", "Dockerfile"), "FROM scratch\n"+
		"COPY toolchain /usr/local/bin/go\nCOPY toolchain /usr/local/bin/sh\nCOPY toolchain /usr/local/bin/gcc\n"+
		"ENV PATH=/usr/local/bin\n")
	t.Cleanup(func() { removeImage(t, plan, imageRef(resolved)) })
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
	record, err := os.ReadFile(filepath.Join(resolved, ".git", "praetor", "devcontainer-home", "gate-ran"))
	if err != nil {
		t.Fatalf("the suite left no record in the persistent HOME: %v", err)
	}
	lines := strings.Split(string(record), "\n")
	worktrees := filepath.Join(resolved, ".standards", "worktrees") + string(filepath.Separator)
	if len(lines) < 3 || !strings.HasPrefix(lines[0], worktrees) || lines[1] != "test -race -timeout "+TestStageTimeout.String()+" ./..." ||
		lines[2] != strconv.Itoa(os.Getuid()) {
		t.Errorf("the suite ran as %q; want the stage worktree under %s, go test -race and uid %d", lines, worktrees, os.Getuid())
	}
}

// realRuntimePlan returns the container runtime this host would build with and whose daemon
// answers, or the reason the test cannot run here.
func realRuntimePlan(t *testing.T) (string, string) {
	t.Helper()
	for _, name := range []string{"docker", "podman"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		_, err = util.RunCommand(ctx, t.TempDir(), path, "info")
		cancel()
		if err != nil {
			return "", name + " is on PATH but its daemon does not answer: " + err.Error()
		}
		return path, ""
	}
	return "", "neither docker nor podman is on PATH: the devcontainer path cannot run here"
}

// removeImage removes the image the test built.
func removeImage(t *testing.T, runtimePath, ref string) {
	ctx, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cancel()
	if _, err := util.RunCommand(ctx, t.TempDir(), runtimePath, "image", "rm", "--force", ref); err != nil {
		t.Logf("remove test image %s: %v", ref, err)
	}
}
