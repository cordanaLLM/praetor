package bump

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/semver"
)

// CatalogEntry is one FleetCatalog pin: the fleet-unified version and the date (YYYY-MM-DD)
// it was last checked against the package's upstream release.
type CatalogEntry struct {
	Version  string
	Verified string
}

// catalogVerifiedOn is the date every entry below was last checked against upstream
// (proxy.golang.org @latest for Go modules, the npm registry "latest" dist-tag for Node
// packages). An entry re-checked on its own gets its own date instead.
const catalogVerifiedOn = "2026-09-26"

// maxCatalogAge bounds how long a FleetCatalog entry may go unverified. TestFleetCatalogVerified
// fails once any entry's Verified date is older, so a stale pin becomes a scheduled refresh
// instead of silent drift (issue #337).
const maxCatalogAge = 90 * 24 * time.Hour

// FleetCatalog provides canonical, fleet-unified dependency versions.
var FleetCatalog = map[string]CatalogEntry{
	// Go core standards
	"gopkg.in/yaml.v3":            {Version: "v3.0.1", Verified: catalogVerifiedOn},
	"github.com/google/uuid":      {Version: "v1.6.0", Verified: catalogVerifiedOn},
	"github.com/spf13/cobra":      {Version: "v1.10.2", Verified: catalogVerifiedOn},
	"github.com/stretchr/testify": {Version: "v1.12.1", Verified: catalogVerifiedOn},
	"golang.org/x/sync":           {Version: "v0.23.0", Verified: catalogVerifiedOn},
	"golang.org/x/sys":            {Version: "v0.48.0", Verified: catalogVerifiedOn},
	"golang.org/x/crypto":         {Version: "v0.57.0", Verified: catalogVerifiedOn},
	"golang.org/x/text":           {Version: "v0.42.0", Verified: catalogVerifiedOn},
	"google.golang.org/protobuf":  {Version: "v1.36.12", Verified: catalogVerifiedOn},
	"google.golang.org/grpc":      {Version: "v1.84.0", Verified: catalogVerifiedOn},
	"go.uber.org/zap":             {Version: "v1.28.0", Verified: catalogVerifiedOn},
	"github.com/sirupsen/logrus":  {Version: "v1.10.2", Verified: catalogVerifiedOn},

	// Node / TypeScript / Svelte standards
	"typescript":  {Version: "^7.0.2", Verified: catalogVerifiedOn},
	"svelte":      {Version: "^5.57.1", Verified: catalogVerifiedOn},
	"@types/node": {Version: "^26.6.3", Verified: catalogVerifiedOn},
	"vite":        {Version: "^8.3.1", Verified: catalogVerifiedOn},
	"prettier":    {Version: "^3.9.9", Verified: catalogVerifiedOn},
	"eslint":      {Version: "^10.11.0", Verified: catalogVerifiedOn},
}

// CatalogDrift is a dependency that differs from its FleetCatalog pin but is not an upgrade:
// the repository is ahead of the catalog, or one side is not a SemVer version.
type CatalogDrift struct {
	Package        string `json:"package"`
	CurrentVersion string `json:"current_version"`
	CatalogVersion string `json:"catalog_version"`
	ManifestType   string `json:"manifest_type"`
	ModuleDir      string `json:"module_dir"`
}

// CatalogReport splits catalog drift by direction. Only Upgrades are ever applied; Ahead and
// Unranked are reported and left unchanged, because moving them to the catalog pin would
// downgrade the repository or rest on an ordering that cannot be established.
type CatalogReport struct {
	// Upgrades are dependencies strictly older than their catalog pin.
	Upgrades []UpgradeCandidate `json:"upgrades"`
	// Ahead are dependencies strictly newer than their catalog pin.
	Ahead []CatalogDrift `json:"ahead"`
	// Unranked are dependencies whose version or pin does not parse as SemVer
	// (a workspace: protocol, a dist-tag, a compound range).
	Unranked []CatalogDrift `json:"unranked"`
}

// ReconcileCatalog returns the dependencies in repoPath that are behind the FleetCatalog.
// A dependency ahead of the catalog is never returned: see ReconcileCatalogReport.
func ReconcileCatalog(ctx context.Context, repoPath string) ([]UpgradeCandidate, error) {
	report, err := ReconcileCatalogReport(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return report.Upgrades, nil
}

// ReconcileCatalogReport compares the dependencies in repoPath with the FleetCatalog by SemVer
// precedence and reports each differing dependency as an upgrade, ahead, or unranked.
//
// Its input is the whole declared inventory (scanInventory), not only the dependencies with
// an upstream upgrade: a dependency already at upstream latest can still be behind or ahead
// of its pin, and the set compared is the same online and offline.
func ReconcileCatalogReport(ctx context.Context, repoPath string) (*CatalogReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("reconcile catalog: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reconcile catalog cancelled: %w", err)
	}
	discovered, err := scanInventory(ctx, repoPath, ScanOptions{})
	if err != nil {
		return nil, err
	}
	report := reconcileWithCatalog(discovered, FleetCatalog)
	return &report, nil
}

// reconcileWithCatalog classifies each discovered dependency against catalog. The input is
// already bounded by the scanners (maxManifestDependencies per package.json section,
// MaxManifestLines per go.mod); the loop walks it once.
func reconcileWithCatalog(discovered []UpgradeCandidate, catalog map[string]CatalogEntry) CatalogReport {
	report := CatalogReport{Upgrades: []UpgradeCandidate{}, Ahead: []CatalogDrift{}, Unranked: []CatalogDrift{}}
	for _, c := range discovered {
		entry, found := catalog[c.Package]
		if !found {
			continue
		}
		drift := CatalogDrift{
			Package: c.Package, CurrentVersion: c.CurrentVersion, CatalogVersion: entry.Version,
			ManifestType: c.ManifestType, ModuleDir: c.ModuleDir,
		}
		current, currentOK := catalogVersion(c.CurrentVersion)
		pinned, pinnedOK := catalogVersion(entry.Version)
		if !currentOK || !pinnedOK {
			report.Unranked = append(report.Unranked, drift)
			continue
		}
		switch semver.Compare(current, pinned) {
		case -1:
			report.Upgrades = append(report.Upgrades, UpgradeCandidate{
				Package: c.Package, CurrentVersion: c.CurrentVersion, TargetVersion: entry.Version,
				Channel: ChannelStable, ManifestType: c.ManifestType, ModuleDir: c.ModuleDir,
			})
		case 1:
			report.Ahead = append(report.Ahead, drift)
		}
	}
	return report
}

// catalogVersion parses a catalog pin or a discovered version for ordering. One leading caret
// or tilde is a range operator whose floor is the version itself; any other range form
// (">=1.0.0", "1.x", "workspace:*") is not ranked.
func catalogVersion(raw string) (semver.Version, bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "^") || strings.HasPrefix(trimmed, "~") {
		trimmed = trimmed[1:]
	}
	return semver.Parse(trimmed)
}

// verifiedWithin reports an error when verified (YYYY-MM-DD) is not a date, lies after now, or
// is older than maxAge at now. It is the dated-assertion check for a hand-maintained currency
// table such as FleetCatalog.
func verifiedWithin(verified string, now time.Time, maxAge time.Duration) error {
	day, err := time.Parse(time.DateOnly, verified)
	if err != nil {
		return fmt.Errorf("verified date %q: %w", verified, err)
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if day.After(today) {
		return fmt.Errorf("verified date %s is after %s", verified, today.Format(time.DateOnly))
	}
	if age := today.Sub(day); age > maxAge {
		return fmt.Errorf("verified %s, %d days ago; the bound is %d days", verified, int(age.Hours()/24), int(maxAge.Hours()/24))
	}
	return nil
}
