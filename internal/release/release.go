package release

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/cordanaLLM/standards/internal/changelog"
)

var semverRegex = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// ReleaseOptions configures release preparation and verification.
type ReleaseOptions struct {
	RepoPath    string
	Version     string
	Date        string
	SkipVerify  bool
	SkipClean   bool
}

// PrepareRelease validates cleanliness, executes verification gates, and renders changelog.
func PrepareRelease(ctx context.Context, opts ReleaseOptions) error {
	if !semverRegex.MatchString(opts.Version) {
		return fmt.Errorf("release: version %q is not valid SemVer", opts.Version)
	}

	cleanVer := strings.TrimPrefix(opts.Version, "v")

	// 1. Verify working tree is clean
	if !opts.SkipClean {
		cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		cmd.Dir = opts.RepoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("check git status: %w", err)
		}
		if len(strings.TrimSpace(string(out))) > 0 {
			return fmt.Errorf("release: working tree has uncommitted changes")
		}
	}

	// 2. Execute verification gates
	if !opts.SkipVerify {
		cmd := exec.CommandContext(ctx, "make", "verify-all")
		cmd.Dir = opts.RepoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("make verify-all failed: %w\n%s", err, string(out))
		}
	}

	// 3. Render changelog release section
	if err := changelog.RenderRelease(opts.RepoPath, cleanVer, opts.Date); err != nil {
		return fmt.Errorf("render changelog: %w", err)
	}

	return nil
}
