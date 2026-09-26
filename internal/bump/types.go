package bump

import "context"

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
	ManifestType   string         `json:"manifest_type"` // "go.mod", "package.json"
	ModuleDir      string         `json:"module_dir"`    // Relative path to module directory (for go.work / workspaces)
}

// BumpReport aggregates discovered upgrade candidates across channels. TotalScanned counts
// every declared dependency examined, up-to-date ones included; TotalCandidates counts only
// the dependencies with an admitted upgrade target.
type BumpReport struct {
	TotalScanned    int                `json:"total_scanned"`
	TotalCandidates int                `json:"total_candidates"`
	Prereleases     []UpgradeCandidate `json:"prereleases"`
	Stables         []UpgradeCandidate `json:"stables"`
}

// ActionCandidate represents a GitHub Actions workflow action reference.
type ActionCandidate struct {
	WorkflowFile   string `json:"workflow_file"`
	Action         string `json:"action"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Deprecated     bool   `json:"deprecated"`
	Warning        string `json:"warning,omitempty"`
}

// DeprecationWarning details runtime or ecosystem deprecations.
type DeprecationWarning struct {
	Component string `json:"component"`
	Kind      string `json:"kind"` // "runner-node20", "unsupported-manifest", etc.
	Details   string `json:"details"`
}

// VersionAuditReport aggregates health and modernization status across all dependencies.
// TotalScanned counts every declared dependency and workflow action examined, up-to-date
// ones included, whether or not the upstream report was reachable.
type VersionAuditReport struct {
	TotalScanned       int                  `json:"total_scanned"`
	UpToDate           int                  `json:"up_to_date"`
	PendingUpgrades    []UpgradeCandidate   `json:"pending_upgrades"`
	Actions            []ActionCandidate    `json:"actions"`
	Deprecations       []DeprecationWarning `json:"deprecations"`
	ModernizationScore float64              `json:"modernization_score"`
	Passed             bool                 `json:"passed"`
}

// ScanOptions configures dependency discovery. IncludePrerelease admits prerelease upgrade
// targets; it never removes a dependency from the scanned inventory.
type ScanOptions struct {
	IncludePrerelease bool
}

// DependencyScanner defines the interface for language-specific dependency inspectors. Scan
// returns the declared-dependency inventory; entries whose target differs from their
// current version are the upgrade candidates.
type DependencyScanner interface {
	Scan(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error)
}
