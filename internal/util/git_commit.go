// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"fmt"
	"strings"
)

// maxCommitNameBytes bounds git's answer to one commit resolution: one object name and a newline.
const maxCommitNameBytes = 1024

// ResolveGitCommit returns the full object name of the commit name names in the checkout at dir,
// such as a branch, a remote-tracking ref or an abbreviated object name, or "" when git answers
// that the checkout holds no such commit (rev-parse --verify --quiet exits 1, which it also does
// for an object that is no commit). It runs through RunGitProbeStatus, so nothing is fetched. A
// name ValidateExecArg refuses and a read git did not answer are errors, never "none".
func ResolveGitCommit(ctx context.Context, dir, name string) (string, error) {
	if err := ValidateExecArg(name); err != nil {
		return "", fmt.Errorf("resolve commit %q: %w", name, err)
	}
	out, status, err := RunGitProbeStatus(ctx, dir, maxCommitNameBytes, "rev-parse", "--verify", "--quiet", name+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve commit %s: %w", name, err)
	}
	if status != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(out.Stdout)), nil
}
