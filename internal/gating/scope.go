// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
)

const (
	// DefaultRaceScopeMemoryHigh is the MemoryHigh bound of the race stage's transient scope.
	DefaultRaceScopeMemoryHigh = "16G"
	// DefaultRaceScopeMemoryMax is the MemoryMax bound of the race stage's transient scope.
	DefaultRaceScopeMemoryMax = "20G"
	// DefaultRaceScopeMemorySwapMax is the MemorySwapMax bound of the race stage's transient scope.
	DefaultRaceScopeMemorySwapMax = "4G"

	// GateScopeEnv overrides or disables scope execution for the race stage (set to "off").
	GateScopeEnv = "PRAETOR_GATE_SCOPE"
	// GateAdmissionEnv set to "off" skips the race stage's wait for host memory, swap and load.
	// The wait only delays the stage, it never decides it, so skipping it changes no verdict; the
	// stage log says it was skipped.
	GateAdmissionEnv = "PRAETOR_GATE_ADMISSION"
	// GateScopeMemoryHighEnv overrides DefaultRaceScopeMemoryHigh.
	GateScopeMemoryHighEnv = "PRAETOR_GATE_SCOPE_MEMORY_HIGH"
	// GateScopeMemoryMaxEnv overrides DefaultRaceScopeMemoryMax.
	GateScopeMemoryMaxEnv = "PRAETOR_GATE_SCOPE_MEMORY_MAX"
	// GateScopeSwapMaxEnv overrides DefaultRaceScopeMemorySwapMax.
	GateScopeSwapMaxEnv = "PRAETOR_GATE_SCOPE_SWAP_MAX"
)

type raceScopeConfig struct {
	useScope bool
	reason   string
	limits   admission.Limits
}

func resolveScopeLimits() (admission.Limits, error) {
	limits := admission.Limits{
		MemoryHigh:    DefaultRaceScopeMemoryHigh,
		MemoryMax:     DefaultRaceScopeMemoryMax,
		MemorySwapMax: DefaultRaceScopeMemorySwapMax,
	}
	if high := os.Getenv(GateScopeMemoryHighEnv); high != "" {
		limits.MemoryHigh = high
		if err := (admission.Limits{MemoryHigh: high, MemoryMax: "1G", MemorySwapMax: "0"}).Check(); err != nil {
			return limits, fmt.Errorf("invalid %s=%q: %w", GateScopeMemoryHighEnv, high, err)
		}
	}
	if max := os.Getenv(GateScopeMemoryMaxEnv); max != "" {
		limits.MemoryMax = max
		if err := (admission.Limits{MemoryHigh: "1G", MemoryMax: max, MemorySwapMax: "0"}).Check(); err != nil {
			return limits, fmt.Errorf("invalid %s=%q: %w", GateScopeMemoryMaxEnv, max, err)
		}
	}
	if swap := os.Getenv(GateScopeSwapMaxEnv); swap != "" {
		limits.MemorySwapMax = swap
		if err := (admission.Limits{MemoryHigh: "1G", MemoryMax: "1G", MemorySwapMax: swap}).Check(); err != nil {
			return limits, fmt.Errorf("invalid %s=%q: %w", GateScopeSwapMaxEnv, swap, err)
		}
	}
	if err := limits.Check(); err != nil {
		return limits, err
	}
	return limits, nil
}

func resolveRaceScope(ctx context.Context, cfg *stageConfig) (raceScopeConfig, error) {
	limits, err := resolveScopeLimits()
	if err != nil {
		return raceScopeConfig{}, err
	}
	if os.Getenv(GateScopeEnv) == "off" {
		return raceScopeConfig{useScope: false, reason: GateScopeEnv + "=off", limits: limits}, nil
	}
	// In a devcontainer the race tests run through the container's runner, where no systemd-run
	// exists; the host probe below would answer for the wrong machine.
	if cfg.container != nil {
		return raceScopeConfig{useScope: false, reason: "the race tests run in the devcontainer, outside the caller's session scope", limits: limits}, nil
	}
	ok, why := cfg.scopesAvailableFunc()(ctx)
	if !ok {
		return raceScopeConfig{useScope: false, reason: why, limits: limits}, nil
	}
	return raceScopeConfig{useScope: true, limits: limits}, nil
}

func waitRaceAdmission(ctx context.Context, cfg *stageConfig) error {
	if os.Getenv(GateAdmissionEnv) == "off" {
		if _, err := fmt.Fprintf(cfg.admissionLogWriter(), "admission race: skipped (%s=off)\n", GateAdmissionEnv); err != nil {
			return fmt.Errorf("admission race: write the log: %w", err)
		}
		return nil
	}
	first, err := cfg.hostReaderFunc()()
	if err != nil {
		return fmt.Errorf("admission race: read the host: %w", err)
	}
	policy := admission.HostCappedPolicy(first)
	maxWait := TestStageTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline); left > 0 {
			maxWait = left
		}
	}
	waiter := admission.Waiter{
		Policy:  policy,
		MaxWait: maxWait,
		Poll:    cfg.admissionPollDuration(),
		Read:    cfg.hostReaderFunc(),
		Sleep:   cfg.admissionSleepFunc(),
		Log:     cfg.admissionLogWriter(),
	}
	verdict, err := waiter.Wait(ctx, "race")
	if err != nil {
		if verdict.Reason != "" && !strings.Contains(err.Error(), verdict.Reason) {
			return fmt.Errorf("admission race held (%s): %w", verdict.Reason, err)
		}
		return fmt.Errorf("admission race: %w", err)
	}
	return nil
}

func raceTestCommand(module goModule, bound time.Duration, scopeCfg raceScopeConfig) (string, []string, string) {
	argv := []string{"go", "test", "-race", "-timeout", bound.String(), "./..."}
	if !scopeCfg.useScope {
		return argv[0], argv[1:], fmt.Sprintf("race tests ran unscoped: %s", scopeCfg.reason)
	}
	unit := admission.UnitName(fmt.Sprintf("race-%s", module.file), time.Now())
	scopeArgs := admission.ScopeArgs(unit, scopeCfg.limits, argv)
	msg := fmt.Sprintf("race tests ran in scope %s (MemoryHigh=%s, MemoryMax=%s, MemorySwapMax=%s)",
		unit, scopeCfg.limits.MemoryHigh, scopeCfg.limits.MemoryMax, scopeCfg.limits.MemorySwapMax)
	return "systemd-run", scopeArgs, msg
}
