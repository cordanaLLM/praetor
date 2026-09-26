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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("testsupport: fixture git environment: %v", err)
	}
	commands := [][]string{{"init", "--quiet"}}
	if origin != "" {
		commands = append(commands, []string{"remote", "add", "origin", origin})
	}
	for _, args := range commands {
		runCtx, cancel := context.WithTimeout(ctx, fixtureGitTimeout)
		out, runErr := util.RunGit(runCtx, dir, args...)
		cancel()
		if runErr != nil {
			t.Fatalf("testsupport: git %v in %s: %v: %s", args, dir, runErr, out)
		}
	}
}
