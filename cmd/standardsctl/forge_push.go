// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/docstrings"
)

// forgeCheckCommitInput selects explicit history or every branch update named by git.
func forgeCheckCommitInput(ctx context.Context, repo, base, head string, prePush bool, excludes []string, firstParent bool) ([]string, error) {
	if !prePush {
		if base == "" {
			return nil, errors.New("usage: standardsctl forge check-commits --base=<ref> [--head=HEAD] or --pre-push")
		}
		return commitRange(ctx, repo, base, head, excludes, firstParent)
	}
	if base != "" || head != "HEAD" {
		return nil, errors.New("forge check-commits --pre-push requires HEAD and no explicit --base")
	}
	ranges, err := docstrings.ReadPrePushCommitRanges(ctx, os.Stdin)
	if err != nil {
		return nil, err
	}
	var messages []string
	for i := 0; i < len(ranges); i++ {
		r := ranges[i]
		rangeBase := r.RemoteOID
		if r.NewRef {
			rangeBase = ""
		}
		found, err := commitRange(ctx, repo, rangeBase, r.LocalOID, excludes, firstParent)
		if err != nil {
			return nil, err
		}
		if len(messages)+len(found) > maxAnalyzedCommits {
			return nil, fmt.Errorf("pushed ranges exceed maximum of %d commits", maxAnalyzedCommits)
		}
		messages = append(messages, found...)
	}
	return messages, nil
}
