package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (op *operation) checkInputConfiguration(ctx context.Context) error {
	for _, dir := range []string{op.opts.OwnerPath, op.opts.SourcePath} {
		if err := op.rejectFilters(ctx, dir); err != nil {
			return err
		}
		if err := op.rejectGrafts(ctx, dir); err != nil {
			return err
		}
		// Status can otherwise descend into submodules and execute their local filters.
		entries, err := op.git.run(ctx, dir, "ls-files", "--stage", "-z")
		if err != nil {
			return err
		}
		for _, entry := range strings.Split(string(entries), "\x00") {
			if strings.HasPrefix(entry, "160000 ") {
				return errors.New("operational sync does not support input submodule worktrees")
			}
		}
	}
	return nil
}

func (op *operation) rejectFilters(ctx context.Context, dir string) error {
	names, err := op.git.text(ctx, dir, "config", "--includes", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil
		}
		return err
	}
	if names != "" {
		return errors.New("repository-local Git filters are unsupported; no status or checkout was executed")
	}
	return nil
}

func (op *operation) rejectGrafts(ctx context.Context, dir string) error {
	path, err := op.git.text(ctx, dir, "rev-parse", "--git-path", "info/grafts")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	_, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect repository graft metadata: %w", err)
	}
	return errors.New("local Git graft metadata is unsupported for reviewed ancestry")
}
