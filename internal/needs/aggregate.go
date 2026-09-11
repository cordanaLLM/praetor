package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/util"
)

// AggregateFleet scans all Go repositories in fleetRoot and produces a FleetDemandReport.
func AggregateFleet(ctx context.Context, fleetRoot, frameworkPath string) (*FleetDemandReport, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	fwIndex, err := InspectFramework(ctx, frameworkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect framework: %w", err)
	}

	repoDirs, err := discoverGoRepos(ctx, fleetRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Go repositories: %w", err)
	}

	report := &FleetDemandReport{
		GeneratedAt:         time.Now().UTC(),
		FleetRoot:           fleetRoot,
		Framework:           fwIndex.Name,
		TotalRepositories:   len(repoDirs),
		DemandFrequency:     make(map[CapabilityKey]int),
		CapabilityConsumers: make(map[CapabilityKey][]string),
	}

	gapPackages := make(map[CapabilityKey]map[string]struct{})
	for _, dir := range repoDirs {
		processRepoForAggregate(ctx, dir, report, gapPackages)
	}

	compileGapsAndLeaderboard(report, gapPackages)
	return report, nil
}

// discoverGoRepos searches up to depth 5 for directories containing go.mod.
func discoverGoRepos(ctx context.Context, root string) ([]string, error) {
	var repoDirs []string
	maxDepth := 5

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil && strings.Count(rel, string(os.PathSeparator)) > maxDepth {
			return filepath.SkipDir
		}
		if shouldSkipDir(info, path) {
			return filepath.SkipDir
		}
		if !info.IsDir() && info.Name() == "go.mod" {
			repoDirs = append(repoDirs, filepath.Dir(path))
		}
		return nil
	})

	return repoDirs, err
}

// processRepoForAggregate scans an individual repo and updates fleet aggregation counters.
func processRepoForAggregate(ctx context.Context, dir string, report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) {
	needs, err := ScanRepo(ctx, dir)
	if err != nil {
		return
	}

	report.ScannedRepositories++
	report.Leaderboard = append(report.Leaderboard, *needs)

	for _, dep := range needs.Dependencies {
		report.DemandFrequency[dep.Capability]++
		report.CapabilityConsumers[dep.Capability] = appendUnique(report.CapabilityConsumers[dep.Capability], needs.Repository)

		if dep.Status == StatusGap {
			if _, ok := gapPackages[dep.Capability]; !ok {
				gapPackages[dep.Capability] = make(map[string]struct{})
			}
			gapPackages[dep.Capability][dep.Package] = struct{}{}
		}
	}
}

// compileGapsAndLeaderboard sorts leaderboard and formats gap details.
func compileGapsAndLeaderboard(report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) {
	sort.Slice(report.Leaderboard, func(i, j int) bool {
		if report.Leaderboard[i].Readiness.Score == report.Leaderboard[j].Readiness.Score {
			return report.Leaderboard[i].Repository < report.Leaderboard[j].Repository
		}
		return report.Leaderboard[i].Readiness.Score > report.Leaderboard[j].Readiness.Score
	})

	for capKey, pkgsMap := range gapPackages {
		consumers := report.CapabilityConsumers[capKey]
		var pkgs []string
		for pkg := range pkgsMap {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)

		report.Gaps = append(report.Gaps, GapDetail{
			Capability:    capKey,
			ConsumerCount: len(consumers),
			Consumers:     consumers,
			PackagesUsed:  pkgs,
		})
	}

	sort.Slice(report.Gaps, func(i, j int) bool {
		return report.Gaps[i].ConsumerCount > report.Gaps[j].ConsumerCount
	})

	calculateFleetCoverage(report)
}

// calculateFleetCoverage computes the aggregate fleet-wide dependency coverage percentage.
func calculateFleetCoverage(report *FleetDemandReport) {
	totalDeps := 0
	coveredDeps := 0

	for _, repo := range report.Leaderboard {
		totalDeps += repo.Readiness.TotalThirdPartyDeps
		coveredDeps += repo.Readiness.CoveredDeps
	}

	if totalDeps > 0 {
		report.OverallFleetCoverage = (float64(coveredDeps) / float64(totalDeps)) * 100.0
	} else {
		report.OverallFleetCoverage = 100.0
	}
}

// appendUnique appends a string to a slice if not already present.
func appendUnique(slice []string, val string) []string {
	for _, item := range slice {
		if item == val {
			return slice
		}
	}
	return append(slice, val)
}

// RenderFrameworkDemandMarkdown outputs the FleetDemandReport in Keep-a-Changelog compatible markdown.
func RenderFrameworkDemandMarkdown(report *FleetDemandReport) string {
	var sb strings.Builder

	sb.WriteString("# Framework Demand & Capability Report\n\n")
	sb.WriteString(fmt.Sprintf("**Target Framework**: `%s`  \n", report.Framework))
	sb.WriteString(fmt.Sprintf("**Generated At**: %s  \n", report.GeneratedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Repositories Scanned**: %d / %d  \n", report.ScannedRepositories, report.TotalRepositories))
	sb.WriteString(fmt.Sprintf("**Overall Fleet Golusoris Coverage**: %.1f%%\n\n", report.OverallFleetCoverage))

	sb.WriteString("## Fleet Demand Topography\n\n")
	sb.WriteString("| Capability | Demand Frequency | Consumer Repositories |\n")
	sb.WriteString("| :--- | :--- | :--- |\n")

	type capFreq struct {
		key   CapabilityKey
		count int
	}
	var freqs []capFreq
	for k, v := range report.DemandFrequency {
		freqs = append(freqs, capFreq{k, v})
	}
	sort.Slice(freqs, func(i, j int) bool {
		return freqs[i].count > freqs[j].count
	})

	for _, f := range freqs {
		consumers := report.CapabilityConsumers[f.key]
		shortConsumers := make([]string, len(consumers))
		for i, c := range consumers {
			shortConsumers[i] = util.CleanGitURL(c)
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d | %s |\n", f.key, f.count, strings.Join(shortConsumers, ", ")))
	}

	sb.WriteString("\n## High-Priority Framework Gaps\n\n")
	if len(report.Gaps) == 0 {
		sb.WriteString("Zero gaps identified! All downstream dependencies are covered by Golusoris.\n")
	} else {
		sb.WriteString("| Capability Gap | Impacted Repos | Underlying Packages |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, g := range report.Gaps {
			sb.WriteString(fmt.Sprintf("| `%s` | %d | `%s` |\n", g.Capability, g.ConsumerCount, strings.Join(g.PackagesUsed, "`, `")))
		}
	}

	sb.WriteString("\n## Migration Readiness Leaderboard\n\n")
	sb.WriteString("| Rank | Repository | Readiness Score | Covered Deps | Gaps |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	for i, repo := range report.Leaderboard {
		sb.WriteString(fmt.Sprintf("| #%d | `%s` | %.1f%% | %d | %d |\n",
			i+1, repo.Repository, repo.Readiness.Score, repo.Readiness.CoveredDeps, repo.Readiness.GapDeps))
	}

	return sb.String()
}
