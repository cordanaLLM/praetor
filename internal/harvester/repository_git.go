package harvester

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func inventoryGit(ctx context.Context, repoPath string, args ...string) (util.CommandBytes, error) {
	commandCtx, err := util.WithCommandEnvironment(ctx, []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1",
	})
	if err != nil {
		return util.CommandBytes{}, err
	}
	commandCtx, cancel := context.WithTimeout(commandCtx, 5*time.Second)
	defer cancel()
	safeArgs := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}
	safeArgs = append(safeArgs, args...)
	return util.RunCommandBytes(commandCtx, repoPath, "git", inventoryGitOutputLimit, safeArgs...)
}

func inventoryRunGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	result, err := inventoryGit(ctx, repoPath, args...)
	if err != nil {
		return "", errors.New("git probe failed")
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// inventoryRunGitExit accepts only Git's success or no-match statuses. Transport,
// output-limit, cancellation and all other Git failures remain unknown.
func inventoryRunGitExit(ctx context.Context, repoPath string, args ...string) (int, error) {
	result, runErr := inventoryGit(ctx, repoPath, args...)
	if runErr == nil {
		return 0, nil
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) ||
		len(result.Stdout) >= inventoryGitOutputLimit || len(result.Stderr) >= inventoryGitOutputLimit {
		return -1, errors.New("git probe failed")
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
		return 1, nil
	}
	return -1, errors.New("git probe failed")
}
