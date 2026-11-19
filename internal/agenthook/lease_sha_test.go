// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// leaseGit runs fixture-only Git with inherited Git variables dropped and config isolated.
func leaseGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// leaseRepository creates one local commit without signing, hooks or network activity.
func leaseRepository(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	leaseGit(t, dir, "init", "-q")
	tree := leaseGit(t, dir, "mktree")
	sha := leaseGit(t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit-tree", tree, "-m", "fixture")
	leaseGit(t, dir, "update-ref", "HEAD", sha)
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, sha
}

// TestPolicyLeaseSHAPlanted checks decisions and hook exit codes for fabricated commit names.
func TestPolicyLeaseSHAPlanted(t *testing.T) {
	dir, real := leaseRepository(t)
	bad := strings.Repeat("a", 40)
	if real[0] == 'a' {
		bad = strings.Repeat("b", 40)
	}
	for _, command := range []string{
		"git push --force-with-lease=refs/heads/main:" + bad,
		"git push --force-with-lease refs/heads/main:" + bad,
		"bash scripts/squash_merge.sh 12 " + bad,
		"scripts/squash_merge.sh 12 " + bad,
		"gh pr merge 12 --match-head-commit " + bad,
		"gh pr merge --match-head-commit=" + bad + " 12",
		"gh run list --commit " + bad,
		"gh run list --commit=" + bad,
		"gh api repos/o/r/commits/" + bad,
		"gh api -X GET --header Accept:application/json repos/o/r/commits/" + bad,
		"gh api --method=GET repos/o/r/git/commits/" + bad + "/comments",
		"gh api repos/o/r/compare/" + real + "..." + bad,
		"git log " + bad,
		"git show '" + bad + "'",
	} {
		t.Run(command, func(t *testing.T) {
			verdict := policy(t).WithWorkDir(dir).Command(command)
			assertLeaseSHARefusal(t, verdict, bad, dir)
			assertLeaseHook(t, dir, command, false)
		})
	}
}

// assertLeaseSHARefusal requires the denial to name both the commit and its repository.
func assertLeaseSHARefusal(t *testing.T, verdict Verdict, sha, dir string) {
	t.Helper()
	repository := "repository " + strconv.Quote(dir)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, sha) || !strings.Contains(verdict.Reason, repository) {
		t.Fatalf("commit resolution refusal missing or diagnostic incomplete: %+v", verdict)
	}
}

// TestPolicyLeaseSHAPlantedWindowsPath exercises escaped repository diagnostics on every OS.
func TestPolicyLeaseSHAPlantedWindowsPath(t *testing.T) {
	dir := `C:\Users\runner\AppData\Local\Temp\repository`
	bad := strings.Repeat("a", 40)
	// Cancellation preserves the real refusal formatter without accessing this synthetic path.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	verdict := policy(t).CommandIn(ctx, "git show "+bad, dir)
	assertLeaseSHARefusal(t, verdict, bad, dir)
}

// assertLeaseHook checks the pre-tool protocol as well as the policy's decision.
func assertLeaseHook(t *testing.T, dir, command string, allow bool) {
	t.Helper()
	response := Run(t.Context(), Invocation{
		Client: "lefthook", Event: "pre-tool", WorkDir: dir, Policy: policy(t), Getenv: noEnvironment,
		Stdin: bytes.NewReader(commandPayload(t, map[string]any{"tool_input": map[string]string{"command": command}})),
	})
	wantExit := 1
	if allow {
		wantExit = 0
	}
	marker := bytes.Equal(response.Stdout, []byte(CommandPolicyMarker+"\n"))
	if response.ExitCode != wantExit || marker != allow {
		t.Fatalf("allow %v, response %s", allow, response.String())
	}
}

// TestPolicyLeaseSHAPositiveAndBoundary preserves runtime expansions and read prefixes.
func TestPolicyLeaseSHAPositiveAndBoundary(t *testing.T) {
	dir, sha := leaseRepository(t)
	for _, value := range []string{sha, sha[:12], sha[:7], "$old", "$(git rev-parse HEAD)", "${old}"} {
		for _, command := range []string{
			"git push --force-with-lease=main:" + value,
			"scripts/squash_merge.sh 1 " + value,
			"gh pr merge 1 --match-head-commit=" + value,
		} {
			if verdict := policy(t).WithWorkDir(dir).Command(command); verdict.Outcome != Allow {
				t.Errorf("%s: %+v", command, verdict)
			}
		}
	}
	for _, command := range []string{
		"git push --force-with-lease", "git push --force-with-lease=main", "git push --force-with-lease main",
		"git status", "git show " + sha, "git log " + sha,
		"gh run list --commit=" + sha, "gh api repos/o/r/commits/" + sha,
		"gh api --method GET repos/o/r/compare/" + sha + "..." + sha,
		"git show " + strings.Repeat("f", 39), "git log " + strings.Repeat("f", 12),
		"gh run list --commit " + strings.Repeat("f", 39), "gh api repos/o/r/commits/" + strings.Repeat("f", 12),
		"git push --force-with-lease=main:" + strings.Repeat("f", 6),
		"git push --force-with-lease=main:" + strings.Repeat("f", 41),
		"echo git show " + strings.Repeat("f", 40),
		"gh pr view 1 --json headRefOid", "git show -- " + strings.Repeat("f", 40),
		"gh api -H '" + strings.Repeat("f", 40) + "' repos/o/r/commits",
		"git push '--force-with-lease=main:" + sha + "'",
	} {
		if verdict := policy(t).WithWorkDir(dir).Command(command); verdict.Outcome != Allow {
			t.Errorf("%s: %+v", command, verdict)
		}
	}
	assertLeaseHook(t, dir, "git push --force-with-lease=main:"+sha, true)
}

// TestPolicyLeaseSHARepositorySelection checks explicit targets and failures without fallback.
func TestPolicyLeaseSHARepositorySelection(t *testing.T) {
	dir, sha := leaseRepository(t)
	base := t.TempDir()
	for _, command := range []string{
		fmt.Sprintf("cd %q && git push --force-with-lease=main:%s", dir, sha),
		fmt.Sprintf("git -C %q push --force-with-lease=main:%s", dir, sha),
		fmt.Sprintf("cd %q && gh run list --commit %s", dir, sha),
		fmt.Sprintf("git -C %q -C . show %s", dir, sha),
		fmt.Sprintf("(cd %q && echo fixture); git -C %q show %s", base, dir, sha),
	} {
		if verdict := policy(t).CommandIn(t.Context(), command, base); verdict.Outcome != Allow {
			t.Errorf("target repository ignored: %s: %+v", command, verdict)
		}
	}
	for _, target := range []string{base, filepath.Join(base, "missing"), "$unknown"} {
		command := fmt.Sprintf("cd %q && git push --force-with-lease=main:%s", target, sha)
		verdict := policy(t).CommandIn(t.Context(), command, dir)
		assertLeaseSHARefusal(t, verdict, sha, target)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	verdict := policy(t).CommandIn(ctx, "git push --force-with-lease=main:"+sha, dir)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "deadline exceeded") {
		t.Errorf("git timeout accepted or reason lost: %+v", verdict)
	}
}

// TestPolicyLeaseSHABoundaryLength pins both ends of the literal-only lease check.
func TestPolicyLeaseSHABoundaryLength(t *testing.T) {
	dir, real := leaseRepository(t)
	digit := "f"
	if real[0] == 'f' {
		digit = "e"
	}
	for _, tc := range []struct {
		digits int
		deny   bool
	}{{6, false}, {7, true}, {40, true}, {41, false}} {
		command := "git push --force-with-lease=main:" + strings.Repeat(digit, tc.digits)
		verdict := policy(t).CommandIn(t.Context(), command, dir)
		if (verdict.Outcome == Deny) != tc.deny {
			t.Errorf("%d hex digits: %+v", tc.digits, verdict)
		}
	}
}

// TestPolicyLeaseSHAGitFailureAndTimeout exercises failed startup and an actually stalled Git.
func TestPolicyLeaseSHAGitFailureAndTimeout(t *testing.T) {
	dir, sha := leaseRepository(t)
	stubDir := t.TempDir()
	testsupport.BuildExecutable(t, stubDir, "git", `package main
import "time"
func main() { time.Sleep(30 * time.Second) }
`)
	t.Setenv("PATH", stubDir)
	verdict := policy(t).CommandIn(t.Context(), "git push --force-with-lease=main:"+sha, dir)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "deadline exceeded") {
		t.Fatalf("stalled Git accepted or timeout lost: %+v", verdict)
	}
	t.Setenv("PATH", t.TempDir())
	verdict = policy(t).CommandIn(t.Context(), "git show "+sha, dir)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "executable file not found") {
		t.Fatalf("missing Git accepted or reason lost: %+v", verdict)
	}
}

// TestLiteralCommitSHAParseAllows preserves shell errors without a SHA-bearing form and hex run.
func TestLiteralCommitSHAParseAllows(t *testing.T) {
	bad := strings.Repeat("a", 40)
	for _, command := range []string{
		`echo "high`,
		`echo "length ` + bad,
		`gh issue create --title "unclosed`,
		`git status "` + bad,
		`git push --force-with-lease=main:aaaaaa "`,
		`gh run list --commit "$runtime`,
		`echo --commitment ` + bad + ` "`,
		`echo not--force-with-lease ` + bad + ` "`,
		`echo squash_merge.sh.bak ` + bad + ` "`,
		`legit show ` + bad + ` "`,
		`git logger ` + bad + ` "`,
		`git-show ` + bad + ` "`,
		`git show-more ` + bad + ` "`,
		`high api repos/o/r/commits/` + bad + ` "`,
		`gh api repos/o/r/notcommits/` + bad + ` "`,
	} {
		t.Run(command, func(t *testing.T) {
			if verdict := judgeLiteralCommitSHAs(t.Context(), command, t.TempDir()); verdict.Outcome != Allow {
				t.Fatalf("unrelated parse error refused: %+v", verdict)
			}
		})
	}
}

// TestLiteralCommitSHAParseRefuses retains fail-closed parsing for every SHA-bearing form.
func TestLiteralCommitSHAParseRefuses(t *testing.T) {
	bad := strings.Repeat("a", 40)
	for _, command := range []string{
		"git push --force-with-lease=main:" + bad,
		"scripts/squash_merge.sh 12 " + bad,
		"gh pr merge 12 --match-head-commit=" + bad,
		"gh run list --commit=" + bad,
		"gh api repos/o/r/commits/" + bad,
		"gh api commits/" + bad,
		"gh --hostname example.test api repos/o/r/commits/" + bad,
		"git log " + bad,
		"git show " + bad,
		"git -C missing show " + bad,
		"gh run list --commit=" + bad[:7],
	} {
		t.Run(command, func(t *testing.T) {
			verdict := judgeLiteralCommitSHAs(t.Context(), command+` "`, t.TempDir())
			if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "literal commit SHA check: command parse:") {
				t.Fatalf("SHA-bearing parse error accepted or reason lost: %+v", verdict)
			}
		})
	}
}

// TestPolicyLeaseSHAForgeRefusal preserves forge-text refusals even alongside fabricated SHAs.
func TestPolicyLeaseSHAForgeRefusal(t *testing.T) {
	dir, _ := leaseRepository(t)
	bad := strings.Repeat("a", 40)
	for _, command := range []string{
		`gh issue create --title "unclosed --commit ` + bad,
		`gh issue create --title 'outside-org/project#42'; gh run list --commit=` + bad,
	} {
		assertForgeTextDenied(t, runPreToolCommand(t, dir, command, nil), "")
	}
}

// TestPolicyLeaseSHAExistingRefusals preserves each existing policy rule's complete refusal.
func TestPolicyLeaseSHAExistingRefusals(t *testing.T) {
	command := "git push --force-with-lease=main:" + strings.Repeat("a", 40) + ` "`
	for _, tc := range []struct {
		name    string
		policy  *Policy
		command string
		want    string
	}{
		{"builtin", policy(t), command + " --no-verify", "verification evasion"},
		{"operator", policy(t, "--force-with-lease"), command, "operator"},
		{"admission", &Policy{admission: &admissionCheck{}}, command, "heavy-step check:"},
		{"worktree", &Policy{worktrees: &worktreeCheck{}}, command, "git worktree add check:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			want := tc.policy.commandRulesIn(t.Context(), tc.command, dir)
			if want.Outcome != Deny || !strings.Contains(want.Reason, tc.want) {
				t.Fatalf("fixture did not trigger existing rule: %+v", want)
			}
			got := tc.policy.CommandIn(t.Context(), tc.command, dir)
			if got.Outcome != want.Outcome || got.Reason != want.Reason || got.Notice != want.Notice {
				t.Fatalf("existing verdict changed: got %+v, want %+v", got, want)
			}
		})
	}
}

// TestLiteralCommitHereDocs inspects commit operands only in executing bodies.
func TestLiteralCommitHereDocs(t *testing.T) {
	dir, _ := leaseRepository(t)
	checkAgentHereDocReader(t, "git show "+strings.Repeat("a", 40), func(script string) bool {
		verdict := judgeLiteralCommitSHAs(t.Context(), script, dir)
		return verdict.Outcome == Deny && strings.Contains(verdict.Reason, "local commit resolution failed")
	})
}
