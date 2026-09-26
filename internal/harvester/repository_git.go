package harvester

import (
	"context"
	"errors"
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
