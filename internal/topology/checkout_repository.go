package topology

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxGitDirSegments bounds the path segments foldWorktreeModules inspects (HISS-02).
const maxGitDirSegments = 256

// CheckoutRepository identifies the repository a checkout belongs to when a fleet counts
// repositories rather than checkouts.
type CheckoutRepository struct {
	// Key is equal for every checkout of one repository: its linked worktrees, and every
	// worktree's copy of one submodule. It is the canonical git common directory with
	// each linked worktree's modules directory folded onto the main one.
	Key string
	// Primary is true when the checkout's own git directory is Key: a main checkout, a
	// clone with a separate git directory, or a submodule checked out in a main checkout.
	// It is false for a linked worktree and for a submodule checked out inside one.
	Primary bool
}

// ResolveCheckoutRepository returns the repository the checkout at path belongs to.
//
// GitCommonDir groups a clone's linked worktrees, but git keeps the submodules a linked
// worktree checks out under <common>/worktrees/<name>/modules/<path>, which has no
// commondir file, beside the main worktree's <common>/modules/<path>. GitCommonDir
// therefore gives every worktree's copy of one submodule a common directory of its own.
// The key folds each <dir>/worktrees/<name>/modules segment onto <dir>/modules when
// <dir>/worktrees/<name> is a linked worktree's git directory whose commondir resolves to
// <dir>, so those copies share one key. No git process is started.
func ResolveCheckoutRepository(ctx context.Context, path string) (CheckoutRepository, error) {
	gitDir, err := checkoutGitDir(path)
	if err != nil {
		return CheckoutRepository{}, err
	}
	common, err := resolveCommonDir(ctx, gitDir)
	if err != nil {
		return CheckoutRepository{}, err
	}
	own, err := util.ResolveExistingPath(ctx, gitDir)
	if err != nil {
		return CheckoutRepository{}, fmt.Errorf("resolve git directory %s: %w", gitDir, err)
	}
	key, err := foldWorktreeModules(ctx, common)
	if err != nil {
		return CheckoutRepository{}, err
	}
	return CheckoutRepository{Key: key, Primary: own == key}, nil
}

// foldWorktreeModules rewrites every <dir>/worktrees/<name>/modules segment of the
// canonical git directory gitDir to <dir>/modules where isWorktreeModules confirms it.
func foldWorktreeModules(ctx context.Context, gitDir string) (string, error) {
	segments := strings.Split(filepath.ToSlash(gitDir), "/")
	if len(segments) > maxGitDirSegments {
		return "", fmt.Errorf("git directory %s has more than %d path segments", gitDir, maxGitDirSegments)
	}
	kept := make([]string, 0, len(segments))
	skip := 0
	for i := range segments {
		if skip > 0 {
			skip--
			continue
		}
		folds, err := isWorktreeModules(ctx, segments, i)
		if err != nil {
			return "", err
		}
		if folds {
			// Drop "worktrees" and "<name>"; "modules" is kept on the next round.
			skip = 1
			continue
		}
		kept = append(kept, segments[i])
	}
	return filepath.FromSlash(strings.Join(kept, "/")), nil
}

// isWorktreeModules reports whether segments[i:i+3] spell worktrees/<name>/modules below
// a directory <dir> = segments[:i] whose linked worktree <name> has <dir> as its common
// directory. The path prefixes are read from the unfolded segments, so every check names
// a directory that exists on disk.
func isWorktreeModules(ctx context.Context, segments []string, i int) (bool, error) {
	if i == 0 || i+2 >= len(segments) || segments[i] != "worktrees" || segments[i+2] != "modules" {
		return false, nil
	}
	worktreeGitDir := filepath.FromSlash(strings.Join(segments[:i+2], "/"))
	linkedCommon, err := resolveCommonDir(ctx, worktreeGitDir)
	if err != nil {
		return false, fmt.Errorf("resolve the common directory of worktree git directory %s: %w", worktreeGitDir, err)
	}
	parentDir := filepath.Dir(filepath.Dir(worktreeGitDir))
	parent, err := util.ResolveExistingPath(ctx, parentDir)
	if err != nil {
		return false, fmt.Errorf("resolve git directory %s: %w", parentDir, err)
	}
	return linkedCommon == parent, nil
}
