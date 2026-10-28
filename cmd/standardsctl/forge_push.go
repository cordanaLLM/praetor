// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// forgeCommitBase keeps CI's explicit range and resolves only the hook's explicit mode.
func forgeCommitBase(ctx context.Context, repo, base, head string, prePush bool) (string, error) {
	if !prePush {
		if base == "" {
			return "", errors.New("usage: standardsctl forge check-commits --base=<ref> [--head=HEAD] or --pre-push")
		}
		return base, nil
	}
	if base != "" || head != "HEAD" {
		return "", errors.New("forge check-commits --pre-push requires HEAD and no explicit --base")
	}
	upstream, err := commitPushUpstream(ctx, repo)
	if err != nil {
		return "", err
	}
	base, err = util.RunGit(ctx, repo, "merge-base", upstream, head)
	if err != nil {
		return "", fmt.Errorf("resolve pre-push merge base with %s: %w; fetch the upstream or default branch", upstream, err)
	}
	if base == "" {
		return "", errors.New("resolve pre-push merge base: git returned an empty base")
	}
	return base, nil
}

func commitPushUpstream(ctx context.Context, repo string) (string, error) {
	branch, err := util.RunGit(ctx, repo, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", fmt.Errorf("pre-push range requires an attached HEAD: %w", err)
	}
	upstream, err := util.RunGit(ctx, repo, "for-each-ref", "--count=1", "--format=%(upstream)", branch)
	if err != nil {
		return "", fmt.Errorf("read pre-push upstream: %w", err)
	}
	if upstream == "" {
		return commitDefaultRef(ctx, repo)
	}
	return upstream, nil
}

// commitDefaultRef reuses the repository's declared or locally recorded default branch.
// It reads only local refs; a first push never requires network access to choose its range.
func commitDefaultRef(ctx context.Context, repo string) (string, error) {
	branch, err := forge.RepositoryDefaultBranch(ctx, repo, nil)
	if err != nil {
		return "", fmt.Errorf("resolve pre-push default branch: %w", err)
	}
	remote := "refs/remotes/origin/" + branch
	if _, err := util.RunGit(ctx, repo, "rev-parse", "--verify", remote+"^{commit}"); err == nil {
		return remote, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "refs/heads/" + branch, nil
}

// checkedCommitRange refuses a pre-push check that inspected no commits. Explicit CI ranges
// retain their existing empty-range behavior.
func checkedCommitRange(ctx context.Context, repo, base, head string, prePush bool) ([]string, error) {
	messages, err := commitRange(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}
	if prePush && len(messages) == 0 {
		return nil, fmt.Errorf("pre-push range %s..%s is empty; no commits checked", base, head)
	}
	return messages, nil
}
