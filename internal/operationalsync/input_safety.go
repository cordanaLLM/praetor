package operationalsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// checkInputConfiguration refuses an input checkout whose own metadata could run a program or
// substitute ancestry before any status reads it. Git filters go through
// util.RefuseGitStatusFilters, the one check every clean-tree probe uses (HISS-19, #667): a
// clean or process filter the repository configures and a tracked path's filter attribute
// selects is refused, because status runs it. A driver no tracked path selects passes, and so
// does a smudge-only one: smudge runs on checkout, and no input is ever checked out. init
// writes the overlay files directly; prepare checks out and merges in a fresh
// `clone --template= --no-checkout` of the owner, whose configuration holds only what clone
// and configureCandidate write, under an environment without the system and global files, so
// no driver an input defines exists where checkout runs.
func (op *operation) checkInputConfiguration(ctx context.Context) error {
	for _, dir := range op.inputDirs() {
		if err := util.RefuseGitStatusFilters(ctx, dir); err != nil {
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
