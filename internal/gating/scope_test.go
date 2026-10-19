// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive (stubbed runner, scopes forced available through a seam): the runner receives
// systemd-run with --user --scope --expand-environment=no, the three properties and -- go test -race ...;
// the stage message names the unit and limits.
func TestRaceStageScope_Positive_RunsInScope(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, recorded := newTestConfig(t, repoDir, false)
	cfg.scopesAvailable = func(context.Context) (bool, string) { return true, "" }

	msg, err := runGoTests(t.Context(), cfg)
	if err != nil {
		t.Fatalf("runGoTests failed: %v", err)
	}

	var runCmd *recordedCommand
	for i := range *recorded {
		if (*recorded)[i].name == "systemd-run" {
			runCmd = &(*recorded)[i]
			break
		}
	}
	if runCmd == nil {
		t.Fatalf("systemd-run was not invoked; recorded commands: %+v", *recorded)
	}

	requiredArgs := []string{
		"--user",
		"--scope",
		"--expand-environment=no",
		"--property=MemoryHigh=16G",
		"--property=MemoryMax=20G",
		"--property=MemorySwapMax=4G",
		"--",
		"go",
		"test",
		"-race",
	}
	for _, arg := range requiredArgs {
		if !slices.Contains(runCmd.args, arg) {
			t.Errorf("systemd-run args %v do not contain required arg %q", runCmd.args, arg)
		}
	}

	if !strings.Contains(msg, "admit-race-go-mod-") {
		t.Errorf("stage message %q does not contain expected unit name", msg)
	}
	wantLimits := "MemoryHigh=16G, MemoryMax=20G, MemorySwapMax=4G"
	if !strings.Contains(msg, wantLimits) {
		t.Errorf("stage message %q does not contain expected limits %q", msg, wantLimits)
	}
}

// Negative: scopes unavailable -> go test runs directly and the message says ran unscoped: with the reason.
func TestRaceStageScope_Negative_ScopesUnavailable(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, recorded := newTestConfig(t, repoDir, false)
	cfg.scopesAvailable = func(context.Context) (bool, string) { return false, "no systemd on darwin" }

	msg, err := runGoTests(t.Context(), cfg)
	if err != nil {
		t.Fatalf("runGoTests failed: %v", err)
	}

	inv := testInvocations(*recorded)
	if len(inv) == 0 || inv[0].name != "go" {
		t.Fatalf("expected go test to run directly, recorded: %+v", *recorded)
	}
	if !strings.Contains(msg, "ran unscoped: no systemd on darwin") {
		t.Errorf("stage message %q does not contain expected unscoped reason", msg)
	}
}

// Negative: PRAETOR_GATE_SCOPE_MEMORY_HIGH=12GB (bad unit) fails the stage naming the variable.
func TestRaceStageScope_Negative_BadMemoryLimitUnitFails(t *testing.T) {
	t.Setenv("PRAETOR_GATE_SCOPE_MEMORY_HIGH", "12GB")
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	cfg.scopesAvailable = func(context.Context) (bool, string) { return true, "" }

	_, err := runGoTests(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected failure on invalid PRAETOR_GATE_SCOPE_MEMORY_HIGH=12GB, got nil")
	}
	if !strings.Contains(err.Error(), "PRAETOR_GATE_SCOPE_MEMORY_HIGH") {
		t.Errorf("error %q does not name the variable PRAETOR_GATE_SCOPE_MEMORY_HIGH", err.Error())
	}
}

// Negative: PRAETOR_GATE_SCOPE=off -> unscoped with that reason.
func TestRaceStageScope_Negative_ScopeOffRunsUnscoped(t *testing.T) {
	t.Setenv("PRAETOR_GATE_SCOPE", "off")
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, recorded := newTestConfig(t, repoDir, false)
	cfg.scopesAvailable = func(context.Context) (bool, string) { return true, "" }

	msg, err := runGoTests(t.Context(), cfg)
	if err != nil {
		t.Fatalf("runGoTests failed: %v", err)
	}

	for _, cmd := range *recorded {
		if cmd.name == "systemd-run" {
			t.Fatalf("systemd-run was called when PRAETOR_GATE_SCOPE=off")
		}
	}
	inv := testInvocations(*recorded)
	if len(inv) == 0 || inv[0].name != "go" {
		t.Fatalf("expected go test to run directly, recorded: %+v", *recorded)
	}
	if !strings.Contains(msg, "ran unscoped: PRAETOR_GATE_SCOPE=off") {
		t.Errorf("stage message %q does not contain unscoped reason PRAETOR_GATE_SCOPE=off", msg)
	}
}

// Boundary: admission holds two readings then admits (planted reader) -> two held lines, then the run.
func TestRaceStageScope_Boundary_AdmissionHoldTwoReadingsThenAdmits(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)

	heldCount := 0
	cfg.hostReader = func() (sentinel.HostStats, error) {
		heldCount++
		if heldCount <= 3 { // the first reading sizes the policy (racePolicy), the next two are held
			return sentinel.HostStats{
				RAMTotalBytes: 64 * (1 << 30),
				RAMFreeBytes:  8 * (1 << 30),
				SwapMeasured:  true,
				SwapFreeBytes: 20 * (1 << 30),
			}, nil
		}
		return sentinel.HostStats{
			RAMTotalBytes: 64 * (1 << 30),
			RAMFreeBytes:  20 * (1 << 30),
			SwapMeasured:  true,
			SwapFreeBytes: 20 * (1 << 30),
		}, nil
	}
	var logBuf bytes.Buffer
	cfg.admissionLog = &logBuf
	cfg.admissionPoll = 1 * time.Millisecond
	cfg.admissionSleep = func(context.Context, time.Duration) error { return nil }

	msg, err := runGoTests(t.Context(), cfg)
	if err != nil {
		t.Fatalf("runGoTests failed: %v", err)
	}
	if msg == "" {
		t.Fatal("expected non-empty stage message")
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 held log lines, got %d:\n%s", len(lines), logBuf.String())
	}
	for i, line := range lines {
		if !strings.Contains(line, "admission wait race: available 8.0 GiB < 12.0 GiB") {
			t.Errorf("line %d = %q, want held reason", i, line)
		}
	}
}

// Boundary: the stage context ending while held fails naming the reason.
func TestRaceStageScope_Boundary_ContextCancellationWhileHeldFailsWithReason(t *testing.T) {
	repoDir := newGoModuleDir(t)
	cfg, _ := newTestConfig(t, repoDir, false)

	cfg.hostReader = func() (sentinel.HostStats, error) {
		return sentinel.HostStats{
			RAMTotalBytes: 64 * (1 << 30),
			RAMFreeBytes:  8 * (1 << 30),
			SwapMeasured:  true,
			SwapFreeBytes: 20 * (1 << 30),
		}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	cfg.admissionPoll = 10 * time.Millisecond
	cfg.admissionSleep = func(sCtx context.Context, _ time.Duration) error {
		cancel()
		return sCtx.Err()
	}

	_, err := runGoTests(ctx, cfg)
	if err == nil {
		t.Fatal("expected failure on context cancellation while held, got nil")
	}
	if !strings.Contains(err.Error(), "available 8.0 GiB < 12.0 GiB") {
		t.Errorf("error %q does not name last admission hold reason", err.Error())
	}
}

// Live planted case, skipped with a stated reason unless admission.ScopesAvailable.
func TestRaceStageScope_Live_ScopeSurvivesParentLimit(t *testing.T) {
	if ok, why := admission.ScopesAvailable(t.Context()); !ok {
		t.Skipf("admission.ScopesAvailable is false: %s", why)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	childPython := `python3 -c "b=bytearray(160*2**20); b[::4096]=b'x'*len(b[::4096])"`

	outDirect, errDirect := util.RunCommand(ctx, "", "systemd-run", "--user", "--scope", "--quiet", "--collect",
		"-p", "MemoryMax=96M", "-p", "MemorySwapMax=0", "--", "sh", "-c", childPython)
	var exitDirect *exec.ExitError
	if errDirect == nil || !errors.As(errDirect, &exitDirect) || exitDirect.ExitCode() == 0 {
		t.Fatalf("expected child running directly inside parent scope to be killed (non-zero exit), got err=%v, out=%s", errDirect, outDirect)
	}

	unit := admission.UnitName("test-survives", time.Now())
	childArgs := admission.ScopeArgs(unit, admission.Limits{MemoryHigh: "450M", MemoryMax: "512M", MemorySwapMax: "0"},
		[]string{"sh", "-c", childPython})

	parentScopedArgs := append([]string{"--user", "--scope", "--quiet", "--collect",
		"-p", "MemoryMax=96M", "-p", "MemorySwapMax=0", "--", "systemd-run"}, childArgs...)

	outScoped, errScoped := util.RunCommand(ctx, "", "systemd-run", parentScopedArgs...)
	if errScoped != nil {
		t.Fatalf("expected child wrapped via ScopeArgs inside parent to exit 0, got err=%v, out=%s", errScoped, outScoped)
	}
}

// Negative: in a devcontainer the stage never wraps the tests in systemd-run, even where the host
// offers scopes, and says why.
func TestRaceStageScope_Negative_DevcontainerRunsUnscoped(t *testing.T) {
	cfg := &stageConfig{container: &devcontainerExec{}}
	cfg.scopesAvailable = func(context.Context) (bool, string) { return true, "" }
	got, err := resolveRaceScope(t.Context(), cfg)
	if err != nil {
		t.Fatalf("resolveRaceScope: %v", err)
	}
	if got.useScope || !strings.Contains(got.reason, "devcontainer") {
		t.Fatalf("scope %+v; want unscoped naming the devcontainer", got)
	}
}

// Boundary: the admission bounds follow the host. A small host or a runner with little swap is
// judged against a quarter of its memory and half of its swap; a large host keeps the fleet's
// 12 GiB and 15 GiB.
func TestRaceStageScope_Boundary_PolicyFollowsTheHost(t *testing.T) {
	const gib = uint64(1) << 30
	small := racePolicy(sentinel.HostStats{RAMTotalBytes: 8 * gib, SwapMeasured: true, SwapTotalBytes: 4 * gib})
	if small.MinAvailableBytes != 2*gib || small.MinSwapFreeBytes != 2*gib {
		t.Fatalf("8 GiB host with 4 GiB swap: policy %+v; want 2 GiB available and 2 GiB swap free", small)
	}
	if v := admission.Judge(sentinel.HostStats{RAMTotalBytes: 8 * gib, RAMFreeBytes: 3 * gib, SwapMeasured: true, SwapTotalBytes: 4 * gib, SwapFreeBytes: 3 * gib}, small); !v.Admit {
		t.Fatalf("a small host with 3 GiB free was held: %s", v.Reason)
	}
	large := racePolicy(sentinel.HostStats{RAMTotalBytes: 128 * gib, SwapMeasured: true, SwapTotalBytes: 96 * gib})
	if large.MinAvailableBytes != 12*gib || large.MinSwapFreeBytes != 15*gib {
		t.Fatalf("128 GiB host: policy %+v; want the fleet's 12 GiB and 15 GiB", large)
	}
	if v := admission.Judge(sentinel.HostStats{RAMTotalBytes: 128 * gib, RAMFreeBytes: 10 * gib, SwapMeasured: true, SwapTotalBytes: 96 * gib, SwapFreeBytes: 90 * gib}, large); v.Admit {
		t.Fatal("a large host with 10 GiB available was admitted")
	}
	if noSwap := racePolicy(sentinel.HostStats{RAMTotalBytes: 16 * gib, SwapMeasured: true}); noSwap.MinSwapFreeBytes != 0 {
		t.Fatalf("a host without swap must not wait for swap: %+v", noSwap)
	}
}

// Positive: the stage judges a small host by its own size and runs the tests at once, without one
// held reading; a fixed 12 GiB bound would hold it until the deadline.
func TestRaceStageScope_Positive_SmallHostIsAdmitted(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	const gib = uint64(1) << 30
	cfg.hostReader = func() (sentinel.HostStats, error) {
		return sentinel.HostStats{RAMTotalBytes: 8 * gib, RAMFreeBytes: 3 * gib, SwapMeasured: true, SwapTotalBytes: 4 * gib, SwapFreeBytes: 3 * gib}, nil
	}
	var logBuf bytes.Buffer
	cfg.admissionLog = &logBuf
	cfg.admissionPoll = time.Millisecond
	cfg.admissionSleep = func(context.Context, time.Duration) error { return errors.New("held: the small host was not admitted") }
	if _, err := runGoTests(t.Context(), cfg); err != nil {
		t.Fatalf("runGoTests: %v (log %q)", err, logBuf.String())
	}
	if strings.Contains(logBuf.String(), "admission wait") {
		t.Fatalf("a small host was held: %s", logBuf.String())
	}
}
