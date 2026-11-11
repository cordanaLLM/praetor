// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Real Lefthook must execute the script even when its pushed-file estimate is empty.
func TestLefthookCommitPolicy_Negative_EmptyDiffStillRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX recording stubs required; generated hooks run through sh on Windows")
	}
	lefthook, err := exec.LookPath("lefthook")
	if err != nil {
		t.Skip("real lefthook required")
	}
	repo, stubs := t.TempDir(), t.TempDir()
	log := filepath.Join(stubs, "calls.log")
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ctx, err = util.WithCommandEnvironment(ctx, testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "--allow-empty", "-qm", "chore: initial"}} {
		if _, err := util.RunGit(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(repo, lefthookFile), buildLefthookYAML())
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(engineLauncherFile)), engineLauncherScript)
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(commitPolicyScriptFile)), buildCommitPolicyScript())
	for _, code := range []int{0, 19} {
		recordingStub(t, stubs, util.PraetorCLI, log, code)
		out, err := util.RunCommand(ctx, repo, lefthook, "run", "pre-push", "--job", commitPolicyScriptName, "--no-auto-install")
		if (err == nil) != (code == 0) {
			t.Fatalf("empty-diff script exit with policy code %d: %v\n%s", code, err, out)
		}
	}
	want := util.PraetorCLI + " forge check-commits --pre-push\n"
	if got := mustRead(t, log); got != want+want {
		t.Fatalf("empty-diff script calls = %q", got)
	}
}
