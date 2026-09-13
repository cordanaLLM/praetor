package harvester

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

func inventoryGit(ctx context.Context, repoPath string, args ...string) (util.CommandBytes, error) {
	return util.RunGitProbe(ctx, repoPath, inventoryGitOutputLimit, args...)
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
