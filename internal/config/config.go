package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RepositoryMetadata describes the identity and public metadata of the target repository.
type RepositoryMetadata struct {
	Owner       string   `yaml:"owner"`
	Name        string   `yaml:"name"`
	Visibility  string   `yaml:"visibility"`
	Description string   `yaml:"description"`
	Homepage    string   `yaml:"homepage"`
	Topics      []string `yaml:"topics"`
}

// ComplexityPolicy defines bounds on code complexity and function size.
type ComplexityPolicy struct {
	MaxCyclomatic int `yaml:"max_cyclomatic"`
	MaxCognitive  int `yaml:"max_cognitive"`
	MaxFuncLOC    int `yaml:"max_func_loc"`
	MaxStatements int `yaml:"max_statements"`
}

// BranchProtectionPolicy defines branch protection invariants.
type BranchProtectionPolicy struct {
	EnforceLinearHistory       bool `yaml:"enforce_linear_history"`
	RequireSignedCommits       bool `yaml:"require_signed_commits"`
	RequiredApprovingReviewers int  `yaml:"required_approving_reviewers"`
	DismissStaleReviews        bool `yaml:"dismiss_stale_reviews"`
}

// SupplyChainPolicy defines supply chain provenance requirements.
type SupplyChainPolicy struct {
	SLSALevel     int  `yaml:"slsa_level"`
	EnforceCosign bool `yaml:"enforce_cosign"`
	RequireSBOM   bool `yaml:"require_sbom"`
}

// Overrides contains explicit project-level overrides that can only increase strictness.
type Overrides struct {
	Complexity       *ComplexityPolicy       `yaml:"complexity,omitempty"`
	BranchProtection *BranchProtectionPolicy `yaml:"branch_protection,omitempty"`
	SupplyChain      *SupplyChainPolicy      `yaml:"supply_chain,omitempty"`
}

// Manifest represents the parsed .standards.yaml file.
type Manifest struct {
	Version    int                `yaml:"version"`
	Repository RepositoryMetadata `yaml:"repository"`
	Profiles   []string           `yaml:"profiles"`
	Facets     []string           `yaml:"facets"`
	Overrides  Overrides          `yaml:"overrides,omitempty"`
}

// ResolvedPolicy is the composite unbypassable policy produced by lattice join (supremum).
type ResolvedPolicy struct {
	Complexity       ComplexityPolicy
	BranchProtection BranchProtectionPolicy
	SupplyChain      SupplyChainPolicy
	Linters          []string
	DevFeatures      []string
}

// LoadManifest reads and parses a .standards.yaml file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest at %s: %w", path, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest at %s: %w", path, err)
	}

	return &m, nil
}

// DefaultPolicy returns a baseline default policy.
func DefaultPolicy() *ResolvedPolicy {
	return &ResolvedPolicy{
		Complexity: ComplexityPolicy{
			MaxCyclomatic: 15,
			MaxCognitive:  20,
			MaxFuncLOC:    100,
			MaxStatements: 75,
		},
		BranchProtection: BranchProtectionPolicy{
			EnforceLinearHistory:       true,
			RequireSignedCommits:       false,
			RequiredApprovingReviewers: 1,
			DismissStaleReviews:        true,
		},
		SupplyChain: SupplyChainPolicy{
			SLSALevel:     1,
			EnforceCosign: false,
			RequireSBOM:   false,
		},
		Linters:     []string{"govet"},
		DevFeatures: []string{"common-utils"},
	}
}

// Join computes the supremum (monotonic maximum/strictest) between two policies.
// Lattice rule: "Highest Standard Wins".
func Join(a, b *ResolvedPolicy) *ResolvedPolicy {
	if a == nil && b == nil {
		return DefaultPolicy()
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	res := &ResolvedPolicy{}

	// Complexity: Strictest is lower bounds (greatest lower bound / min)
	res.Complexity.MaxCyclomatic = minPositive(a.Complexity.MaxCyclomatic, b.Complexity.MaxCyclomatic)
	res.Complexity.MaxCognitive = minPositive(a.Complexity.MaxCognitive, b.Complexity.MaxCognitive)
	res.Complexity.MaxFuncLOC = minPositive(a.Complexity.MaxFuncLOC, b.Complexity.MaxFuncLOC)
	res.Complexity.MaxStatements = minPositive(a.Complexity.MaxStatements, b.Complexity.MaxStatements)

	// Branch Protection: Strictest is true or higher count (least upper bound / max)
	res.BranchProtection.EnforceLinearHistory = a.BranchProtection.EnforceLinearHistory || b.BranchProtection.EnforceLinearHistory
	res.BranchProtection.RequireSignedCommits = a.BranchProtection.RequireSignedCommits || b.BranchProtection.RequireSignedCommits
	res.BranchProtection.DismissStaleReviews = a.BranchProtection.DismissStaleReviews || b.BranchProtection.DismissStaleReviews
	res.BranchProtection.RequiredApprovingReviewers = max(a.BranchProtection.RequiredApprovingReviewers, b.BranchProtection.RequiredApprovingReviewers)

	// Supply Chain: Strictest is higher SLSA level and mandatory signing
	res.SupplyChain.SLSALevel = max(a.SupplyChain.SLSALevel, b.SupplyChain.SLSALevel)
	res.SupplyChain.EnforceCosign = a.SupplyChain.EnforceCosign || b.SupplyChain.EnforceCosign
	res.SupplyChain.RequireSBOM = a.SupplyChain.RequireSBOM || b.SupplyChain.RequireSBOM

	// Linters and DevFeatures: Cumulative union
	res.Linters = unionStrings(a.Linters, b.Linters)
	res.DevFeatures = unionStrings(a.DevFeatures, b.DevFeatures)

	return res
}

// ApplyOverrides applies project-level overrides on top of the resolved policy,
// enforcing that overrides can only increase strictness.
func (p *ResolvedPolicy) ApplyOverrides(o Overrides) {
	if o.Complexity != nil {
		if o.Complexity.MaxCyclomatic > 0 {
			p.Complexity.MaxCyclomatic = minPositive(p.Complexity.MaxCyclomatic, o.Complexity.MaxCyclomatic)
		}
		if o.Complexity.MaxCognitive > 0 {
			p.Complexity.MaxCognitive = minPositive(p.Complexity.MaxCognitive, o.Complexity.MaxCognitive)
		}
		if o.Complexity.MaxFuncLOC > 0 {
			p.Complexity.MaxFuncLOC = minPositive(p.Complexity.MaxFuncLOC, o.Complexity.MaxFuncLOC)
		}
		if o.Complexity.MaxStatements > 0 {
			p.Complexity.MaxStatements = minPositive(p.Complexity.MaxStatements, o.Complexity.MaxStatements)
		}
	}

	if o.BranchProtection != nil {
		p.BranchProtection.EnforceLinearHistory = p.BranchProtection.EnforceLinearHistory || o.BranchProtection.EnforceLinearHistory
		p.BranchProtection.RequireSignedCommits = p.BranchProtection.RequireSignedCommits || o.BranchProtection.RequireSignedCommits
		p.BranchProtection.DismissStaleReviews = p.BranchProtection.DismissStaleReviews || o.BranchProtection.DismissStaleReviews
		if o.BranchProtection.RequiredApprovingReviewers > p.BranchProtection.RequiredApprovingReviewers {
			p.BranchProtection.RequiredApprovingReviewers = o.BranchProtection.RequiredApprovingReviewers
		}
	}

	if o.SupplyChain != nil {
		if o.SupplyChain.SLSALevel > p.SupplyChain.SLSALevel {
			p.SupplyChain.SLSALevel = o.SupplyChain.SLSALevel
		}
		p.SupplyChain.EnforceCosign = p.SupplyChain.EnforceCosign || o.SupplyChain.EnforceCosign
		p.SupplyChain.RequireSBOM = p.SupplyChain.RequireSBOM || o.SupplyChain.RequireSBOM
	}
}

func minPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	var result []string
	for _, item := range a {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	for _, item := range b {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}
