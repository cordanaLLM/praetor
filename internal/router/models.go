package router

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ModelFamily identifies provider architecture families for orthogonal auditing.
type ModelFamily string

const (
	FamilyAnthropic   ModelFamily = "anthropic"
	FamilyGoogle      ModelFamily = "google"
	FamilyOpenAI      ModelFamily = "openai"
	FamilyOpenWeights ModelFamily = "open-weights"
)

// ModelDescriptor specifies capability and rate-limit metadata for an LLM endpoint.
type ModelDescriptor struct {
	ID          string      `yaml:"id"`
	Family      ModelFamily `yaml:"family"`
	RPMLimit    int         `yaml:"rpm_limit"`
	TPMLimit    int         `yaml:"tpm_limit"`
	CostPerMIn  float64     `yaml:"cost_per_m_in"`
	CostPerMOut float64     `yaml:"cost_per_m_out"`
}

// Tier defines a cognitive competence classification and its member models.
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
}

// LoadRoutingConfig reads and parses .config/models/routing.yaml.
func LoadRoutingConfig(path string) (*RoutingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read model routing config at %s: %w", path, err)
	}

	var cfg RoutingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse model routing config at %s: %w", path, err)
	}

	return &cfg, nil
}
