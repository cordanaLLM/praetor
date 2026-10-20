// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

func (c *Collector) transcriptJoin(ctx context.Context) (*transcriptJoin, []string, error) {
	roots := make(map[string]string)
	notes := []string{}
	trees, err := worktree.NewManager(c.opts.Root).List(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		notes = append(notes, fmt.Sprintf("live worktrees unavailable: %v; using recorded transcript creations only", err))
	}
	for _, tree := range trees {
		roots[tree.Path] = tree.Branch
	}
	branch := c.opts.DefaultBranch
	if branch == "" {
		var ok bool
		branch, ok, err = util.ReadOriginHeadBranch(ctx, c.opts.Root)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			branch = "main"
			notes = append(notes, "origin default branch unavailable; treating main as default (set repository.default_branch to override)")
		}
	}
	return newTranscriptJoin(roots, branch), notes, nil
}

func (c *Collector) applyTranscriptCoverage(unit *UnitReport, stats *BranchTranscriptStats, src unitSources) {
	if !src.measured.Transcripts {
		return
	}
	share := TranscriptShare{}
	if stats != nil {
		share = stats.Unattributed
		share.add(stats, false)
	}
	unit.TranscriptCoverage = &share
	if unit.FrontierTokensNum != nil {
		return
	}
	reason := transcriptMeasurementReason(stats)
	if src.owners[unit.HeadBranch] != unit.PullRequestNumber {
		reason = "reused head branch belongs to the latest merged pull request"
	}
	unit.TokenMeasurementReason = reason
}

func transcriptMeasurementReason(stats *BranchTranscriptStats) string {
	if stats != nil && stats.AmbiguousResponses > 0 {
		return "ambiguous: responses named this branch only together with other branches; usage counted in the unattributed share"
	}
	if stats == nil || !stats.WorktreeResolved {
		return "worktree never resolved: neither a live worktree nor a recorded creation resolved the pull request head branch; no transcript response named a path under its worktrees"
	}
	return "no transcript response joined the pull request head branch via gitBranch or an unambiguous worktree path"
}
