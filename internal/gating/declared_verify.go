// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The declared-verification stage (#1218) runs the repository's own verification entry point
// where no toolchain stage applies. A repository declares it as the Makefile target verify-all:
// adoption writes or preserves that target (internal/adopt), the managed README names it as the
// configured verification cascade, and admittedUnsupportedNote points to it. Without it, a root
// holding neither go.mod nor Cargo.lock was admitted with none of its own checks run.
const (
	// declaredVerifyTarget is the Makefile target a repository declares its verification in.
	declaredVerifyTarget = "verify-all"
	// declaredVerifyGuardEnv is set in the command's environment, so a verify-all that itself
	// runs `gate run` does not run verify-all again inside it.
	declaredVerifyGuardEnv = "PRAETOR_GATE_DECLARED_VERIFY"
	// maxMakefileBytes bounds the Makefile read to find the target (HISS-02).
	maxMakefileBytes = 4 << 20
	// maxDeclaredVerifyDiagnostic bounds the command output quoted in a failed stage's reason;
	// the command's own streams are bounded by util.RunCommand.
	maxDeclaredVerifyDiagnostic = 2048
)

// makefileNames are the names GNU Make reads a makefile under, in the order it tries them.
var makefileNames = []string{"GNUmakefile", "makefile", "Makefile"}

// declaredVerifyCommand is the command line the stage runs and every reason names.
var declaredVerifyCommand = "make " + declaredVerifyTarget

// runDeclaredVerifyStage runs `make verify-all` when the root holds no toolchain marker and its
// makefile declares the target. Like the test suites it runs in an isolated worktree of HEAD
// under the race stage's bound (inStageWorktree), so it checks what is pushed and leaves the
// working tree as it was. A non-zero exit fails the stage, which refuses the push and names the
// command and its exit code; exit 0 passes it. It does not count toward a receipt.
func runDeclaredVerifyStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if reason := declaredVerifyNotApplicable(cfg); reason != "" {
		return "", notApplicable(reason)
	}
	if cfg.dryRun {
		return "", skipped("dry run: " + declaredVerifyCommand + " not run")
	}
	if _, err := cfg.lookPath("make"); err != nil {
		return "", skipped(declaredVerifyCommand + " is declared but make is not on PATH, so it did not run")
	}
	bound := stageBudget(cfg.repoDir).StageBound
	err := inStageWorktree(ctx, cfg, bound, func(tCtx context.Context, dir string) error {
		runCtx, envErr := util.WithCommandEnvironment(tCtx, util.InheritedEnvironment([]string{declaredVerifyGuardEnv + "=1"}))
		if envErr != nil {
			return fmt.Errorf("%s: %w", declaredVerifyCommand, envErr)
		}
		if _, runErr := cfg.run(runCtx, dir, "make", declaredVerifyTarget); runErr != nil {
			return declaredVerifyFailure(tCtx, bound, runErr)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	cfg.declaredVerified = true
	return declaredVerifyCommand + " exited 0", nil
}

// declaredVerifyNotApplicable says why the stage runs nothing, or returns "" when it applies.
func declaredVerifyNotApplicable(cfg *stageConfig) string {
	switch {
	case os.Getenv(declaredVerifyGuardEnv) != "":
		return "running inside " + declaredVerifyCommand + " (" + declaredVerifyGuardEnv + "); not run again"
	case holdsToolchainMarker(cfg.repoDir):
		return "go.mod or Cargo.lock at the root: the toolchain stages verify this repository"
	case !declaresVerifyTarget(cfg.repoDir):
		return "no verification command declared: the makefile has no " + declaredVerifyTarget + " target"
	}
	return ""
}

// declaresVerifyTarget reports whether the makefile GNU Make would read at repoDir declares
// verify-all. An unreadable or oversized makefile declares nothing.
func declaresVerifyTarget(repoDir string) bool {
	for _, name := range makefileNames {
		path := filepath.Join(repoDir, name)
		if !util.FileExists(path) {
			continue
		}
		data, err := util.ReadFileLimited(path, maxMakefileBytes)
		return err == nil && util.MakefileHasTarget(string(data), declaredVerifyTarget)
	}
	return false
}

// declaredVerifyFailure names the command, its exit code or why it has none, and the end of
// what it reported.
func declaredVerifyFailure(ctx context.Context, bound time.Duration, err error) error {
	outcome := "failed to run"
	var exit interface{ ExitCode() int }
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		outcome = fmt.Sprintf("did not finish within %s", bound)
	case errors.As(err, &exit):
		outcome = fmt.Sprintf("exited %d", exit.ExitCode())
	}
	return fmt.Errorf("declared verification %s %s: %s", declaredVerifyCommand, outcome, tail(err.Error(), maxDeclaredVerifyDiagnostic))
}

// tail keeps the last limit bytes of text, where a failing command's final report usually is.
func tail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return "..." + text[len(text)-limit:]
}
