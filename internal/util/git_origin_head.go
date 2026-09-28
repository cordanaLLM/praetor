// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"fmt"
	"strings"
)

const (
	// originHeadRef is the ref git clone and `git remote set-head origin --auto` point at the
	// origin remote's default branch.
	originHeadRef = "refs/remotes/origin/HEAD"
	// originBranchPrefix is where the branches of the origin remote live.
	originBranchPrefix = "refs/remotes/origin/"
	// maxOriginHeadBytes bounds git's answer for originHeadRef: one ref name and a newline.
	maxOriginHeadBytes = 4096
)

// ReadOriginHeadBranch reads the branch the checkout at repoPath records as the origin remote's
// default: the target of refs/remotes/origin/HEAD, which git clone and `git remote set-head
// origin --auto` write, as a branch name such as "master". It runs through RunGitProbe, so no
// inherited configuration or hook takes part and nothing is fetched.
//
// ok is false when git answers that there is none: repoPath is not inside a repository
// (GitAnsweredNotARepository), or the ref is not a symbolic ref (a checkout that never recorded
// it, as a CI checkout usually does not). A read git did not answer (a missing directory, a
// cancelled or expired context, git failing to start, any other exit status) and a target outside
// refs/remotes/origin/ are errors, never "none", so no caller falls back on a question that was
// never answered.
func ReadOriginHeadBranch(ctx context.Context, repoPath string) (branch string, ok bool, err error) {
	result, status, err := RunGitProbeStatus(ctx, repoPath, maxOriginHeadBytes, "symbolic-ref", "--quiet", originHeadRef)
	if GitAnsweredNotARepository(result, err) && ctx.Err() == nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("util: read %s in %q: %w", originHeadRef, repoPath, err)
	}
	if status == 1 {
		return "", false, nil
	}
	target := strings.TrimSpace(string(result.Stdout))
	branch, found := strings.CutPrefix(target, originBranchPrefix)
	if !found || branch == "" || branch == "HEAD" {
		return "", false, fmt.Errorf("util: %s in %q points at %q, not a branch of origin", originHeadRef, repoPath, target)
	}
	return branch, true, nil
}
