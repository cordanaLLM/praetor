package bump

import (
	"context"
	"fmt"
	"strings"
)

const (
	// maxDependenciesLimit caps the upgrade candidates one BumpReport carries.
	maxDependenciesLimit = 200
	// maxManifestDependencies is the scalar upper bound (HISS-02) on the entries one
	// package.json dependency section or one pnpm outdated report may carry. A larger one
	// is refused rather than truncated, so a scan never reports a partial inventory as whole.
	maxManifestDependencies = 1000
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

// upgradeAllowed reports whether the channel policy in opts admits target as an upgrade.
func upgradeAllowed(target string, opts ScanOptions) bool {
	return opts.IncludePrerelease || ClassifyChannel(target) == ChannelStable
}

// ScanDependencies scans manifests in repoPath for dependencies and available bumps.
//
// TotalScanned counts every declared Go and Node dependency the scan examined, whether or
// not an upgrade exists for it; Stables and Prereleases hold only the dependencies whose
// target differs from their current version. An offline scan therefore reports the same
// inventory with no candidates instead of turning every dependency into a phantom upgrade.
func ScanDependencies(ctx context.Context, repoPath string, includePrerelease bool) (*BumpReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("bump: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("bump cancelled: %w", err)
	}

	inventory, err := scanInventory(ctx, repoPath, ScanOptions{IncludePrerelease: includePrerelease})
	if err != nil {
		return nil, err
	}
	report := &BumpReport{
		TotalScanned: len(inventory),
		Prereleases:  make([]UpgradeCandidate, 0),
		Stables:      make([]UpgradeCandidate, 0),
	}
	appendCandidates(report, inventory)
	report.TotalCandidates = len(report.Prereleases) + len(report.Stables)
	return report, nil
}

// languageScanner names one ecosystem scanner so its failure says which one failed.
type languageScanner struct {
	name string
	scan func(context.Context, string, ScanOptions) ([]UpgradeCandidate, error)
}

// languageScanners are the ecosystems whose declared dependencies bump examines.
var languageScanners = []languageScanner{
	{name: "Go", scan: ScanGoDependencies},
	{name: "Node", scan: ScanNodeDependencies},
}

// scanInventory returns every declared dependency of every scanned ecosystem, up-to-date
// ones included. It is the one inventory the scan report, the audit and the catalog share.
func scanInventory(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error) {
	var inventory []UpgradeCandidate
	for _, scanner := range languageScanners {
		found, err := scanner.scan(ctx, repoPath, opts)
		if err != nil {
			return nil, fmt.Errorf("scan %s dependencies: %w", scanner.name, err)
		}
		inventory = append(inventory, found...)
	}
	return inventory, nil
}

// pendingUpgrades returns the inventory entries whose target differs from their current
// version: the only entries that are upgrade candidates.
func pendingUpgrades(inventory []UpgradeCandidate) []UpgradeCandidate {
	var pending []UpgradeCandidate
	for _, c := range inventory {
		if c.CurrentVersion != c.TargetVersion {
			pending = append(pending, c)
		}
	}
	return pending
}

// appendCandidates files the inventory's upgrade candidates, at most maxDependenciesLimit
// of them, under their release channel. Up-to-date entries are never candidates.
func appendCandidates(report *BumpReport, inventory []UpgradeCandidate) {
	pending := pendingUpgrades(inventory)
	for _, c := range pending[:min(len(pending), maxDependenciesLimit)] {
		if c.Channel == ChannelStable {
			report.Stables = append(report.Stables, c)
		} else {
			report.Prereleases = append(report.Prereleases, c)
		}
	}
}

// manifestInventory is the inventory of one manifest. declare reads the dependencies the
// manifest declares; report asks the package manager which of them has an upgrade and is
// consulted only when something is declared. A report that could not be produced
// (offline, a missing tool, no go.sum) is not a scan failure: the declared dependencies
// are still the inventory, none with a known upgrade. A complete report, even one naming
// no upgrade, is overlaid; it never pivots back to the manifest-only result.
func manifestInventory(ctx context.Context, declare, report func() ([]UpgradeCandidate, error)) ([]UpgradeCandidate, error) {
	declared, err := declare()
	if err != nil || len(declared) == 0 {
		return declared, err
	}
	upstream, reportErr := report()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reportErr == nil {
		declared = overlayUpgrades(declared, upstream)
	}
	return declared, nil
}

// overlayUpgrades returns the declared inventory with each dependency the upstream report
// also names replaced by the report's entry, which carries the resolved current version
// and any admitted upgrade target. Entries the report names but the manifest does not
// declare (a transitive module, another workspace member's dependency) are not part of
// this manifest's inventory and are dropped, so the inventory is the same set online and
// offline.
func overlayUpgrades(declared, report []UpgradeCandidate) []UpgradeCandidate {
	byPackage := make(map[string]UpgradeCandidate, len(report))
	for _, entry := range report {
		byPackage[entry.Package] = entry
	}
	inventory := make([]UpgradeCandidate, 0, len(declared))
	for _, dep := range declared {
		if entry, ok := byPackage[dep.Package]; ok {
			dep = entry
		}
		inventory = append(inventory, dep)
	}
	return inventory
}
