// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const holdTestReason = "engine-test-order: quota hold until reviewed"

// TestHolds_Coproc refuses simple and named compound coprocesses only during a hold.
func TestHolds_Coproc(t *testing.T) {
	checkHoldSpellings(t, []string{
		`coproc agy models`, `bash -c 'coproc agy models'`,
		`coproc { agy models; }`, `coproc WORKER { agy models; }`,
		`bash -c 'coproc WORKER (agy models)'`,
	})
}

// TestHolds_ShellOptionTerminator covers options, --, missing scripts and nested payloads.
func TestHolds_ShellOptionTerminator(t *testing.T) {
	checkHoldSpellings(t, []string{
		`bash -c -- 'agy models'`, `bash -xc -- 'agy models'`,
		`bash -c -x -- 'agy models'`, `bash --norc -c -- 'agy models'`,
		`sh -c -- 'agy models'`, `bash -c -- "agy models" arg0`,
		`bash -c --`, `bash -c -x --`, `bash -c -- "$SCRIPT"`,
		`bash -c -- 'sh -c -- "agy models"'`,
	})
}

// TestHolds_XargsCommandBoundary refuses stdin-supplied command words after wrapper chains.
func TestHolds_XargsCommandBoundary(t *testing.T) {
	checkHoldSpellings(t, []string{
		`xargs`, `xargs nice`, `xargs env`, `xargs env nice`,
		`xargs -r env --`, `xargs timeout 10 env`, `env xargs nice`,
		`xargs nice agy models`, `xargs env agy models`,
	})
}

// TestHolds_XargsReplacement refuses scripts or command words replaced from stdin.
func TestHolds_XargsReplacement(t *testing.T) {
	checkHoldSpellings(t, []string{
		`xargs -I X sh -c X`, `xargs -IX sh -c X`, `xargs -0IX sh -c X`,
		`xargs -iX sh -c X`, `xargs -i sh -c '{}'`,
		`xargs --replace=X sh -c X`, `xargs --replace sh -c '{}'`,
		`xargs -I X echo X`,
	})
}

// TestHolds_CommentContinuation keeps commands following physical comment lines visible.
func TestHolds_CommentContinuation(t *testing.T) {
	checkHoldSpellings(t, []string{
		"# note \\\nagy models", "echo ok # note \\\nagy models",
		"echo ok # note \\\r\nagy models", "# note \\\n# note \\\nagy models",
		"bash -c 'echo ok # note \\\nagy models'",
		"echo \\# word # note \\\nagy models",
		"echo '# quoted' # note \\\nagy models",
		"echo \"# quoted\" # note \\\nagy models",
		"cat <<'EOF'\n'\nEOF\n# note \\\nagy models",
	})
}

// checkHoldSpellings checks planted bypasses with an absent, active and unconfigured hold.
func checkHoldSpellings(t *testing.T, commands []string) {
	t.Helper()
	for _, command := range commands {
		// Keep shell metacharacters in subtest names out of the expanded hold-file path.
		policy, file := holdFixture(t)
		t.Run(command, func(t *testing.T) {
			if verdict := policy.Command(command); verdict.Outcome != Allow {
				t.Fatalf("absent hold: %+v", verdict)
			}
			writeHoldFixture(t, file, holdTestReason)
			if verdict := policy.Command(command); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) {
				t.Fatalf("active hold: %+v", verdict)
			}
			unset, err := BuildPolicy(config.HookSettings{})
			if err != nil {
				t.Fatal(err)
			}
			if verdict := unset.Command(command); verdict.Outcome != Allow {
				t.Fatalf("unconfigured hold: %+v", verdict)
			}
		})
	}
}

// TestHolds_ReviewRoundThreeAllowed preserves literal programs and wrappers outside xargs.
func TestHolds_ReviewRoundThreeAllowed(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, command := range []string{
		`nice`, `env`, `env nice`, `xargs nice echo safe`, `xargs env echo safe`,
		`bash -c -- 'echo safe'`, `bash -c -x -- 'echo safe'`,
		`coproc echo safe`, `coproc WORKER { echo safe; }`,
		"echo ok # agy models \\\n", "echo '# agy models'",
	} {
		t.Run(command, func(t *testing.T) {
			if verdict := policy.Command(command); verdict.Outcome != Allow {
				t.Fatalf("active hold: %+v", verdict)
			}
		})
	}
}

// TestHolds_JoinLineContinuations checks shared joining across quotes, escapes and comments.
func TestHolds_JoinLineContinuations(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"\\", "\\"}, {"a\\\ngy", "agy"}, {"a\\\r\ngy", "agy"},
		{"# note \\\nagy", "# note \\\nagy"},
		{"echo \\#\\\nword", "echo \\#word"},
		{"echo \\ #\\\nword", "echo \\ #word"},
		{"echo '#\\\nword'", "echo '#\\\nword'"},
		{"echo \"#\\\nword\"", "echo \"#word\""},
		{"echo \\\\\nagy", "echo \\\\\nagy"},
		{"echo \"$(echo ok # note \\\nagy)\"", "echo \"$(echo ok # note \\\nagy)\""},
		{"cat <<'EOF'\n'\\\nEOF\na\\\ngy", "cat <<'EOF'\n'\\\nEOF\nagy"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := joinLineContinuations(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHolds_StalledReadSlot bounds reader goroutines across many expired policy checks.
func TestHolds_StalledReadSlot(t *testing.T) {
	policy, _ := holdFixture(t)
	release, started := make(chan struct{}), make(chan struct{}, 64)
	var calls atomic.Int32
	var readers sync.WaitGroup
	policy.holds.read = func(ctx context.Context, file string) ([]byte, error) {
		return readHoldWith(ctx, file, func(context.Context, string) ([]byte, error) {
			readers.Add(1)
			defer readers.Done()
			calls.Add(1)
			started <- struct{}{}
			<-release
			return nil, os.ErrNotExist
		})
	}
	t.Cleanup(func() { close(release); readers.Wait() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan Verdict, 1)
	go func() { done <- policy.CommandIn(ctx, "agy models", "") }()
	<-started
	cancel()
	if verdict := <-done; verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "context canceled") {
		t.Fatalf("first canceled check: %+v", verdict)
	}
	baseline := runtime.NumGoroutine()
	for i := 0; i < 32; i++ {
		checkStalledHold(t, policy)
	}
	growth := runtime.NumGoroutine() - baseline
	if calls.Load() != 1 || growth > 1 {
		t.Fatalf("reader starts %d; goroutine growth %d; want one reader and no accumulating goroutines", calls.Load(), growth)
	}
}

// checkStalledHold verifies one expired check names the occupied read slot.
func checkStalledHold(t *testing.T, policy *Policy) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	verdict := policy.CommandIn(ctx, "agy models", "")
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "stalled hold read slot") {
		t.Errorf("stalled check: %+v", verdict)
	}
}

// holdFixture returns a policy and temporary hold path for guarded program tests.
func holdFixture(t *testing.T) (*Policy, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "hold")
	policy, err := BuildPolicy(config.HookSettings{Holds: []config.HoldSettings{{
		File: file, Commands: []string{"agy", "praetor-agy-wrapper"}, Via: []string{"agy-run.sh"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return policy, file
}

// TestHolds_ConfiguredNamesAndWrapperVia checks normalized names and launcher precedence.
func TestHolds_ConfiguredNamesAndWrapperVia(t *testing.T) {
	for _, names := range []struct{ commands, via []string }{
		{[]string{"agy.exe"}, []string{"run.sh.exe"}},
		{[]string{"agy"}, []string{"env"}},
	} {
		file := filepath.Join(t.TempDir(), "hold")
		policy, err := BuildPolicy(config.HookSettings{Holds: []config.HoldSettings{{File: file, Commands: names.commands, Via: names.via}}})
		if err != nil {
			t.Fatal(err)
		}
		writeHoldFixture(t, file, holdTestReason)
		for _, command := range []string{"agy models", "agy.exe models"} {
			if verdict := policy.Command(command); verdict.Outcome != Deny {
				t.Fatalf("%s: %+v", command, verdict)
			}
		}
		launcher := commandName(names.via[0]) + " agy models"
		if verdict := policy.Command(launcher); verdict.Outcome != Allow {
			t.Fatalf("%s: %+v", launcher, verdict)
		}
	}
}

// TestHolds_PayloadAndParserBounds checks bounded nesting, malformed input and inactive holds.
func TestHolds_PayloadAndParserBounds(t *testing.T) {
	policy, file := holdFixture(t)
	commands := []string{`bash -c`, `bash -c "$PAYLOAD"`, `eval agy models`, `"unterminated`, `>`, `env --argv0`,
		`env -S 'agy models'`, `env --split-string='agy models'`, `sudo -s 'agy models'`, `a{g,g}y models`,
		`bash -c 'echo ok; agy models'`, `sh -c 'bash -c "agy models"'`, `env -aagy agy models`,
		`env --argv0=other agy models`, `sudo -rrole agy models`, `sudo --role=role agy models`,
		`env "X=1" agy models`, `env X\=1 agy models`, `bash -o errexit -c 'agy models'`,
		`bash --rcfile file -c 'agy models'`, `bash +O extglob -c 'agy models'`,
		`bash`, `bash -l`, `bash --`, `bash -- "$SCRIPT"`, `xargs -e agy models`,
		`xargs -i agy models`, `xargs -l agy models`, `env --unknown value echo unrelated`,
		`chrt agy models`, `chrt -b agy models`,
		`>"out file" "agy" models`, `agy<in`, `2>&1<in agy models`, `a'g'y models`, `a\g\y models`}
	nested := "echo ok"
	for i := 0; i < 5; i++ {
		nested = "eval " + util.QuotePOSIXShellArg(nested)
	}
	commands = append(commands, nested)
	writeHoldFixture(t, file, holdTestReason)
	for _, command := range commands {
		if verdict := policy.Command(command); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) {
			t.Errorf("%s: %+v", command, verdict)
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if verdict := policy.holds.judge(t.Context(), command, ""); verdict.Outcome != Allow {
			t.Errorf("absent %s: %+v", command, verdict)
		}
	}
}

// TestHolds_IndeterminateNamesAndLookup asserts useful diagnostics and literal-only lookups.
func TestHolds_IndeterminateNamesAndLookup(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, word := range []string{"$PROGRAM", "${PROGRAM}", "$(printf agy)", "a*y", "a?y", "a[g]y"} {
		verdict := policy.Command(word + " models")
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, word) {
			t.Errorf("word %s: %+v", word, verdict)
		}
	}
	for _, command := range []string{`bash -c 'command -v agy'`, `bash -c 'echo unrelated'`, `eval 'which agy'`,
		`env --block-signal=INT command -v agy`, `env -i "X=1" echo unrelated`, `sudo -r role echo unrelated`,
		`command -pv agy`, `command -p -V agy`, `bash -- /path/agy-run.sh agy models`, `chrt 0 echo unrelated`} {
		if verdict := policy.holds.judge(t.Context(), command, ""); verdict.Outcome != Allow {
			t.Errorf("lookup/unrelated %s: %+v", command, verdict)
		}
	}
}

// TestHolds_WrapperValueArity checks every supported env/sudo value option against both targets.
func TestHolds_WrapperValueArity(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for wrapper, options := range map[string][]string{
		"env": {"-a", "--argv0", "-u", "--unset", "-C", "--chdir", "--env0-from"},
		"sudo": {"-u", "--user", "-g", "--group", "-h", "--host", "-p", "--prompt", "-r", "--role",
			"-t", "--type", "-C", "--close-from", "-D", "--chdir", "-R", "--chroot", "-T", "--command-timeout",
			"-U", "--other-user", "-a", "--auth-type", "-c", "--login-class"},
	} {
		for _, option := range options {
			for _, tc := range []struct {
				target  string
				outcome Outcome
			}{{"agy models", Deny}, {"echo unrelated", Allow}} {
				command := wrapper + " " + option + " value " + tc.target
				if verdict := policy.holds.judge(t.Context(), command, ""); verdict.Outcome != tc.outcome {
					t.Errorf("%s: %+v; want %v", command, verdict, tc.outcome)
				}
			}
		}
	}
}

// TestHolds_ScalarBounds checks exact and exceeded byte, line, word and segment caps.
func TestHolds_ScalarBounds(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, tc := range []struct {
		command string
		outcome Outcome
	}{
		{strings.Repeat(" ", contextopt.MaxSourceBytes), Allow},
		{strings.Repeat(" ", contextopt.MaxSourceBytes+1), Deny},
		{strings.Repeat("\n", admissionMaxLines-1), Allow},
		{strings.Repeat("\n", admissionMaxLines), Deny},
		{"echo " + strings.Repeat("x ", admissionMaxWords-1), Allow},
		{"echo " + strings.Repeat("x ", admissionMaxWords), Deny},
		{strings.Repeat(";", admissionMaxSegments-1), Allow},
		{strings.Repeat(";", admissionMaxSegments), Deny},
	} {
		if verdict := policy.holds.judge(t.Context(), tc.command, ""); verdict.Outcome != tc.outcome {
			t.Errorf("length %d: %+v; want %v", len(tc.command), verdict, tc.outcome)
		}
	}
}

// TestHolds_PayloadDepthBoundary permits four literal payload levels and refuses five.
func TestHolds_PayloadDepthBoundary(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	command := "echo unrelated"
	for i := 0; i < 4; i++ {
		command = "eval " + util.QuotePOSIXShellArg(command)
	}
	if verdict := policy.holds.judge(t.Context(), command, ""); verdict.Outcome != Allow {
		t.Fatal(verdict)
	}
	command = "eval " + util.QuotePOSIXShellArg(command)
	if verdict := policy.Command(command); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "payload bound") {
		t.Fatal(verdict)
	}
}

// TestHolds_NameReduction checks path, executable suffix, empty list and independent cloning.
func TestHolds_NameReduction(t *testing.T) {
	for input, want := range map[string]string{"agy": "agy", "agy.exe": "agy", "/opt/bin/agy.exe": "agy", `C:\bin\agy.exe`: "agy"} {
		if got := commandName(input); got != want {
			t.Errorf("%s: %s, want %s", input, got, want)
		}
	}
	if got := util.CommandNames(nil); got != nil {
		t.Fatalf("nil names: %v", got)
	}
	input := []string{"agy.exe"}
	normalized := util.CommandNames(input)
	normalized[0] = "changed"
	if input[0] != "agy.exe" {
		t.Fatal("normalization aliases input")
	}
}

// TestHolds_ContinuationKeepsExistingRules checks joined words and existing spaced policy matches.
func TestHolds_ContinuationKeepsExistingRules(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, command := range []string{"a\\\ngy models", "a\\\r\ngy models", "rm \\\n-rf .git/hooks"} {
		if verdict := policy.Command(command); verdict.Outcome != Deny {
			t.Errorf("%q: %+v", command, verdict)
		}
	}
}

// writeHoldFixture writes hold content to a fixture file, failing the test on error.
func writeHoldFixture(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHolds_PresentRefusesProgramWords checks refusals for direct, wrapped and compound commands.
func TestHolds_PresentRefusesProgramWords(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason+"\nsecond line is not the reason\n")
	commands := []string{
		`bash -c 'agy models'`, `sh -c 'agy models'`, `zsh -c 'agy models'`, `dash -c 'agy models'`,
		`eval 'agy models'`, `"agy" models`, `'agy' models`, `a\gy models`, `a"g"y models`,
		`>out agy models`, `2>&1 agy models`, `<in agy>out models`, `&>out agy models`,
		`$PROGRAM models`, `${PROGRAM} models`, `$(printf agy) models`, `a*y models`,
		`env --argv0 pretend agy models`, `sudo -r role agy models`,
		`env --unknown value agy models`, `sudo --unknown value agy models`,
		`bash -c "$PAYLOAD"`, `eval agy models`,
		"a\\\ngy models",
		"agy models", "praetor-agy-wrapper models", "/opt/bin/agy models",
		"env X=1 agy models", "X=1 agy models", "timeout 60 agy models",
		"command agy models", "env X=1 timeout -s TERM 60 command agy models",
		"sudo -u operator agy models",
		"echo ok; agy models", "true && agy models", "echo ok | agy models",
		"(agy models)", "echo $(agy models)", "if true; then agy models; fi",
		"agy models; bash /path/agy-run.sh work agy:model brief",
		"bash /path/agy-run.sh work agy:model brief; agy models",
		"agy agy-run.sh models",
		strings.Repeat("command ", admissionMaxWrappers+1) + "agy models",
		strings.Repeat("X=1 ", admissionMaxWords+1) + "agy models",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			verdict := policy.Command(command)
			for _, want := range []string{file, holdTestReason, "launcher is the only path"} {
				if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, want) {
					t.Fatalf("%q: %+v; want denial containing %q", command, verdict, want)
				}
			}
			if strings.Contains(verdict.Reason, "second line") {
				t.Fatal("reason includes more than the first hold line")
			}
		})
	}
}

// TestHolds_LauncherAndNonCommandsAllowed checks that launchers and non-command mentions remain allowed.
func TestHolds_LauncherAndNonCommandsAllowed(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, command := range []string{
		"command -v agy", "command -V agy", "type agy", "which agy", "hash -t agy",
		"env command -v agy", "env --argv0 alias echo unrelated",
		"bash /path/agy-run.sh work agy:model brief", "/path/agy-run.sh agy models",
		"env X=1 timeout 60 /path/agy-run.sh agy models",
		`echo "agy models"`, "echo agy models", "echo ok # agy models",
		"cat <<'EOF'\nagy models\npraetor-agy-wrapper models\nEOF",
		"echo agy-run.sh", "agy-other models", "X=1", "env", "",
	} {
		if verdict := policy.Command(command); verdict.Outcome != Allow {
			t.Errorf("%q: %+v", command, verdict)
		}
	}
}

// TestHolds_AbsentAndUnset checks absent holds, live file changes and policies without holds.
func TestHolds_AbsentAndUnset(t *testing.T) {
	policy, file := holdFixture(t)
	for _, command := range []string{"agy models", "praetor-agy-wrapper models", "env X=1 agy models", "timeout 60 agy models"} {
		if verdict := policy.Command(command); verdict.Outcome != Allow {
			t.Errorf("absent hold %q: %+v", command, verdict)
		}
	}
	// Same compiled policy must observe a hold created after compilation.
	writeHoldFixture(t, file, holdTestReason)
	if policy.Command("agy models").Outcome != Deny {
		t.Fatal("new hold ignored")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if policy.Command("agy models").Outcome != Allow {
		t.Fatal("removed hold still enforced")
	}
	unset, err := BuildPolicy(config.HookSettings{})
	if err != nil || unset.holds != nil || unset.Command("agy models").Outcome != Allow {
		t.Fatalf("unset: %+v, %v", unset, err)
	}
}

// TestHolds_UnreadableAndBoundaries checks refusals for unreadable files, size limits and canceled reads.
func TestHolds_UnreadableAndBoundaries(t *testing.T) {
	policy, file := holdFixture(t)
	// A directory is deterministically unreadable as a hold, even as root.
	if err := os.Mkdir(file, 0o500); err != nil {
		t.Fatal(err)
	}
	verdict := policy.Command("agy models")
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) || !strings.Contains(verdict.Reason, "cannot read") {
		t.Fatalf("unreadable hold: %+v", verdict)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, contextopt.MaxSourceBytes, contextopt.MaxSourceBytes + 1} {
		writeHoldFixture(t, file, strings.Repeat("x", size))
		if verdict := policy.Command("agy models"); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) {
			t.Fatalf("hold size %d: %+v", size, verdict)
		}
	}
	checkCanceledHold(t)
}

// checkCanceledHold uses a missing file so only cancellation can produce the required refusal.
func checkCanceledHold(t *testing.T) {
	t.Helper()
	policy, _ := holdFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if verdict := policy.CommandIn(ctx, "agy models", ""); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "context canceled") {
		t.Fatalf("canceled read: %+v", verdict)
	}
}

// TestHolds_PathExpansionAndRelativeBase checks environment, home and working-directory path resolution.
func TestHolds_PathExpansionAndRelativeBase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAETOR_HOLD_TEST_ROOT", dir)
	file := filepath.Join(dir, "hold")
	writeHoldFixture(t, file, holdTestReason)
	for _, path := range []string{"${PRAETOR_HOLD_TEST_ROOT}/hold", "hold"} {
		policy, err := BuildPolicy(config.HookSettings{Holds: []config.HoldSettings{{File: path, Commands: []string{"agy"}, Via: []string{"agy-run.sh"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if verdict := policy.CommandIn(t.Context(), "agy models", dir); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) {
			t.Fatalf("%s: %+v", path, verdict)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := expandHoldFile("~/hold"); err != nil || got != filepath.Join(home, "hold") {
		t.Fatalf("home expansion: %s, %v", got, err)
	}
}
