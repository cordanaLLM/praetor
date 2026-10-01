// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fixtureGitTimeout bounds one fixture git command (HISS-02).
const fixtureGitTimeout = 30 * time.Second

// InitGitRepoWithOrigin makes the existing directory dir a git repository, with an origin
// remote naming origin when origin is not empty, as a clone has. It skips the test when git
// is not installed and fails it when a git command fails. Every command runs through
// util.RunGit under HermeticGitEnv, so no configuration outside the fixture changes the
// result.
func InitGitRepoWithOrigin(t testing.TB, dir, origin string) {
	t.Helper()
	commands := [][]string{{"init", "--quiet"}}
	if origin != "" {
		commands = append(commands, []string{"remote", "add", "origin", origin})
	}
	runFixtureGitCommands(t, dir, commands)
}

// RecordOriginHead points refs/remotes/origin/HEAD of the repository dir at the origin branch
// branch, as git clone records the remote's default branch; the branch itself need not exist.
// It skips the test when git is not installed and fails it when git refuses, under
// HermeticGitEnv like InitGitRepoWithOrigin.
func RecordOriginHead(t testing.TB, dir, branch string) {
	t.Helper()
	runFixtureGitCommands(t, dir, [][]string{{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/" + branch}})
}

// RunFixtureGit runs each of commands in dir the way InitGitRepoWithOrigin runs its own, and
// returns the output of the last one, such as the commit id a closing rev-parse prints. It skips
// the test when git is not installed and fails it at the first command git fails.
func RunFixtureGit(t testing.TB, dir string, commands ...[]string) string {
	t.Helper()
	return runFixtureGitCommands(t, dir, commands)
}

// runFixtureGitCommands runs each of commands in dir through util.RunGit under HermeticGitEnv,
// each bounded by fixtureGitTimeout, and returns the output of the last one. It skips the test
// when git is not installed and fails it at the first command git fails.
func runFixtureGitCommands(t testing.TB, dir string, commands [][]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("testsupport: fixture git environment: %v", err)
	}
	last := ""
	for _, args := range commands {
		runCtx, cancel := context.WithTimeout(ctx, fixtureGitTimeout)
		out, runErr := util.RunGit(runCtx, dir, args...)
		cancel()
		if runErr != nil {
			t.Fatalf("testsupport: git %v in %s: %v: %s", args, dir, runErr, out)
		}
		last = out
	}
	return last
}
