package needs

import (
	"time"
)

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

// DependencyDemand captures a single dependency requirement and its framework mapping.
type DependencyDemand struct {
	Package              string           `json:"package" yaml:"package"`
	Version              string           `json:"version,omitempty" yaml:"version,omitempty"`
	Language             string           `json:"language,omitempty" yaml:"language,omitempty"`
	Ecosystem            string           `json:"ecosystem,omitempty" yaml:"ecosystem,omitempty"`
	Capability           CapabilityKey    `json:"capability" yaml:"capability"`
	Status               CapabilityStatus `json:"status" yaml:"status"`
	GolusorisReplacement string           `json:"golusoris_replacement,omitempty" yaml:"golusoris_replacement,omitempty"`
	TargetBuilderKit     string           `json:"target_builder_kit,omitempty" yaml:"target_builder_kit,omitempty"`
	Notes                string           `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// CapabilityDeclaration groups required and optional capabilities.
type CapabilityDeclaration struct {
	Required []CapabilityKey `json:"required" yaml:"required"`
	Optional []CapabilityKey `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// ReadinessMetrics summarizes framework adoption feasibility.
type ReadinessMetrics struct {
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
	Readiness    ReadinessMetrics      `json:"readiness" yaml:"readiness"`
	UpdatedAt    time.Time             `json:"updated_at" yaml:"updated_at"`
}

// FrameworkPackage describes an exported package in the framework.
type FrameworkPackage struct {
	ImportPath   string          `json:"import_path"`
	Domain       string          `json:"domain"`
	Capabilities []CapabilityKey `json:"capabilities"`
	Description  string          `json:"description,omitempty"`
}

// FrameworkIndex represents the indexed capability offerings of the framework.
type FrameworkIndex struct {
	Name         string                      `json:"name"`
	RootPath     string                      `json:"root_path"`
	Version      string                      `json:"version"`
	Packages     map[string]FrameworkPackage `json:"packages"`
	Capabilities map[CapabilityKey][]string  `json:"capabilities"` // capability -> list of import paths
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
	GeneratedAt          time.Time                  `json:"generated_at"`
	FleetRoot            string                     `json:"fleet_root"`
	Framework            string                     `json:"framework"`
	TotalRepositories    int                        `json:"total_repositories"`
	ScannedRepositories  int                        `json:"scanned_repositories"`
	DemandFrequency      map[CapabilityKey]int      `json:"demand_frequency"`
	CapabilityConsumers  map[CapabilityKey][]string `json:"capability_consumers"`
	Gaps                 []GapDetail                `json:"gaps"`
	Leaderboard          []RepoNeeds                `json:"leaderboard"`
	OverallFleetCoverage float64                    `json:"overall_fleet_coverage"`
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
}

// MigrationResult summarizes the outcome of an applied migration.
type MigrationResult struct {
	Repository   string   `json:"repository"`
	Branch       string   `json:"branch"`
	FilesChanged []string `json:"files_changed"`
	Success      bool     `json:"success"`
	Error        string   `json:"error,omitempty"`
}
