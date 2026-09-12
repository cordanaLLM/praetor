package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	// #nosec G304 -- path is the manifest location chosen by the invoking user (a CLI
	// flag defaulting to the repository root); there is no confinement root to enforce.
	data, err := os.ReadFile(filepath.Clean(path))
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
		return clonePolicy(b)
	}
	if b == nil {
		return clonePolicy(a)
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

func clonePolicy(p *ResolvedPolicy) *ResolvedPolicy {
	clone := *p
	clone.Linters = append([]string(nil), p.Linters...)
	clone.DevFeatures = append([]string(nil), p.DevFeatures...)
	return &clone
}

// ApplyOverrides applies project-level overrides on top of the resolved policy,
// enforcing that overrides can only increase strictness.
func (p *ResolvedPolicy) ApplyOverrides(o Overrides) {
	if o.Complexity != nil {
		p.Complexity.applyOverride(o.Complexity)
	}
	if o.BranchProtection != nil {
		p.BranchProtection.applyOverride(o.BranchProtection)
	}
	if o.SupplyChain != nil {
		p.SupplyChain.applyOverride(o.SupplyChain)
	}
}

// tightenPositive lowers *current to override when override is a stricter positive cap.
func tightenPositive(current *int, override int) {
	if override > 0 {
		*current = minPositive(*current, override)
	}
}

// applyOverride keeps the stricter (lower, positive) complexity caps.
func (c *ComplexityPolicy) applyOverride(o *ComplexityPolicy) {
	tightenPositive(&c.MaxCyclomatic, o.MaxCyclomatic)
	tightenPositive(&c.MaxCognitive, o.MaxCognitive)
	tightenPositive(&c.MaxFuncLOC, o.MaxFuncLOC)
	tightenPositive(&c.MaxStatements, o.MaxStatements)
}

// applyOverride keeps the stricter branch protection settings.
func (b *BranchProtectionPolicy) applyOverride(o *BranchProtectionPolicy) {
	b.EnforceLinearHistory = b.EnforceLinearHistory || o.EnforceLinearHistory
	b.RequireSignedCommits = b.RequireSignedCommits || o.RequireSignedCommits
	b.DismissStaleReviews = b.DismissStaleReviews || o.DismissStaleReviews
	b.RequiredApprovingReviewers = max(b.RequiredApprovingReviewers, o.RequiredApprovingReviewers)
}

// applyOverride keeps the stricter supply-chain settings.
func (s *SupplyChainPolicy) applyOverride(o *SupplyChainPolicy) {
	s.SLSALevel = max(s.SLSALevel, o.SLSALevel)
	s.EnforceCosign = s.EnforceCosign || o.EnforceCosign
	s.RequireSBOM = s.RequireSBOM || o.RequireSBOM
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
