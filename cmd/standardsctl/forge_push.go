// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"

	"github.com/cordanaLLM/praetor/internal/docstrings"
)

// forgeCheckCommitInput selects explicit history or every branch update named by git.
func forgeCheckCommitInput(ctx context.Context, repo, base, head string, prePush bool, remote string, excludes []string, firstParent bool) ([]string, error) {
	if !prePush {
		if base == "" {
			return nil, errors.New("usage: standardsctl forge check-commits --base=<ref> [--head=HEAD] or --pre-push")
		}
		return commitRange(ctx, repo, base, head, excludes, firstParent)
	}
	if remote == "" {
		return nil, errors.New("usage: --pre-push requires --remote=<name>")
	}
	if base != "" || head != "HEAD" {
		return nil, errors.New("forge check-commits --pre-push requires HEAD and no explicit --base")
	}
	ranges, err := docstrings.ReadPrePushCommitRanges(ctx, os.Stdin)
	if err != nil {
		return nil, err
	}
	destination, err := forgePushRemote(ctx, repo, remote)
	if err != nil {
		return nil, err
	}
	return forgePushRanges(ctx, repo, remote, destination, ranges, excludes, firstParent)
}

// forgePushRanges checks each pushed range, counting each commit once across all refs.
func forgePushRanges(ctx context.Context, repo, remote, destination string, ranges []docstrings.PrePushCommitRange, excludes []string, firstParent bool) ([]string, error) {
	var messages []string
	seen := make(map[string]bool)
	for i := 0; i < len(ranges); i++ {
		r := ranges[i]
		rangeBase := r.RemoteOID
		if r.NewRef {
			rangeBase = ""
		}
		found, err := commitRange(ctx, repo, rangeBase, r.LocalOID, excludes, firstParent, destination)
		if err != nil {
			if r.NewRef && destination == "" {
				return nil, fmt.Errorf("push destination %q: %w; push to a named remote configured with git remote", remote, err)
			}
			return nil, err
		}
		messages, err = appendUniquePushRecords(messages, found, seen)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// forgePushRemote returns a verified, literal remote name or an empty exclusion for unknown destinations.
func forgePushRemote(ctx context.Context, repo, remote string) (string, error) {
	if strings.ContainsAny(remote, "?*[\\") {
		return "", nil
	}
	out, err := util.RunGit(ctx, repo, "remote")
	if err != nil {
		return "", fmt.Errorf("list push remotes: %w", err)
	}
	for _, name := range strings.Split(out, "\n") {
		if name == remote {
			return remote, nil
		}
	}
	return "", nil
}

// appendUniquePushRecords enforces the aggregate bound after deduplicating by commit SHA.
func appendUniquePushRecords(messages, found []string, seen map[string]bool) ([]string, error) {
	for _, record := range found {
		sha, _, _ := strings.Cut(record, commitFieldSep)
		if seen[sha] {
			continue
		}
		seen[sha] = true
		if len(messages) == maxAnalyzedCommits {
			return nil, fmt.Errorf("pushed ranges exceed maximum of %d commits", maxAnalyzedCommits)
		}
		messages = append(messages, record)
	}
	return messages, nil
}
