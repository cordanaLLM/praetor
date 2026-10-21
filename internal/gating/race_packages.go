// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

func validateRacePackages(packages []string) error {
	if packages == nil {
		return nil
	}
	parsed, err := util.ParseRacePackages(util.FormatRacePackages(packages))
	if err != nil {
		return fmt.Errorf("invalid pre-push race scope: %w", err)
	}
	if !slices.Equal(parsed, packages) {
		return fmt.Errorf("race scope must contain exact packages or the sole ./... pattern")
	}
	return nil
}

// scopedGoTests preserves the verdict and records the exact selection even when the detector
// is unavailable. No package discovery lives here: the hook supplies its existing calculation.
func scopedGoTests(ctx context.Context, cfg *stageConfig) (string, error) {
	msg, err := runGoTests(ctx, cfg)
	if len(cfg.racePackages) == 0 {
		return msg, err
	}
	reason := lockdown.RacePackagesReason(cfg.racePackages)
	if skip, ok := errors.AsType[*stageSkip](err); ok {
		return "", &stageSkip{status: skip.status, reason: reason + "; " + skip.reason}
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", reason, err)
	}
	return reason + "; " + msg, nil
}

func goRaceApplicability(ctx context.Context, cfg *stageConfig) error {
	if err := ctx.Err(); err != nil {
		return cutError(ctx, "creating the isolated test worktree", TestStageTimeout, cfg.repoDir, err.Error())
	}
	if cfg.racePackages != nil && len(cfg.racePackages) == 0 {
		return notApplicable(lockdown.NoAffectedGoPackage)
	}
	modules, err := cfg.goModules(ctx)
	if err != nil {
		return err
	}
	if cfg.racePackages != nil && !slices.ContainsFunc(modules, func(module goModule) bool { return module.file == "go.mod" }) {
		return errors.New("explicit race package scope requires a root go.mod")
	}
	if len(modules) == 0 {
		return notApplicable("no go.mod: Go race-detector tests skipped")
	}
	if cfg.dryRun {
		return skipped("dry run: race-detector tests skipped")
	}
	return nil
}

func prepareGoRace(ctx context.Context, cfg *stageConfig) (raceScopeConfig, error) {
	if err := goRaceApplicability(ctx, cfg); err != nil {
		return raceScopeConfig{}, err
	}
	available, absent, err := raceDetectorAvailable(ctx, cfg)
	if err != nil {
		return raceScopeConfig{}, fmt.Errorf("the race detector probe failed: %w", err)
	}
	if !available {
		return raceScopeConfig{}, skipped(fmt.Sprintf("race detector unavailable (%s): race-detector tests skipped; CI runs this leg on Linux with cgo", absent))
	}
	scopeCfg, err := resolveRaceScope(ctx, cfg)
	if err != nil {
		return raceScopeConfig{}, err
	}
	if err := waitRaceAdmission(ctx, cfg); err != nil {
		return raceScopeConfig{}, err
	}
	return scopeCfg, nil
}

func runGoRaceCommand(ctx context.Context, cfg *stageConfig, module goModule, dir string, bound time.Duration, scopeCfg raceScopeConfig) (string, error) {
	run := cfg.goToolchain().run
	count, err := ModulePackageCount(ctx, run, dir)
	if err != nil {
		return "", err
	}
	if count == 0 && cfg.racePackages != nil {
		return "", errors.New("explicit race scope selected packages in an empty module")
	}
	if count == 0 {
		return NoPackagesNote + ": race tests not run", nil
	}
	name, args, msg := raceTestCommand(module, bound, scopeCfg, cfg.racePackages)
	out, err := run(ctx, dir, name, args...)
	if err == nil {
		return msg, nil
	}
	if cutErr := cutError(ctx, "race-detector tests", bound, dir, fmt.Sprintf("%s [%v]", out, err)); cutErr != nil {
		return "", cutErr
	}
	return "", fmt.Errorf("tests failed in %s: %s (%w)", dir, out, err)
}

// An explicit empty Go selection stays visible even in a repository with only other languages.
func explicitNoGoPart(part languagePart) bool {
	return part.err != nil && part.err.Error() == lockdown.NoAffectedGoPackage
}
