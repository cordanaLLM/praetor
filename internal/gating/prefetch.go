package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

const (
	DefaultPrefetchTimeout = 60 * time.Second
)

// ErrLockfileEmpty reports a lockfile or manifest that exists but carries no content.
var ErrLockfileEmpty = errors.New("file is empty")

// PrefetchReport details the results of dependency prefetching and checksum verification.
type PrefetchReport struct {
	VerifiedDependencies bool          `json:"verified_dependencies"`
	Skipped              bool          `json:"skipped"`
	TotalModules         int           `json:"total_modules"`
	Duration             time.Duration `json:"duration"`
	Message              string        `json:"message,omitempty"`
}

// PrefetchDependencies runs go mod verify and pre-downloads modules with an explicit
// context timeout. Repositories without a go.mod are not Go repositories: the stage is
// reported as skipped instead of rejecting a conforming polyglot repository.
func PrefetchDependencies(ctx context.Context, repoDir string) (*PrefetchReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("prefetch: context cannot be nil")
	}
	modules, err := apiassets.TrackedModuleFiles(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("prefetch: discover tracked Go modules: %w", err)
	}
	if len(modules) == 0 {
		return &PrefetchReport{Skipped: true}, nil
	}
	moduleDir := filepath.Join(repoDir, filepath.Dir(filepath.FromSlash(modules[0])))
	return prefetchDependencies(ctx, moduleDir, util.RunCommand)
}

// prefetchDependencies is PrefetchDependencies with the command runner injected, so the gate
// stage runs the go commands through the same seam as every other stage and a test can prove
// which of them ran.
//
// A first attempt that ends in a transient kill (its deadline, SIGKILL or SIGTERM) runs once
// more under a fresh DefaultPrefetchTimeout window; the report's Message names the retry. A
// second failure, or any other failure, is returned as the module's error.
func prefetchDependencies(ctx context.Context, repoDir string, run commandRunner) (*PrefetchReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("prefetch: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("prefetch cancelled: %w", err)
	}

	start := time.Now()
	firstErr := prefetchModuleOnce(ctx, repoDir, run)
	if firstErr == nil {
		return &PrefetchReport{
			VerifiedDependencies: true,
			Duration:             time.Since(start),
		}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("prefetch cancelled: %w (%w)", firstErr, err)
	}
	reason, ok := transientPrefetchReason(firstErr)
	if !ok {
		return nil, firstErr
	}
	if secondErr := prefetchModuleOnce(ctx, repoDir, run); secondErr != nil {
		return nil, fmt.Errorf("%w (second attempt after retry; first attempt: %w)", secondErr, firstErr)
	}
	return &PrefetchReport{
		VerifiedDependencies: true,
		Duration:             time.Since(start),
		Message:              fmt.Sprintf("passed after retry (first attempt: %s)", reason),
	}, nil
}

// prefetchModuleOnce runs go mod verify and go mod download for repoDir under
// DefaultPrefetchTimeout, returning any verification, download, or timeout error.
func prefetchModuleOnce(ctx context.Context, repoDir string, run commandRunner) error {
	pCtx, cancel := context.WithTimeout(ctx, DefaultPrefetchTimeout)
	defer cancel()

	if out, err := run(pCtx, repoDir, "go", "mod", "verify"); err != nil {
		return fmt.Errorf("dependency verification failed: %s (%w)", out, err)
	}
	if out, err := run(pCtx, repoDir, "go", "mod", "download"); err != nil {
		return fmt.Errorf("dependency download failed: %s (%w)", out, err)
	}
	return nil
}

// signaledWaitStatus matches process wait status types that report whether
// a process terminated due to an operating system signal.
type signaledWaitStatus interface {
	Signaled() bool
	Signal() syscall.Signal
}

// isTransientPrefetchError reports whether a failed module prefetch ended in a transient
// deadline or signal kill that warrants a single retry under a fresh per-module timeout.
func isTransientPrefetchError(err error) bool {
	_, ok := transientPrefetchReason(err)
	return ok
}

// transientPrefetchReason inspects err for a deadline exceeded or kill signal,
// returning the matched reason string and whether the failure was transient.
func transientPrefetchReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded.Error(), true
	}
	if reason, ok := exitErrorKillReason(err); ok {
		return reason, true
	}
	return textKillReason(err.Error())
}

// exitErrorKillReason extracts the signal or 137/143 exit code from an exec.ExitError,
// returning the kill description and true if matched.
func exitErrorKillReason(err error) (string, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr == nil {
		return "", false
	}
	if reason, ok := processStateSignalReason(exitErr.ProcessState); ok {
		return reason, true
	}
	switch exitErr.ExitCode() {
	case 137:
		return "exit status 137", true
	case 143:
		return "exit status 143", true
	default:
		return "", false
	}
}

// processStateSignalReason inspects an exited process state for SIGKILL or SIGTERM,
// returning the signal name and true if terminated by either signal.
func processStateSignalReason(state *os.ProcessState) (string, bool) {
	if state == nil {
		return "", false
	}
	ws, ok := state.Sys().(signaledWaitStatus)
	if !ok || !ws.Signaled() {
		return "", false
	}
	switch ws.Signal() {
	case syscall.SIGKILL:
		return "signal: killed", true
	case syscall.SIGTERM:
		return "signal: terminated", true
	default:
		return "", false
	}
}

var transientKillPhrases = [...]string{
	"context deadline exceeded",
	"signal: killed",
	"signal: terminated",
	"exit status 137",
	"exit status 143",
}

// textKillReason searches msg for known transient kill phrases, returning
// the matched phrase and true if found.
func textKillReason(msg string) (string, bool) {
	for i := 0; i < len(transientKillPhrases); i++ {
		if strings.Contains(msg, transientKillPhrases[i]) {
			return transientKillPhrases[i], true
		}
	}
	return "", false
}

// VerifyLockfiles confirms that the standards manifest and lockfile exist and are
// non-empty.
func VerifyLockfiles(repoDir string) error {
	lockPath := filepath.Join(repoDir, ".standards.lock")
	manifestPath := filepath.Join(repoDir, ".standards.yaml")

	if err := checkFileExistsAndNonEmpty(manifestPath); err != nil {
		return fmt.Errorf("standards manifest invalid: %w", err)
	}
	if err := checkFileExistsAndNonEmpty(lockPath); err != nil {
		return fmt.Errorf("standards lockfile invalid: %w", err)
	}
	return nil
}

// checkFileExistsAndNonEmpty stats path literally. It must never treat the path as a
// glob pattern: repository directories legitimately contain '[', '*' and '?'.
func checkFileExistsAndNonEmpty(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not found: %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("expected a file but found a directory: %s", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%w: %s", ErrLockfileEmpty, path)
	}
	return nil
}
