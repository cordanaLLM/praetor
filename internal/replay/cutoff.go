// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// commitStamp pairs a commit object ID with its committer timestamp.
type commitStamp struct {
	sha     string
	parents []string
	time    time.Time
}

// resolveBaseTip resolves base (or the repository default branch when empty) to a commit OID.
func resolveBaseTip(ctx context.Context, repoDir, base string) (string, string, error) {
	if strings.TrimSpace(base) == "" {
		resolved, err := forge.RepositoryDefaultBranch(ctx, repoDir, nil)
		if err != nil {
			return "", "", fmt.Errorf("resolve default branch: %w", err)
		}
		base = resolved
	}
	tip, err := util.ResolveGitCommit(ctx, repoDir, base)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if tip == "" {
		return "", "", fmt.Errorf("%w: base %q does not resolve to a commit", ErrRefused, base)
	}
	return base, tip, nil
}

// Cutoff returns the last commit on base with committer timestamp at or before cutoff.
func Cutoff(ctx context.Context, repoDir, base string, cutoff time.Time) (string, error) {
	if ctx == nil {
		return "", errors.New("cutoff requires a context")
	}
	if strings.TrimSpace(repoDir) == "" || cutoff.IsZero() {
		return "", errors.New("cutoff requires a repository path and nonzero cutoff timestamp")
	}
	cutoffUTC := cutoff.UTC()
	baseName, tip, err := resolveBaseTip(ctx, repoDir, base)
	if err != nil {
		return "", err
	}
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "log", "--first-parent", "--format=%H%x09%ct", "-n1", "--until="+strconv.FormatInt(cutoff.Unix(), 10), tip, "--")
	if err != nil {
		return "", fmt.Errorf("inspect cutoff history on %s: %w", baseName, err)
	}
	stamps, err := parseCommitStamps(string(res.Stdout), false)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(stamps) && i < 1; i++ {
		if !stamps[i].time.After(cutoffUTC) {
			return stamps[i].sha, nil
		}
	}
	return "", fmt.Errorf("%w: no commit on %s at or before %s", ErrRefused, baseName, cutoffUTC.Format(time.RFC3339))
}

// VerifyTree refuses a checkout whose HEAD or ancestor history has a committer date after cutoff.
func VerifyTree(ctx context.Context, treeDir string, cutoff time.Time) error {
	if ctx == nil {
		return errors.New("verify tree requires a context")
	}
	if strings.TrimSpace(treeDir) == "" || cutoff.IsZero() {
		return errors.New("verify tree requires a directory and nonzero cutoff timestamp")
	}
	return verifyCommitHistory(ctx, treeDir, "HEAD", cutoff)
}

// verifyCommitHistory checks only post-cutoff ancestors, including nonmonotonic parent dates.
func verifyCommitHistory(ctx context.Context, repoDir, sha string, cutoff time.Time) error {
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "log", "--format=%H%x09%ct", "-n1",
		"--since-as-filter="+strconv.FormatInt(cutoff.Unix()+1, 10), sha, "--")
	if err != nil {
		return fmt.Errorf("inspect cutoff history: %w", err)
	}
	stamps, err := parseCommitStamps(string(res.Stdout), false)
	if err != nil {
		return err
	}
	if len(stamps) > 0 {
		return refusedf(ErrLeak, "commit %s committer date %s is after cutoff %s", stamps[0].sha,
			stamps[0].time.Format(time.RFC3339), cutoff.UTC().Format(time.RFC3339))
	}
	return nil
}

// parseCommitStamps decodes tab-separated git log lines ("%H\t%ct" or "%H\t%P\t%ct").
func parseCommitStamps(raw string, withParents bool) ([]commitStamp, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > maxScanLines {
		return nil, refusedf(ErrLimitExceeded, "git log output exceeds %d lines", maxScanLines)
	}
	out := make([]commitStamp, 0, len(lines))
	for i := 0; i < len(lines) && i < maxScanLines; i++ {
		stamp, err := parseOneCommitStamp(lines[i], withParents)
		if err != nil {
			return nil, err
		}
		out = append(out, stamp)
	}
	return out, nil
}

// parseOneCommitStamp parses a single commit timestamp record from git log output.
func parseOneCommitStamp(line string, withParents bool) (commitStamp, error) {
	parts := strings.Split(strings.TrimSpace(line), "\t")
	want := 2
	if withParents {
		want = 3
	}
	if len(parts) != want || !util.ValidGitObjectID(parts[0]) {
		return commitStamp{}, fmt.Errorf("malformed git log stamp %q", line)
	}
	secField := parts[len(parts)-1]
	sec, err := strconv.ParseInt(secField, 10, 64)
	if err != nil {
		return commitStamp{}, fmt.Errorf("parse commit timestamp %q: %w", secField, err)
	}
	var parents []string
	if withParents && parts[1] != "" {
		parents = strings.Fields(parts[1])
	}
	return commitStamp{sha: parts[0], parents: parents, time: time.Unix(sec, 0).UTC()}, nil
}
