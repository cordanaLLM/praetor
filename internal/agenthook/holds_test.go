// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const holdTestReason = "engine-test-order: quota hold until reviewed"

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

func writeHoldFixture(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHolds_PresentRefusesProgramWords(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason+"\nsecond line is not the reason\n")
	commands := []string{
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

func TestHolds_LauncherAndNonCommandsAllowed(t *testing.T) {
	policy, file := holdFixture(t)
	writeHoldFixture(t, file, holdTestReason)
	for _, command := range []string{
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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if verdict := policy.CommandIn(ctx, "agy models", ""); verdict.Outcome != Deny || !strings.Contains(verdict.Reason, file) {
		t.Fatalf("canceled read: %+v", verdict)
	}
}

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
	// Home expansion reuses the same resolver as command target paths.
	if got, err := expandHoldFile("~/hold"); err != nil || got != filepath.Join(homeTarget("~"), "hold") {
		t.Fatalf("home expansion: %s, %v", got, err)
	}
}
