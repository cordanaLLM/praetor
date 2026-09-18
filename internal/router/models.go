package router

import (
	"context"
	"time"
)

// ModelFamily identifies provider architecture families for orthogonal auditing.
type ModelFamily string

const (
	FamilyAnthropic   ModelFamily = "anthropic"
	FamilyGoogle      ModelFamily = "google"
	FamilyOpenAI      ModelFamily = "openai"
	FamilyOpenWeights ModelFamily = "open-weights"
)

// ModelSource records which writer owns a catalog entry. `models sync` rewrites
// seed entries and never removes local or operator entries unless told to prune.
type ModelSource string

const (
	// SourceOperator marks an entry declared by hand; it is the empty value.
	SourceOperator ModelSource = ""
	// SourceSeed marks an entry written from the built-in seed list.
	SourceSeed ModelSource = "seed"
	// SourceLocal marks an entry discovered from a local model runtime.
	SourceLocal ModelSource = "local"
)

// ModelDescriptor specifies capability and rate-limit metadata for an LLM endpoint.
type ModelDescriptor struct {
	ID           string      `yaml:"id" json:"id"`
	Family       ModelFamily `yaml:"family" json:"family"`
	Source       ModelSource `yaml:"source,omitempty" json:"source,omitempty"`
	RPMLimit     int         `yaml:"rpm_limit" json:"rpm_limit"`
	TPMLimit     int         `yaml:"tpm_limit" json:"tpm_limit"`
	CostPerMIn   float64     `yaml:"cost_per_m_in" json:"cost_per_m_in"`
	CostPerMOut  float64     `yaml:"cost_per_m_out" json:"cost_per_m_out"`
	Capabilities []string    `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	// CostRatesDeclared distinguishes explicit zero rates from omitted prices.
	// The YAML loader sets it; programmatic task-routing configurations must do so.
	CostRatesDeclared bool `yaml:"-" json:"-"`
}

// Tier groups operator-declared task assignments and their candidate models.
type Tier struct {
	Description  string            `yaml:"description"`
	TargetTasks  []string          `yaml:"target_tasks"`
	Models       []ModelDescriptor `yaml:"models"`
	FallbackTier string            `yaml:"fallback_tier"`
}

// GovernancePolicy governs concurrency and cross-audit rules.
type GovernancePolicy struct {
	MaxConcurrentSameModel     int     `yaml:"max_concurrent_same_model"`
	ExhaustionThresholdPercent float64 `yaml:"exhaustion_threshold_percent"`
	OrthogonalAuditRequired    bool    `yaml:"orthogonal_audit_required"`
}

// RoutingConfig represents .config/models/routing.yaml.
type RoutingConfig struct {
	Version    int              `yaml:"version"`
	Tiers      map[string]Tier  `yaml:"tiers"`
	Governance GovernancePolicy `yaml:"governance"`
	// SourceSHA256 identifies the exact loaded configuration bytes, when available.
	SourceSHA256 string `yaml:"-"`
}

// LoadRoutingConfig reads a bounded, validated routing config with a short deadline.
// Use LoadRoutingConfigContext to retain an existing caller cancellation budget.
func LoadRoutingConfig(path string) (*RoutingConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return LoadRoutingConfigContext(ctx, path)
}
