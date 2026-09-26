package needs

import (
	"os"
	"time"
)

const (
	// Fleet artifacts can contain private repository and dependency inventories.
	// New files and output directories remain accessible only to their owner.
	manifestFilePerm os.FileMode = 0o600
	outputDirPerm    os.FileMode = 0o700
)

// LanguageUnsupported is the language of a repository no signal identified. Such a
// repository is reported as skipped, never scored against a default framework.
const LanguageUnsupported = "unsupported"

// CapabilityKey represents a standardized architectural or runtime capability.
type CapabilityKey string

// CapabilityStatus describes whether a capability is supported by the target framework.
type CapabilityStatus string

const (
	StatusCovered          CapabilityStatus = "covered"
	StatusAdapterAvailable CapabilityStatus = "adapter_available"
	StatusGap              CapabilityStatus = "gap"
	StatusNative           CapabilityStatus = "native"
)

// LibraryRelationshipKind distinguishes retained libraries from migration candidates.
type LibraryRelationshipKind string

const (
	RelationshipFoundation LibraryRelationshipKind = "foundation"
	RelationshipWrappedBy  LibraryRelationshipKind = "wrapped-by"
	RelationshipTooling    LibraryRelationshipKind = "tooling"
)

// LibraryRelationship describes a library's role, not interchangeable APIs.
// FrameworkPackage is a related adapter, never an import-replacement instruction.
// Basis applies to this relationship; retained foundations remain catalog-declared.
type LibraryRelationship struct {
	Kind             LibraryRelationshipKind `json:"kind" yaml:"kind"`
	FrameworkPackage string                  `json:"framework_package,omitempty" yaml:"framework_package,omitempty"`
	Basis            string                  `json:"basis" yaml:"basis"`
}

// DependencyDemand captures a single dependency requirement and its framework mapping.
type DependencyDemand struct {
	Package              string               `json:"package" yaml:"package"`
	Version              string               `json:"version,omitempty" yaml:"version,omitempty"`
	Language             string               `json:"language,omitempty" yaml:"language,omitempty"`
	Ecosystem            string               `json:"ecosystem,omitempty" yaml:"ecosystem,omitempty"`
	Capability           CapabilityKey        `json:"capability" yaml:"capability"`
	Status               CapabilityStatus     `json:"status" yaml:"status"`
	GolusorisReplacement string               `json:"golusoris_replacement,omitempty" yaml:"golusoris_replacement,omitempty"`
	TargetBuilderKit     string               `json:"target_builder_kit,omitempty" yaml:"target_builder_kit,omitempty"`
	Notes                string               `json:"notes,omitempty" yaml:"notes,omitempty"`
	Relationship         *LibraryRelationship `json:"relationship,omitempty" yaml:"relationship,omitempty"`
}

// CapabilityDeclaration groups required and optional capabilities.
type CapabilityDeclaration struct {
	Required []CapabilityKey `json:"required" yaml:"required"`
	Optional []CapabilityKey `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// ReadinessMetrics summarizes dependency mapping availability, not migration safety.
// Basis distinguishes catalog declarations from observed source; tests are not run.
type ReadinessMetrics struct {
	Basis               string  `json:"basis,omitempty" yaml:"basis,omitempty"`
	Score               float64 `json:"score" yaml:"score"` // 0.0 - 100.0%
	TotalThirdPartyDeps int     `json:"total_third_party_deps" yaml:"total_third_party_deps"`
	CoveredDeps         int     `json:"covered_deps" yaml:"covered_deps"`
	GapDeps             int     `json:"gap_deps" yaml:"gap_deps"`
}

// RepoNeeds is the declarative manifest of a repository's framework needs (.needs.yaml).
type RepoNeeds struct {
	Version      int                   `json:"version" yaml:"version"`
	Repository   string                `json:"repository" yaml:"repository"`
	Language     string                `json:"language" yaml:"language"`
	Languages    []string              `json:"languages,omitempty" yaml:"languages,omitempty"`
	GoVersion    string                `json:"go_version,omitempty" yaml:"go_version,omitempty"`
	Framework    string                `json:"framework" yaml:"framework"`
	BuilderKits  []string              `json:"builder_kits,omitempty" yaml:"builder_kits,omitempty"`
	Capabilities CapabilityDeclaration `json:"capabilities" yaml:"capabilities"`
	Dependencies []DependencyDemand    `json:"dependencies" yaml:"dependencies"`
	// StandardLibraryImports contains selected catalog imports observed in Go source.
	// They never contribute to third-party dependency counts or migration candidates.
	StandardLibraryImports []DependencyDemand `json:"standard_library_imports,omitempty" yaml:"standard_library_imports,omitempty"`
	Readiness              ReadinessMetrics   `json:"readiness" yaml:"readiness"`
	UpdatedAt              time.Time          `json:"updated_at" yaml:"updated_at"`
	// Path is the repository root a fleet aggregation scanned this row at. It is a local
	// path and never written into a .needs.yaml manifest.
	Path string `json:"path,omitempty" yaml:"-"`
	// Subprojects lists, relative to the repository root, the nested project directories
	// scanned and merged into this row.
	Subprojects []string `json:"subprojects,omitempty" yaml:"-"`
	// UnscannedSubprojects lists, relative to the repository root, the project directories
	// deeper than the sub-project depth bound. They are reported, not scanned.
	UnscannedSubprojects []string `json:"unscanned_subprojects,omitempty" yaml:"-"`
	// FailedSubprojects lists, relative to the repository root, the nested project
	// directories whose scan failed. Their demand is missing from this row; the root
	// project and every other sub-project are still scored.
	FailedSubprojects []SubprojectFailure `json:"failed_subprojects,omitempty" yaml:"-"`
}

// SubprojectFailure names a nested sub-project whose scan failed, with the error.
type SubprojectFailure struct {
	Dir   string `json:"dir"`
	Error string `json:"error"`
}

// FleetDuplicate names a checkout that fleet discovery collapsed onto the checkout kept for
// its repository: a linked worktree (the two share one git common directory), or a
// submodule checked out in one (see topology.ResolveCheckoutRepository).
type FleetDuplicate struct {
	Dir string `json:"dir"`
	Of  string `json:"of"`
}

// FrameworkPackage describes an exported package in the framework.
type FrameworkPackage struct {
	ImportPath string `json:"import_path"`
	// Module is the Go module containing the package when a capability contract
	// declares one; empty means the framework root module or an unknown owner.
	Module       string          `json:"module,omitempty"`
	Domain       string          `json:"domain"`
	Capabilities []CapabilityKey `json:"capabilities"`
	Description  string          `json:"description,omitempty"`
}

// FrameworkIndex represents the indexed capability offerings of the framework.
type FrameworkIndex struct {
	Basis    string `json:"basis,omitempty"`
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	Version  string `json:"version"`
	// Contract names the capability contract file the inventory came from; empty when
	// the packages came from the static catalog or directory heuristics.
	Contract     string                      `json:"contract,omitempty"`
	Packages     map[string]FrameworkPackage `json:"packages"`
	Capabilities map[CapabilityKey][]string  `json:"capabilities"` // capability -> list of import paths
	// Replacements maps third-party module paths (major-version suffix stripped) onto
	// every observed framework package whose contract entry replaces them, in import
	// order; reconciliation prefers the claimant declaring the demanded capability.
	Replacements map[string][]string `json:"replacements,omitempty"`
}

// GapDetail documents an unmet capability demand across the fleet.
type GapDetail struct {
	Capability    CapabilityKey `json:"capability"`
	ConsumerCount int           `json:"consumer_count"`
	Consumers     []string      `json:"consumers"`
	PackagesUsed  []string      `json:"packages_used"`
}

// FleetDemandReport aggregates all downstream needs across the fleet.
type FleetDemandReport struct {
	CoverageBasis       string    `json:"coverage_basis,omitempty"`
	GeneratedAt         time.Time `json:"generated_at"`
	FleetRoot           string    `json:"fleet_root"`
	Framework           string    `json:"framework"`
	TotalRepositories   int       `json:"total_repositories"`
	ScannedRepositories int       `json:"scanned_repositories"`
	// SkippedRepositories contains repositories with no supported language analyzer.
	SkippedRepositories []string `json:"skipped_repositories,omitempty"`
	// FailedRepositories counts discovered repositories whose scan returned an error.
	FailedRepositories int `json:"failed_repositories"`
	// ScanErrors records those failures, bounded by maxScanErrorsReported.
	ScanErrors          []string                   `json:"scan_errors,omitempty"`
	DemandFrequency     map[CapabilityKey]int      `json:"demand_frequency"`
	CapabilityConsumers map[CapabilityKey][]string `json:"capability_consumers"`
	Gaps                []GapDetail                `json:"gaps"`
	Leaderboard         []RepoNeeds                `json:"leaderboard"`
	// CoverageKnown is false when no repository could be scanned, in which case
	// OverallFleetCoverage carries no meaning and must not be rendered as a result.
	CoverageKnown        bool    `json:"coverage_known"`
	OverallFleetCoverage float64 `json:"overall_fleet_coverage"`
	// DuplicateCheckouts lists the linked worktrees, and the submodules checked out in
	// them, collapsed onto the checkout kept for their repository. They are not counted
	// in TotalRepositories.
	DuplicateCheckouts []FleetDuplicate `json:"duplicate_checkouts,omitempty"`
	// UnscannedSubprojects lists the project directories deeper than the sub-project depth
	// bound below their repository root, across the fleet.
	UnscannedSubprojects []string `json:"unscanned_subprojects,omitempty"`
	// FailedSubprojects lists the nested project directories whose scan failed, across
	// the fleet, each with its error. Their repositories' rows were still scored.
	FailedSubprojects []SubprojectFailure `json:"failed_subprojects,omitempty"`
}

// ReplacementAction defines an import or dependency substitution.
type ReplacementAction struct {
	File        string `json:"file"`
	OldImport   string `json:"old_import"`
	NewImport   string `json:"new_import"`
	Description string `json:"description"`
}

// MigrationPlan represents the planned actions to migrate a repo to the framework.
type MigrationPlan struct {
	Repository      string              `json:"repository"`
	Framework       string              `json:"framework"`
	AddedRequires   []string            `json:"added_requires"`
	DroppedRequires []string            `json:"dropped_requires"`
	Replacements    []ReplacementAction `json:"replacements"`
	GuideMarkdown   string              `json:"guide_markdown"`
	// Candidate evidence is descriptive; it cannot authorize mutation.
	FrameworkVersion    string   `json:"framework_version"`
	CoverageBasis       string   `json:"coverage_basis"`
	MappingAvailability float64  `json:"mapping_availability"`
	Status              string   `json:"status"`
	Blockers            []string `json:"blockers"`
}

// MigrationResult summarizes the outcome of an applied migration. Success is false
// when a migration step fails; Error and Warnings retain the failure details.
type MigrationResult struct {
	Repository   string   `json:"repository"`
	Branch       string   `json:"branch"`
	FilesChanged []string `json:"files_changed"`
	// Warnings preserves diagnostic details for incomplete migration steps.
	Warnings []string `json:"warnings,omitempty"`
	Success  bool     `json:"success"`
	Error    string   `json:"error,omitempty"`
}
