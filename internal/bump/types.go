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

// BumpReport aggregates discovered upgrade candidates across channels.
type BumpReport struct {
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
type VersionAuditReport struct {
	TotalScanned       int                  `json:"total_scanned"`
	UpToDate           int                  `json:"up_to_date"`
	PendingUpgrades    []UpgradeCandidate   `json:"pending_upgrades"`
	Actions            []ActionCandidate    `json:"actions"`
	Deprecations       []DeprecationWarning `json:"deprecations"`
	ModernizationScore float64              `json:"modernization_score"`
	Passed             bool                 `json:"passed"`
}

// ScanOptions configures dependency discovery.
type ScanOptions struct {
	IncludePrerelease bool
	MaxCandidates     int
}

// DependencyScanner defines the interface for language-specific dependency inspectors.
type DependencyScanner interface {
	Scan(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error)
}
