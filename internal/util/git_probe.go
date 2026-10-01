package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// GitAnsweredNotARepository reports whether a RunGitProbe that returned result and err failed
// because git answered that its directory is not inside a repository, rather than because the
// read did not complete. RunGitProbe runs git under LANG=C.UTF-8, so the message it matches is
// the untranslated one.
func GitAnsweredNotARepository(result CommandBytes, err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && bytes.Contains(result.Stderr, []byte("not a git repository"))
}

// GitProbeTimeout bounds one RunGitProbe inspection.
const GitProbeTimeout = 5 * time.Second

// RunGitProbe isolates bounded read-only Git inspections from inherited Git
// configuration, hooks, filesystem monitors and lazy network fetches. Callers
// supply fixed inspection argv and must handle filters/submodules before status.
func RunGitProbe(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error) {
	return RunGitProbeWithin(ctx, dir, maxBytes, GitProbeTimeout, args...)
}

// RunGitProbeWithin is RunGitProbe under a caller-chosen bound instead of GitProbeTimeout, for
// an inspection whose cost grows with the repository, such as a status walk of a large tree.
// The caller's own deadline still applies when it is earlier; a timeout of zero or less is
// refused rather than read as "unbounded".
func RunGitProbeWithin(ctx context.Context, dir string, maxBytes int, timeout time.Duration, args ...string) (CommandBytes, error) {
	if timeout <= 0 {
		return CommandBytes{}, fmt.Errorf("git probe bound must be positive, got %v", timeout)
	}
	probeCtx, err := WithCommandEnvironment(ctx, []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1",
	})
	if err != nil {
		return CommandBytes{}, err
	}
	return runGitInspection(probeCtx, dir, maxBytes, timeout, args...)
}

// runGitInspection is the part of RunGitProbeWithin that does not choose which configuration
// git reads: it runs a read-only inspection under ctx's command environment within timeout,
// with the filesystem monitor and hooks switched off on the command line, which outranks every
// configuration file.
func runGitInspection(ctx context.Context, dir string, maxBytes int, timeout time.Duration, args ...string) (CommandBytes, error) {
	inspectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	return RunGitBytes(inspectCtx, dir, maxBytes, argv...)
}

// RunGitProbeStatus runs RunGitProbe and accepts both of git's answering statuses: 0 for a
// match and 1 for "nothing matched", which check-ignore and config --get-regexp use. It
// returns the output with that status. Any other status, a stream that reached maxBytes,
// cancellation and a failure to start git are errors, because none of them is an answer.
func RunGitProbeStatus(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, int, error) {
	result, runErr := RunGitProbe(ctx, dir, maxBytes, args...)
	return gitAnswerStatus(result, runErr, maxBytes)
}

// gitAnswerStatus classifies one git inspection whose streams were capped at maxBytes, as
// RunGitProbeStatus describes, whichever environment the inspection ran under.
func gitAnswerStatus(result CommandBytes, runErr error, maxBytes int) (CommandBytes, int, error) {
	if runErr == nil {
		return result, 0, nil
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) ||
		len(result.Stdout) >= maxBytes || len(result.Stderr) >= maxBytes {
		return result, -1, fmt.Errorf("%w: %w", errGitProbeStatus, runErr)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
		return result, 1, nil
	}
	return result, -1, fmt.Errorf("%w: %w", errGitProbeStatus, runErr)
}
