package bump

import (
	"context"
	"fmt"
	"strings"
)

const (
	maxDependenciesLimit = 200
	maxLineScanLimit     = 1000
)

// ClassifyChannel determines the release channel from a SemVer string.
func ClassifyChannel(version string) ReleaseChannel {
	vLower := strings.ToLower(version)
	switch {
	case strings.Contains(vLower, "-rc"):
		return ChannelRC
	case strings.Contains(vLower, "-beta"):
		return ChannelBeta
	case strings.Contains(vLower, "-alpha"):
		return ChannelAlpha
	case strings.Contains(vLower, "-nightly") || strings.Contains(vLower, "-dev") || strings.Contains(vLower, "-preview"):
		return ChannelNightly
	default:
		return ChannelStable
	}
}

// ScanDependencies scans manifests in repoPath for dependencies and available bumps.
func ScanDependencies(ctx context.Context, repoPath string, includePrerelease bool) (*BumpReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("bump: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("bump cancelled: %w", err)
	}

	opts := ScanOptions{
		IncludePrerelease: includePrerelease,
		MaxCandidates:     maxDependenciesLimit,
	}

	report := &BumpReport{
		Prereleases: make([]UpgradeCandidate, 0),
		Stables:     make([]UpgradeCandidate, 0),
	}

	// 1. Go dependencies
	goCandidates, err := ScanGoDependencies(ctx, repoPath, opts)
	if err == nil {
		appendCandidates(report, goCandidates)
	}

	// 2. Node dependencies
	nodeCandidates, err := ScanNodeDependencies(ctx, repoPath, opts)
	if err == nil {
		appendCandidates(report, nodeCandidates)
	}

	report.TotalCandidates = len(report.Prereleases) + len(report.Stables)
	return report, nil
}

func appendCandidates(report *BumpReport, candidates []UpgradeCandidate) {
	limit := len(candidates)
	if limit > maxDependenciesLimit {
		limit = maxDependenciesLimit
	}

	for i := 0; i < limit; i++ {
		c := candidates[i]
		if c.Channel == ChannelStable {
			report.Stables = append(report.Stables, c)
		} else {
			report.Prereleases = append(report.Prereleases, c)
		}
	}
}
