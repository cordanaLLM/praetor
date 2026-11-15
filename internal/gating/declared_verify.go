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
	"strings"
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
	// DeclaredVerifyGuardEnv is set in the command's environment, so a verify-all that itself
	// runs `gate run` does not run verify-all again inside it. It also signals to state audit
	// that it is running inside the isolated stage worktree where the git-ignored ledger is absent.
	DeclaredVerifyGuardEnv = "PRAETOR_GATE_DECLARED_VERIFY"
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

// declaredVerifyEnvironment selects C messages without changing recipe data locales.
// GNU gettext 2.3.2/2.3.3 documents LC_ALL's override and LANGUAGE's message priority:
// https://www.gnu.org/software/gettext/manual/html_node/Locale-Environment-Variables.html
// https://www.gnu.org/software/gettext/manual/html_node/The-LANGUAGE-variable.html
// A nonempty LC_ALL is expanded into POSIX and GNU non-message categories before removal.
// InheritedEnvironment preserves the Git repository-variable scrub and recursion guard.
func declaredVerifyEnvironment() []string {
	overrides := []string{DeclaredVerifyGuardEnv + "=1", "LC_MESSAGES=C", "LANGUAGE=C"}
	if all := os.Getenv("LC_ALL"); all != "" {
		for _, category := range declaredVerifyLocaleCategories {
			overrides = append(overrides, category+"="+all)
		}
	}
	return util.FilterEnvironment(util.InheritedEnvironment(overrides), func(name string) bool {
		return name == "LC_ALL"
	})
}

// declaredVerifyLocaleCategories includes POSIX and GNU categories except messages.
var declaredVerifyLocaleCategories = [...]string{
	"LC_CTYPE", "LC_COLLATE", "LC_NUMERIC", "LC_TIME", "LC_MONETARY",
	"LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
}

// runDeclaredVerifyStage runs `make verify-all` when the root holds no toolchain marker and its
// makefile declares the target. Like the test suites it runs in an isolated worktree of HEAD
// under the race stage's bound (inStageWorktree), so it checks what is pushed and leaves the
// working tree as it was. A non-zero exit fails the stage, which refuses the push and names the
// command and its exit code; exit 0 passes it. It does not count toward a receipt.
func runDeclaredVerifyStage(ctx context.Context, cfg *stageConfig) (string, error) {
	reason, err := declaredVerifyNotApplicable(ctx, cfg)
	if err != nil {
		return "", err
	}
	if reason != "" {
		return "", notApplicable(reason)
	}
	if cfg.dryRun {
		return "", skipped("dry run: " + declaredVerifyCommand + " not run")
	}
	if _, err := cfg.lookPath("make"); err != nil {
		return "", skipped(declaredVerifyCommand + " is declared but make is not on PATH, so it did not run")
	}
	budget, err := stageBudget(ctx, cfg.repoDir)
	if err != nil {
		return "", err
	}
	bound := budget.StageBound
	origin := budget.BoundOrigin
	if origin == "" {
		origin = "default"
	}
	stageErr := inStageWorktree(ctx, cfg, bound, origin, func(tCtx context.Context, dir string) error {
		return executeDeclaredVerify(tCtx, cfg, dir, bound, origin)
	})
	if stageErr != nil {
		return "", stageErr
	}
	cfg.declaredVerified = true
	return declaredVerifyCommand + " exited 0", nil
}

// declaredVerifyNotApplicable says why the stage runs nothing, or returns "" when it applies.
func declaredVerifyNotApplicable(ctx context.Context, cfg *stageConfig) (string, error) {
	if os.Getenv(DeclaredVerifyGuardEnv) != "" {
		return "running inside " + declaredVerifyCommand + " (" + DeclaredVerifyGuardEnv + "); not run again", nil
	}
	markers, err := toolchainMarkers(ctx, cfg)
	if err != nil {
		return "", err
	}
	if len(markers) > 0 {
		return "a tracked " + strings.Join(markers, " and ") + " is present: the toolchain stages verify this repository", nil
	}
	if !declaresVerifyTarget(cfg.repoDir) {
		return "no verification command declared: the makefile has no " + declaredVerifyTarget + " target", nil
	}
	return "", nil
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
func declaredVerifyFailure(ctx context.Context, repoDir string, bound time.Duration, origin string, output util.CommandBytes, err error) error {
	outcome := "failed to run"
	var exit interface{ ExitCode() int }
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		outcome = fmt.Sprintf("did not finish within %s (%s)", bound, origin)
	case errors.As(err, &exit):
		outcome = fmt.Sprintf("exited %d", exit.ExitCode())
	}
	target, diagnostic := declaredVerifyDiagnostic(output)
	if diagnostic == "" {
		diagnostic = err.Error()
	}
	execution := "execution error: " + util.UTF8Tail(strings.ReplaceAll(err.Error(), "\n", "; "), maxDeclaredVerifyDiagnostic)
	logPath, logErr := writeDeclaredVerifyLog(ctx, repoDir, target+"\n"+execution, diagnostic)
	logNote := "gate log: " + logPath
	if logErr != nil {
		logNote = "no gate log written: " + logErr.Error()
	}
	return &declaredVerifyError{cause: err, reason: fmt.Sprintf("declared verification %s %s; %s; %s:\n%s", declaredVerifyCommand, outcome, target+"; "+execution, logNote, util.UTF8Tail(diagnostic, maxDeclaredVerifyDiagnostic))}
}

type declaredVerifyError struct {
	cause  error
	reason string
}

// Error returns the declared verification outcome and its bounded diagnostics.
func (e *declaredVerifyError) Error() string { return e.reason }

// Unwrap preserves the command failure for error inspection.
func (e *declaredVerifyError) Unwrap() error { return e.cause }
