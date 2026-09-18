package release

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/changelog"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ReleaseOptions configures release preparation and verification.
type ReleaseOptions struct {
	RepoPath   string
	Version    string
	Date       string
	SkipVerify bool
	SkipClean  bool
}

// PrepareRelease validates cleanliness, executes verification gates, and renders changelog.
func PrepareRelease(ctx context.Context, opts ReleaseOptions) error {
	if _, ok := semver.Parse(opts.Version); !ok {
		return fmt.Errorf("release: version %q is not valid SemVer", opts.Version)
	}

	cleanVer := strings.TrimPrefix(opts.Version, "v")

	// 1. Verify working tree is clean
	if !opts.SkipClean {
		out, err := util.RunGit(ctx, opts.RepoPath, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("check git status: %w", err)
		}
		if len(strings.TrimSpace(out)) > 0 {
			return fmt.Errorf("release: working tree has uncommitted changes")
		}
	}

	// 2. Execute verification gates
	if !opts.SkipVerify {
		if out, err := util.RunCommandBytes(ctx, opts.RepoPath, "make", 16<<20, "verify-all"); err != nil {
			return fmt.Errorf("make verify-all failed: %w\n%s\n%s", err, out.Stdout, out.Stderr)
		}
	}

	// 3. Render changelog release section
	if err := changelog.RenderReleaseContext(ctx, opts.RepoPath, cleanVer, opts.Date); err != nil {
		return fmt.Errorf("render changelog: %w", err)
	}

	return nil
}
