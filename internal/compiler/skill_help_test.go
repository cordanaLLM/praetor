package compiler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestSkillHelpDeadlineAfterWarmup requires named skips for wrapped deadlines and bounded kills.
func TestSkillHelpDeadlineAfterWarmup(t *testing.T) {
	for _, killed := range []bool{false, true} {
		t.Run(fmt.Sprintf("killed=%t", killed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { assertSkillHelpDeadlineAfterWarmup(t, killed) })
		})
	}
}

// assertSkillHelpDeadlineAfterWarmup checks timeout classification after a successful version call.
func assertSkillHelpDeadlineAfterWarmup(t *testing.T, killed bool) {
	t.Helper()
	var calls []string
	runner := func(ctx context.Context, tool string, args []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "--version" {
			return "version", nil
		}
		if killed {
			<-ctx.Done()
			return "partial help", errors.New("exit status 1")
		}
		return "", fmt.Errorf("runner: %w", context.DeadlineExceeded)
	}
	if _, err := runBoundedSkillHelp("dotnet", []string{"--version"}, skillWarmupTimeout, runner); err != nil {
		t.Fatal(err)
	}
	err := verifySkillSubcommandFlags([]skillCommand{{tool: "dotnet", helpArgs: []string{"build"}}}, runner)
	var skip *skillHelpSkip
	want := "dotnet did not answer within 1m0s on this runner; flags not checked (call: dotnet build --help)"
	if !errors.As(err, &skip) || skip.reason != want {
		t.Fatalf("deadline must name tool, call and bound: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"--version", "build --help"}) {
		t.Fatalf("warmup/probe order: %v", calls)
	}
}

// TestSkillHelpSlowWarmup allows setup beyond the probe bound and still checks every flag.
func TestSkillHelpSlowWarmup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls []string
		runner := func(ctx context.Context, tool string, args []string) (string, error) {
			calls = append(calls, strings.Join(args, " "))
			deadline, ok := ctx.Deadline()
			wantBound := skillHelpTimeout
			if args[len(args)-1] == "--version" {
				wantBound = skillWarmupTimeout
			}
			if !ok || time.Until(deadline) != wantBound {
				t.Fatalf("%s %v bound = %s; want %s", tool, args, time.Until(deadline), wantBound)
			}
			if wantBound == skillWarmupTimeout {
				time.Sleep(skillHelpTimeout + time.Second)
			}
			return "--message-format", ctx.Err()
		}
		if _, err := runBoundedSkillHelp("cargo", []string{"--version"}, skillWarmupTimeout, runner); err != nil {
			t.Fatal(err)
		}
		command := skillCommand{tool: "cargo", helpArgs: []string{"clippy"}, flags: []string{"--message-format"}}
		if err := verifySkillSubcommandFlags([]skillCommand{command, command}, runner); err != nil {
			t.Fatal(err)
		}
		want := []string{"--version", "clippy --version", "clippy --help", "clippy --help"}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("setup must run once before probes: %v; want %v", calls, want)
		}
	})
}

// TestSkillHelpFailuresStayFailures rejects ordinary errors and unsupported flags without skips.
func TestSkillHelpFailuresStayFailures(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		output string
		err    error
		want   string
	}{
		{"warmup", []string{"--version"}, "broken SDK", errors.New("exit status 1"), "broken SDK"},
		{"help", []string{"build", "--help"}, "invalid option", errors.New("exit status 1"), "invalid option"},
		{"killed early", []string{"build", "--help"}, "", errors.New("signal: killed"), "signal: killed"},
		{"deadline text", []string{"build", "--help"}, "", errors.New("context deadline exceeded"), "context deadline exceeded"},
		{"canceled", []string{"build", "--help"}, "", context.Canceled, "context canceled"},
		{"missing flag", nil, "--real", nil, "--fake"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(context.Context, string, []string) (string, error) { return tc.output, tc.err }
			var err error
			if tc.args != nil {
				_, err = runBoundedSkillHelp("dotnet", tc.args, skillHelpTimeout, runner)
			} else {
				err = verifySkillSubcommandFlags([]skillCommand{{tool: "dotnet", flags: []string{"--fake"}}}, runner)
			}
			var skip *skillHelpSkip
			if err == nil || errors.As(err, &skip) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("must fail with %q, never skip: %v", tc.want, err)
			}
		})
	}
}

// TestSkillHelpMissingSubcommand skips only explicit missing-component diagnostics on failures.
func TestSkillHelpMissingSubcommand(t *testing.T) {
	for _, output := range []string{
		"error: no such command: `clippy`",
		"error: 'cargo-clippy' is not installed for the toolchain 'stable'",
	} {
		runner := func(context.Context, string, []string) (string, error) {
			return output, errors.New("exit status 1")
		}
		err := verifySkillSubcommandFlags([]skillCommand{{tool: "cargo", helpArgs: []string{"clippy"}}}, runner)
		var skip *skillHelpSkip
		want := "cargo clippy not installed; flags not checked (call: cargo clippy --version; bound: 3m0s)"
		if !errors.As(err, &skip) || skip.reason != want {
			t.Errorf("missing component must name skipped check: %v", err)
		}
	}
}

// TestSkillHelpSubcommandErrors rejects setup errors and missing-component text in successful help.
func TestSkillHelpSubcommandErrors(t *testing.T) {
	runner := func(context.Context, string, []string) (string, error) {
		return "component setup failed", errors.New("exit status 1")
	}
	command := skillCommand{tool: "cargo", helpArgs: []string{"clippy"}}
	err := verifySkillSubcommandFlags([]skillCommand{command}, runner)
	var skip *skillHelpSkip
	if err == nil || errors.As(err, &skip) || !strings.Contains(err.Error(), "component setup failed") {
		t.Fatalf("ordinary clippy setup failure must fail: %v", err)
	}
	runner = func(context.Context, string, []string) (string, error) {
		return "error: no such command: `clippy`", nil
	}
	if err := verifySkillSubcommandFlags([]skillCommand{command}, runner); err != nil {
		t.Fatalf("successful output must not be interpreted as absence: %v", err)
	}
}

// TestSkillHelpSubcommandIsolation keeps installed siblings checked when an optional command is absent.
func TestSkillHelpSubcommandIsolation(t *testing.T) {
	commands := []skillCommand{
		{tool: "cargo", helpArgs: []string{"build"}, flags: []string{"--message-format"}},
		{tool: "cargo", helpArgs: []string{"clippy"}},
		{tool: "cargo", helpArgs: []string{"clippy"}},
	}
	groups := groupSkillSubcommands(commands)
	if len(groups) != 2 || len(groups["clippy"]) != 2 {
		t.Fatalf("commands must share one warmup per subcommand: %v", groups)
	}
	var checkedBuild bool
	runner := func(ctx context.Context, tool string, args []string) (string, error) {
		if args[0] == "clippy" {
			return "error: no such command: `clippy`", errors.New("exit status 1")
		}
		checkedBuild = true
		return "--message-format", nil
	}
	err := verifySkillSubcommandFlags(groups["clippy"], runner)
	var skip *skillHelpSkip
	if !errors.As(err, &skip) {
		t.Fatalf("absent clippy must skip: %v", err)
	}
	if err := verifySkillSubcommandFlags(groups["build"], runner); err != nil || !checkedBuild {
		t.Fatalf("installed cargo build must still be checked: checked=%t err=%v", checkedBuild, err)
	}
}
