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
	WorkflowFile string `json:"workflow_file"`
	// Line is the 1-based line of the first uses: line in WorkflowFile with this reference.
	Line   int    `json:"line,omitempty"`
	Action string `json:"action"`
	// CurrentVersion is the tag or branch the reference names. For a SHA pin it is the
	// release the pin's trailing comment names, or the SHA itself when it names none.
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	// UpToDate is ActionPinCurrent(CurrentVersion, LatestVersion): the pin compared by
	// SemVer at its own precision, never by raw string equality. It is false for a SHA pin
	// that names no release, and VerifyActionPins clears it for a SHA pin whose commit it
	// could not confirm as its release's.
	UpToDate   bool   `json:"up_to_date"`
	Deprecated bool   `json:"deprecated"`
	Warning    string `json:"warning,omitempty"`
	// PinnedSHA is the full commit SHA a SHA-pinned reference runs; empty for a tag or
	// branch reference.
	PinnedSHA string `json:"pinned_sha,omitempty"`
	// Pin is the verdict on PinnedSHA and PinDetail its reason; both are empty for a
	// reference not pinned by SHA.
	Pin       PinStatus `json:"pin_status,omitempty"`
	PinDetail string    `json:"pin_detail,omitempty"`
}

// PinStatus is what an action's upstream repository says about a SHA pin (VerifyActionPins).
type PinStatus string

const (
	// PinVerified: the release the pin's comment names points at the pinned commit.
	PinVerified PinStatus = "verified"
	// PinUnverified: the upstream was not asked or did not answer (offline, rate limited, no
	// token, a repository it does not show), so the pin is never reported up to date.
	PinUnverified PinStatus = "unverified"
	// PinUnversioned: the pin names no release, and the upstream has the commit or was not
	// asked; there is no release to compare with the registry.
	PinUnversioned PinStatus = "unversioned"
	// PinCommitMissing: the upstream has no such commit, so the job stops at setup.
	PinCommitMissing PinStatus = "commit-missing"
	// PinReleaseMismatch: the release the pin's comment names is not the pinned commit: its
	// tag points at another commit, or the upstream has no such tag.
	PinReleaseMismatch PinStatus = "release-mismatch"
)

// DeprecationWarning details runtime or ecosystem deprecations.
type DeprecationWarning struct {
	Component string `json:"component"`
	Kind      string `json:"kind"` // "runner-node20", "unsupported-manifest", etc.
	Details   string `json:"details"`
}

// VersionAuditReport aggregates health and modernization status across all dependencies.
// TotalScanned counts every declared dependency and workflow action examined, up-to-date
// ones included, whether or not the upstream report was reachable. ModernizationScore and
// PendingUpgrades reflect only the upgrades that report named: offline no upgrade target is
// known, so an outdated dependency counts as up to date. UpToDate leaves out every pending
// upgrade and every workflow action row that is drifted or deprecated. Passed is false when
// the report holds any deprecation; `bump audit` then exits non-zero and
// standards_version_audit returns an error result.
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
