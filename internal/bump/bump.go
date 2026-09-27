package bump

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/semver"
)

const (
	// maxDependenciesLimit caps the upgrade candidates one BumpReport carries.
	maxDependenciesLimit = 200
	// maxManifestDependencies is the scalar upper bound (HISS-02) on the entries one
	// package.json dependency section or one pnpm outdated report may carry. A larger one
	// is refused rather than truncated, so a scan never reports a partial inventory as whole.
	maxManifestDependencies = 1000
)

// channelMarkers maps prerelease markers to their channel, most specific first. Every
// marker not listed as rc, beta or alpha is a nightly-grade build.
var channelMarkers = [...]struct {
	marker  string
	channel ReleaseChannel
}{
	{"rc", ChannelRC},
	{"beta", ChannelBeta},
	{"alpha", ChannelAlpha},
	{"nightly", ChannelNightly},
	{"dev", ChannelNightly},
	{"preview", ChannelNightly},
	{"next", ChannelNightly},
	{"canary", ChannelNightly},
	{"snapshot", ChannelNightly},
	{"pre", ChannelNightly},
}

// ClassifyChannel determines the release channel of a version string.
//
// A SemVer version is stable only without a prerelease component: "1.2.3-next.1",
// "-canary", "-pre", "-snapshot" and Go pseudo-versions are prereleases whatever their
// marker says, so none of them reaches the stable channel and slips past
// IncludePrerelease (BUG-421). A known leading marker picks rc, beta or alpha; any other
// prerelease is nightly. A string that is not SemVer falls back to the marker table,
// matched as "-<marker>", and is stable when none matches.
func ClassifyChannel(version string) ReleaseChannel {
	if v, ok := semver.Parse(version); ok {
		if !v.IsPrerelease() {
			return ChannelStable
		}
		return prereleaseChannel(strings.ToLower(v.Prerelease))
	}
	vLower := strings.ToLower(version)
	for i := 0; i < len(channelMarkers); i++ {
		if strings.Contains(vLower, "-"+channelMarkers[i].marker) {
			return channelMarkers[i].channel
		}
	}
	return ChannelStable
}

// prereleaseChannel classifies a SemVer prerelease component by its leading marker.
func prereleaseChannel(prerelease string) ReleaseChannel {
	for i := 0; i < len(channelMarkers); i++ {
		if strings.HasPrefix(prerelease, channelMarkers[i].marker) {
			return channelMarkers[i].channel
		}
	}
	return ChannelNightly
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
// no upgrade, is overlaid; it never pivots back to the manifest-only result. held names the
// declared entries the report never replaces (see overlayUpgrades); nil holds none.
func manifestInventory(ctx context.Context, declare, report func() ([]UpgradeCandidate, error), held func(UpgradeCandidate) bool) ([]UpgradeCandidate, error) {
	declared, err := declare()
	if err != nil || len(declared) == 0 {
		return declared, err
	}
	upstream, reportErr := report()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reportErr == nil {
		declared = overlayUpgrades(declared, upstream, held)
	}
	return declared, nil
}

// overlayUpgrades returns the declared inventory with each dependency the upstream report
// also names replaced by the report's entry, which carries the resolved current version
// and any admitted upgrade target. Entries the report names but the manifest does not
// declare (a transitive module, another workspace member's dependency) are not part of
// this manifest's inventory and are dropped, so the inventory is the same set online and
// offline. A declared entry held reports true for keeps its declared form even when the
// report names it: the ecosystem's rule for a declaration the report's versions do not
// describe, such as a package.json comparator range (unrankedNodeRange).
func overlayUpgrades(declared, report []UpgradeCandidate, held func(UpgradeCandidate) bool) []UpgradeCandidate {
	byPackage := make(map[string]UpgradeCandidate, len(report))
	for _, entry := range report {
		byPackage[entry.Package] = entry
	}
	inventory := make([]UpgradeCandidate, 0, len(declared))
	for _, dep := range declared {
		if entry, ok := byPackage[dep.Package]; ok && (held == nil || !held(dep)) {
			dep = entry
		}
		inventory = append(inventory, dep)
	}
	return inventory
}
