// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/generated"
	"github.com/cordanaLLM/praetor/internal/util"
)

const gosecBaselineRule = "no first gosec baseline; existing gosec baseline only shrinks: fix every finding, mark its line with #nosec Gxxx -- reason, or declare a dated per-file gosec exception in .standards.yaml"

// gosecDefaultBaseline reads the default branch through the shared offline git readers.
// The remote-tracking branch takes precedence over a possibly edited local branch.
func gosecDefaultBaseline(ctx context.Context, root string) (*baseline.Baseline, error) {
	return defaultBranchBaseline(ctx, root, gating.GosecBaselineFile, nil)
}

func defaultBranchBaseline(ctx context.Context, root, file string, fallback *baseline.Baseline) (*baseline.Baseline, error) {
	if fallback != nil && !gitHasCommits(ctx, root) {
		return fallback, nil
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, root, nil)
	if err != nil {
		return nil, err
	}
	refs := []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch}
	for i := 0; i < len(refs); i++ {
		commit, err := util.ResolveGitCommit(ctx, root, refs[i])
		if err != nil {
			return nil, err
		}
		if commit != "" {
			return readCommittedBaseline(ctx, root, commit, file)
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return &baseline.Baseline{Absent: true}, nil
}

func readCommittedBaseline(ctx context.Context, root, commit, file string) (*baseline.Baseline, error) {
	top, err := gitTopLevel(ctx, root)
	if err != nil {
		return nil, err
	}
	rel, err := gitRelativePath(top, filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	data, found, err := generated.CommitTree(top, commit).Read(ctx, rel)
	if err != nil {
		return nil, err
	}
	if !found {
		return &baseline.Baseline{Absent: true}, nil
	}
	return parseClassBaseline(file, data)
}

func guardedGosecRecord(ctx context.Context, root string, previous *baseline.Baseline, findings []baseline.Infraction, opts baseline.RecordOptions) (*baseline.Baseline, error) {
	if opts.AllowIncrease {
		return nil, fmt.Errorf("%s; --allow-gosec-increase is refused", gosecBaselineRule)
	}
	committed, err := gosecDefaultBaseline(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("read default-branch gosec baseline: %w", err)
	}
	if err := baseline.CheckMonotonic(committed, &baseline.Baseline{TotalInfractions: len(findings)}); err != nil {
		return nil, fmt.Errorf("%s: %w", gosecBaselineRule, err)
	}
	if previous.Absent {
		previous = committed
	}
	next, err := baseline.Record(previous, findings, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", gosecBaselineRule, err)
	}
	return next, nil
}

func parseClassBaseline(file string, data []byte) (*baseline.Baseline, error) {
	b, err := baseline.ParseBaseline(data)
	if err != nil {
		return nil, err
	}
	return b, b.ValidateClass(file, baseline.ClassForFile(file))
}
