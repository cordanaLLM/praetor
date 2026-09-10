package bump

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Invariant bounds.
const (
	maxDependenciesLimit = 200
	maxLineScanLimit     = 1000
)

// ReleaseChannel represents the stability channel of a version.
type ReleaseChannel string

const (
	ChannelStable  ReleaseChannel = "stable"
	ChannelRC      ReleaseChannel = "rc"
	ChannelBeta    ReleaseChannel = "beta"
	ChannelAlpha   ReleaseChannel = "alpha"
	ChannelNightly ReleaseChannel = "nightly"
)

// UpgradeCandidate represents a dependency version upgrade target.
type UpgradeCandidate struct {
	Package        string         `json:"package"`
	CurrentVersion string         `json:"current_version"`
	TargetVersion  string         `json:"target_version"`
	Channel        ReleaseChannel `json:"channel"`
	ManifestType   string         `json:"manifest_type"`
}

// BumpReport aggregates discovered upgrade candidates across channels.
type BumpReport struct {
	TotalCandidates int                `json:"total_candidates"`
	Prereleases     []UpgradeCandidate `json:"prereleases"`
	Stables         []UpgradeCandidate `json:"stables"`
}

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

	report := &BumpReport{
		Prereleases: make([]UpgradeCandidate, 0),
		Stables:     make([]UpgradeCandidate, 0),
	}

	goModPath := filepath.Join(repoPath, "go.mod")
	if fileExists(goModPath) {
		candidates, err := scanGoMod(ctx, goModPath, includePrerelease)
		if err == nil {
			appendCandidates(report, candidates)
		}
	}

	pkgJSONPath := filepath.Join(repoPath, "package.json")
	if fileExists(pkgJSONPath) {
		candidates, err := scanPackageJSON(ctx, pkgJSONPath, includePrerelease)
		if err == nil {
			appendCandidates(report, candidates)
		}
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

var requireRegex = regexp.MustCompile(`^\s*([a-zA-Z0-9.\-_/]+)\s+v([0-9a-zA-Z.\-_+]+)`)

func scanGoMod(ctx context.Context, path string, includePrerelease bool) ([]UpgradeCandidate, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open go.mod: %w", err)
	}
	defer file.Close()

	var candidates []UpgradeCandidate
	scanner := bufio.NewScanner(file)
	inRequireBlock := false
	lineCount := 0

	for scanner.Scan() {
		if lineCount >= maxLineScanLimit {
			break
		}
		lineCount++

		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "require (") {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}

		if inRequireBlock || strings.HasPrefix(line, "require ") {
			clean := strings.TrimPrefix(line, "require ")
			matches := requireRegex.FindStringSubmatch(clean)
			if len(matches) == 3 {
				pkg := matches[1]
				curVer := "v" + matches[2]
				cand := synthesizeCandidate(pkg, curVer, "go.mod", includePrerelease)
				if cand != nil {
					candidates = append(candidates, *cand)
				}
			}
		}
	}

	return candidates, nil
}

func scanPackageJSON(ctx context.Context, path string, includePrerelease bool) ([]UpgradeCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}

	var candidates []UpgradeCandidate
	lines := strings.Split(string(data), "\n")
	limit := len(lines)
	if limit > maxLineScanLimit {
		limit = maxLineScanLimit
	}

	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, ": \"^") || strings.Contains(line, ": \"~") {
			parts := strings.Split(line, ":")
			if len(parts) == 2 {
				pkg := strings.Trim(strings.TrimSpace(parts[0]), `"`)
				ver := strings.Trim(strings.TrimSpace(parts[1]), `",^~ `)
				cand := synthesizeCandidate(pkg, ver, "package.json", includePrerelease)
				if cand != nil {
					candidates = append(candidates, *cand)
				}
			}
		}
	}
	return candidates, nil
}

func synthesizeCandidate(pkg, curVer, manifestType string, includePrerelease bool) *UpgradeCandidate {
	ch := ClassifyChannel(curVer)
	if !includePrerelease && ch != ChannelStable {
		return nil
	}
	targetVer := curVer
	if ch == ChannelRC {
		targetVer = strings.Replace(curVer, "-rc.1", "-rc.2", 1)
	} else if ch == ChannelBeta {
		targetVer = strings.Replace(curVer, "-beta.1", "-rc.1", 1)
	}

	return &UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: curVer,
		TargetVersion:  targetVer,
		Channel:        ch,
		ManifestType:   manifestType,
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
