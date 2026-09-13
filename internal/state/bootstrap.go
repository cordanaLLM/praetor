package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// InitWorkingDirIfAbsentContext initializes only a newly created private ledger.
// Existing paths are unchanged, including partial ledgers and invalid paths. The
// caller must run AuditWorkingDir afterward; this function does not certify state.
// Failed initialization leaves the partial directory for explicit recovery.
func InitWorkingDirIfAbsentContext(ctx context.Context, rootPath string) (created bool, err error) {
	if ctx == nil {
		return false, errors.New("state initialization requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	project, err := contextopt.OpenDirectory(ctx, rootPath)
	if err != nil {
		return false, fmt.Errorf("open project for state bootstrap: %w", err)
	}
	defer func() { err = errors.Join(err, project.Close()) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := project.Mkdir(WorkingDirName, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("create private working directory: %w", err)
	}
	working, err := openWorkingDirectory(project)
	if err != nil {
		return true, err
	}
	defer func() { err = errors.Join(err, working.Close()) }()
	return true, initializeWorkingFiles(ctx, working)
}
