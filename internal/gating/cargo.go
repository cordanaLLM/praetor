// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The Cargo parts of the toolchain stages. Each runs only where a Cargo.lock is present
// (withCargo), through the stage's command runner and lookPath seams like the Go commands, and
// none runs in a dry run: cargo fetch and cargo audit write caches and reach the network, and the
// test commands execute repository code.

const (
	// CargoLockFile marks a Cargo workspace whose dependency graph is committed. The gate runs
	// the Cargo toolchain only where it is present: every Cargo command below is held to it with
	// --locked, and without it there is nothing to hold the build to.
	CargoLockFile = "Cargo.lock"
	// CargoAuditTimeout bounds cargo audit, which fetches the RustSec advisory database before
	// it scans Cargo.lock.
	CargoAuditTimeout = 3 * time.Minute
	// cargoInstallHint says where the Cargo toolchain comes from when cargo is not on PATH.
	cargoInstallHint = "install the Rust toolchain from https://rustup.rs"
	// cargoAuditInstallHint installs the cargo audit subcommand.
	cargoAuditInstallHint = "cargo install cargo-audit --locked"
	// maxCargoOutputBytes bounds how much of a failed cargo command's standard output a stage
	// message carries; its standard error is bounded by util.RunCommand already.
	maxCargoOutputBytes = util.MaxErrorBodyBytes
)

// cargoTestCommands are the test stage's Cargo commands, run in order in the isolated worktree.
var cargoTestCommands = [...][]string{
	{"test", "--workspace", "--locked"},
	{"clippy", "--workspace", "--all-targets", "--", "-D", "warnings"},
}

// requireCargo reports the Cargo part of a stage as not run when cargo is not on PATH. A runner
// without the Rust toolchain is reported, never passed (HISS-21).
func requireCargo(cfg *stageConfig) error {
	if _, err := cfg.lookPath("cargo"); err != nil {
		return skipped(fmt.Sprintf("not run: cargo is not on PATH (%s)", cargoInstallHint))
	}
	return nil
}

// cargoFailure describes a cargo command that failed under a bound of its own. When that bound
// fired while the stage's context was still live, it says so rather than reporting a finding;
// a cut by the run's deadline is named by attributeRunCut instead.
func cargoFailure(ctx, cmdCtx context.Context, what string, bound time.Duration, out string, err error) error {
	excerpt := util.TruncateExcerpt(out, maxCargoOutputBytes)
	if cmdCtx.Err() != nil && ctx.Err() == nil {
		return fmt.Errorf("%s did not finish within its %s bound, so this is not a finding: %w: %s",
			what, bound, err, excerpt)
	}
	return fmt.Errorf("%s failed: %w: %s", what, err, excerpt)
}

// runCargoPrefetch is the prefetch stage's Cargo part: cargo fetch --locked downloads the
// crates Cargo.lock pins and fails when the lockfile no longer matches the manifests.
func runCargoPrefetch(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo fetch --locked not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	pCtx, cancel := context.WithTimeout(ctx, DefaultPrefetchTimeout)
	defer cancel()
	if out, err := cfg.run(pCtx, cfg.repoDir, "cargo", "fetch", "--locked"); err != nil {
		return "", cargoFailure(ctx, pCtx, "cargo fetch --locked", DefaultPrefetchTimeout, out, err)
	}
	return "cargo fetch --locked passed", nil
}

// runCargoSecurity is the security stage's Cargo part: cargo audit checks Cargo.lock against the
// RustSec advisory database. Unlike the Go scanners it is optional: where cargo-audit is not
// installed the part is reported as not run, with the command that installs it, and the stage is
// never recorded as passed on its account (combineParts).
func runCargoSecurity(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo audit not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	if _, err := cfg.lookPath("cargo-audit"); err != nil {
		return "", skipped(fmt.Sprintf("cargo audit not run: cargo-audit is not installed (%s)", cargoAuditInstallHint))
	}
	aCtx, cancel := context.WithTimeout(ctx, CargoAuditTimeout)
	defer cancel()
	if out, err := cfg.run(aCtx, cfg.repoDir, "cargo", "audit"); err != nil {
		return "", cargoFailure(ctx, aCtx, "cargo audit", CargoAuditTimeout, out, err)
	}
	return "cargo audit passed", nil
}

// runCargoTests is the test stage's Cargo part: cargo test and cargo clippy with warnings denied,
// against HEAD in an isolated worktree under the same bound as the Go race detector
// (inStageWorktree), so a raised PRAETOR_TEST_STAGE_TIMEOUT applies to it too.
func runCargoTests(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo test and cargo clippy not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	budget := EnvRunBudget()
	bound := budget.StageBound
	err := inStageWorktree(ctx, cfg, bound, func(tCtx context.Context, dir string) error {
		for i := 0; i < len(cargoTestCommands); i++ {
			if runErr := runCargoSuite(tCtx, cfg, bound, dir, cargoTestCommands[i]); runErr != nil {
				return runErr
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	msg := "cargo test --workspace --locked and cargo clippy --workspace --all-targets -- -D warnings passed"
	if budget.Note != "" {
		msg += "; " + budget.Note
	}
	return msg, nil
}

// runCargoSuite runs one cargo command of the test stage in the worktree dir. A cut by the stage
// bound or the run deadline is reported as the cut, as the race stage reports one (cutError).
func runCargoSuite(tCtx context.Context, cfg *stageConfig, bound time.Duration, dir string, args []string) error {
	out, err := cfg.run(tCtx, dir, "cargo", args...)
	if err == nil {
		return nil
	}
	what := "cargo " + strings.Join(args, " ")
	excerpt := util.TruncateExcerpt(out, maxCargoOutputBytes)
	if cutErr := cutError(tCtx, what, bound, dir, fmt.Sprintf("%s [%v]", excerpt, err)); cutErr != nil {
		return cutErr
	}
	return fmt.Errorf("%s failed in %s: %s (%w)", what, dir, excerpt, err)
}
