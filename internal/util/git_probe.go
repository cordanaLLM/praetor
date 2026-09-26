package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// RunGitProbe isolates bounded read-only Git inspections from inherited Git
// configuration, hooks, filesystem monitors and lazy network fetches. Callers
// supply fixed inspection argv and must handle filters/submodules before status.
func RunGitProbe(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error) {
	probeCtx, err := WithCommandEnvironment(ctx, []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1",
	})
	if err != nil {
		return CommandBytes{}, err
	}
	probeCtx, cancel := context.WithTimeout(probeCtx, 5*time.Second)
	defer cancel()
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	return RunGitBytes(probeCtx, dir, maxBytes, argv...)
}

// RunGitProbeStatus runs RunGitProbe and accepts both of git's answering statuses: 0 for a
// match and 1 for "nothing matched", which check-ignore and config --get-regexp use. It
// returns the output with that status. Any other status, a stream that reached maxBytes,
// cancellation and a failure to start git are errors, because none of them is an answer.
func RunGitProbeStatus(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, int, error) {
	result, runErr := RunGitProbe(ctx, dir, maxBytes, args...)
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
