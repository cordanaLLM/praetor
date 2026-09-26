package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
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
	// Source records the public repository this manifest was overlaid from, as
	// "<owner>/<name>". internal/operationalsync's owner overlay writes it because the
	// overlay rewrites Owner to the operational fork's own identity; every consumer that
	// must agree with files the overlay never touches (workflow repository guards in
	// internal/forge, and the ruleset context computation built on them) prefers Source
	// over Owner/Name so a fork's own tests pass against the public identity those files
	// still carry. Empty on the canonical repository and on any manifest that predates
	// the field.
	Source string `yaml:"source,omitempty"`
}

// ComplexityPolicy defines bounds on code complexity and function size.
type ComplexityPolicy struct {
	MaxCyclomatic int `yaml:"max_cyclomatic"`
	MaxCognitive  int `yaml:"max_cognitive"`
	MaxFuncLOC    int `yaml:"max_func_loc"`
	MaxStatements int `yaml:"max_statements"`
}

// BranchReviewMode selects how pull-request review requirements are enforced.
type BranchReviewMode string

const (
	BranchReviewModeIndependent      BranchReviewMode = "independent"
	BranchReviewModeSingleMaintainer BranchReviewMode = "single_maintainer"
)

// UnmarshalYAML rejects unknown, empty, and non-string modes at the source
// boundary. The zero value remains valid only for manifests that omit the field.
func (m *BranchReviewMode) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("branch protection review_mode must be a string enum")
	}
	mode := BranchReviewMode(node.Value)
	if mode != BranchReviewModeIndependent && mode != BranchReviewModeSingleMaintainer {
		return fmt.Errorf("unsupported branch protection review mode %q", mode)
	}
	*m = mode
	return nil
}

// BranchProtectionPolicy defines branch protection invariants. Single-maintainer
// mode changes only the effective review gate; the configured reviewer minimum is
// retained so returning to independent mode restores it.
type BranchProtectionPolicy struct {
	EnforceLinearHistory       bool             `yaml:"enforce_linear_history"`
	RequireSignedCommits       bool             `yaml:"require_signed_commits"`
	RequiredApprovingReviewers int              `yaml:"required_approving_reviewers"`
	DismissStaleReviews        bool             `yaml:"dismiss_stale_reviews"`
	ReviewMode                 BranchReviewMode `yaml:"review_mode,omitempty"`
}

// UnmarshalYAML distinguishes an omitted review mode from an explicitly null
// value before decoding the remaining branch-protection fields normally.
func (b *BranchProtectionPolicy) UnmarshalYAML(node *yaml.Node) error {
	reviewMode := policyMember(node, "review_mode")
	if reviewMode != nil && (reviewMode.Kind != yaml.ScalarNode || reviewMode.Tag != "!!str") {
		return errors.New("branch protection review_mode must be a string enum")
	}
	type rawBranchProtectionPolicy BranchProtectionPolicy
	var decoded rawBranchProtectionPolicy
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*b = BranchProtectionPolicy(decoded)
	return nil
}

// EffectiveReviewRequirements resolves the review gate used by every ruleset
// consumer. An omitted mode retains the historical independent-review behavior.
func (b BranchProtectionPolicy) EffectiveReviewRequirements() (int, bool, error) {
	if b.RequiredApprovingReviewers < 0 {
		return 0, false, errors.New("required approving review count cannot be negative")
	}
	switch b.ReviewMode {
	case "", BranchReviewModeIndependent:
		return b.RequiredApprovingReviewers, true, nil
	case BranchReviewModeSingleMaintainer:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("unsupported branch protection review mode %q", b.ReviewMode)
	}
}

// SupplyChainPolicy defines supply chain provenance requirements.
type SupplyChainPolicy struct {
	SLSALevel     int  `yaml:"slsa_level"`
	EnforceCosign bool `yaml:"enforce_cosign"`
	RequireSBOM   bool `yaml:"require_sbom"`
}

// Overrides contains explicit project-level policy overrides. Numeric and boolean
// controls can only increase strictness; review mode is the bounded exception.
type Overrides struct {
	Complexity       *ComplexityPolicy       `yaml:"complexity,omitempty"`
	BranchProtection *BranchProtectionPolicy `yaml:"branch_protection,omitempty"`
	SupplyChain      *SupplyChainPolicy      `yaml:"supply_chain,omitempty"`
	// Actions governs the repository's GitHub Actions workflow permissions. It is modelled here
	// because it is load-bearing in the same way branch protection is and was not modelled at
	// all: a bot-opened pull request is either possible or it is not, and a release flow built
	// on one fails on a red main with nothing reporting the setting that caused it (#153).
	Actions *ActionsPolicy `yaml:"actions,omitempty"`
	// CI is declared by the schema and currently has no consumer: internal/cifilter
	// computes its decision without reading the manifest, so these values do not change
	// behaviour. Declared here so the manifest parses rather than being silently dropped,
	// and so the gap is visible instead of invisible.
	CI *CIPolicy `yaml:"ci,omitempty"`
}

// ActionsPolicy declares GitHub Actions workflow permissions for a repository.
type ActionsPolicy struct {
	// DefaultWorkflowPermissions is the GITHUB_TOKEN default: "read" or "write".
	DefaultWorkflowPermissions string `yaml:"default_workflow_permissions"`
	// AllowCreateAndApprovePullRequests decides whether a workflow may open or approve a pull
	// request. release-please, dependency bumpers and flavor sync all need it; a repository
	// whose release flow does not should leave it false.
	AllowCreateAndApprovePullRequests bool `yaml:"allow_create_and_approve_pull_requests"`
}

// CIPolicy declares diff-aware gating intent (HISS-18).
type CIPolicy struct {
	DiffAwareFiltering          bool `yaml:"diff_aware_filtering"`
	SkipHeavyGatesOnDocsOrState bool `yaml:"skip_heavy_gates_on_docs_or_state"`
}

// ReceiptAnchor pins the Ed25519 public key that `gate verify` accepts. The canonical
// manifest declares it so the schema is complete; internal/lockdown reads it through its
// own narrow parse of the same file.
type ReceiptAnchor struct {
	PublicKey string `yaml:"public_key"`
}

// Manifest represents the parsed .standards.yaml file.
type Manifest struct {
	Version    int                `yaml:"version"`
	Repository RepositoryMetadata `yaml:"repository"`
	Profiles   []string           `yaml:"profiles"`
	Facets     []string           `yaml:"facets"`
	Overrides  Overrides          `yaml:"overrides,omitempty"`
	// Receipt and Needs are consumed by internal/lockdown and internal/needs through their
	// own narrow parses of this same file. They are declared here because this is the
	// canonical manifest type: a schema that omits keys the file legitimately carries
	// cannot tell a real key from a typo.
	Receipt *ReceiptAnchor         `yaml:"receipt,omitempty"`
	Needs   map[string]interface{} `yaml:"needs,omitempty"`
	// Adoption records which generated surfaces this repository accepts. It belongs in the
	// manifest rather than in a command flag so the decision survives the next adoption run
	// instead of depending on whoever typed the command.
	Adoption *AdoptionPolicy `yaml:"adoption,omitempty"`
	// Register selects the text register per audience surface and task class. It is
	// repository-only and stays out of ResolvedPolicy, so no fleet or profile layer can set
	// it and the resolved policy never changes because of it (ADR-0010).
	Register *RegisterPolicy `yaml:"register,omitempty"`
	// Editors selects the editor configurations `praetorctl adopt`, onboarding and
	// `praetorctl editors` generate. An absent key keeps every supported editor, the behaviour
	// before the key existed; a present list, empty included, selects exactly what it names
	// (#202). Ids are resolved and rejected by internal/editor, which owns the editor set.
	Editors []string `yaml:"editors,omitempty"`
	// AgentClients selects the vendor context projections compile-context writes and
	// verifies, with the same absent-versus-present rule as Editors. Ids are resolved and
	// rejected by internal/agentcontext, which owns the projection registry.
	AgentClients []string `yaml:"agent_clients,omitempty"`
}

// AdoptionPolicy declares generated artefacts this repository refuses.
//
// A repository can have a real reason to refuse one. README.md is the common case: a project
// that pins a published artefact to a digest over its source set has README.md inside that
// set, so injecting a badge invalidates the artefact's provenance and fails the project's own
// gate, for a badge (#145).
type AdoptionPolicy struct {
	Decline []string `yaml:"decline,omitempty"`
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

	return parseManifest(path, data)
}

func parseManifest(path string, data []byte) (*Manifest, error) {
	m, err := decodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest at %s: %w", path, err)
	}
	if err := validateManifestReviewPolicy(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestRegister(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestRepositorySource(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}

	return m, nil
}

// decodeManifest parses the manifest with no unknown fields, so a misspelled key is an
// error rather than a silently ignored line. A key that is quietly dropped reads as
// configured while the repository is governed by the built-in defaults instead, which
// silently loosens policy exactly where an operator believed they had tightened it.
func decodeManifest(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return &m, nil
		}
		return nil, err
	}
	return &m, nil
}

func validateManifestReviewPolicy(m *Manifest) error {
	if m == nil || m.Overrides.BranchProtection == nil {
		return nil
	}
	_, _, err := m.Overrides.BranchProtection.EffectiveReviewRequirements()
	return err
}

// validateManifestRepositorySource rejects a repository.source that is not an "<owner>/<name>"
// identity. Empty is valid: it is the canonical repository's shape, and the shape every
// manifest had before the field existed. A source equal to owner/name is valid too -- it is
// redundant, not wrong, and manifestIdentity in internal/forge reads it the same either way.
func validateManifestRepositorySource(m *Manifest) error {
	if m == nil || m.Repository.Source == "" {
		return nil
	}
	owner, name, ok := strings.Cut(m.Repository.Source, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("repository.source %q is not an owner/name identity", m.Repository.Source)
	}
	return nil
}

// DefaultPolicy returns a baseline default policy. Its function-length limit is
// hiss.DefaultMaxFuncLOC, the length the scanner and the audit enforce; it used to be 100,
// so a plan or sync preview promised a length no audit accepted (BUG-309).
func DefaultPolicy() *ResolvedPolicy {
	return &ResolvedPolicy{
		Complexity: ComplexityPolicy{
			MaxCyclomatic: 15,
			MaxCognitive:  20,
			MaxFuncLOC:    hiss.DefaultMaxFuncLOC,
			MaxStatements: 75,
		},
		BranchProtection: BranchProtectionPolicy{
			EnforceLinearHistory:       true,
			RequireSignedCommits:       false,
			RequiredApprovingReviewers: 1,
			DismissStaleReviews:        true,
			ReviewMode:                 BranchReviewModeIndependent,
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
	res.BranchProtection.ReviewMode = joinReviewMode(a.BranchProtection.ReviewMode, b.BranchProtection.ReviewMode)

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

// ApplyOverrides applies project-level overrides on top of the resolved policy.
// ReviewMode is the sole explicit relaxation; every other control stays monotonic.
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

// applyOverride keeps stricter branch settings while allowing the explicit review mode.
func (b *BranchProtectionPolicy) applyOverride(o *BranchProtectionPolicy) {
	b.EnforceLinearHistory = b.EnforceLinearHistory || o.EnforceLinearHistory
	b.RequireSignedCommits = b.RequireSignedCommits || o.RequireSignedCommits
	b.DismissStaleReviews = b.DismissStaleReviews || o.DismissStaleReviews
	b.RequiredApprovingReviewers = max(b.RequiredApprovingReviewers, o.RequiredApprovingReviewers)
	if o.ReviewMode != "" {
		b.ReviewMode = o.ReviewMode
	}
}

// joinReviewMode preserves invalid input for downstream rejection while ensuring
// policy-layer joins never enable the repository-only relaxation implicitly.
func joinReviewMode(a, b BranchReviewMode) BranchReviewMode {
	if !knownBranchReviewMode(a) {
		return a
	}
	if !knownBranchReviewMode(b) {
		return b
	}
	return BranchReviewModeIndependent
}

func knownBranchReviewMode(mode BranchReviewMode) bool {
	return mode == "" || mode == BranchReviewModeIndependent || mode == BranchReviewModeSingleMaintainer
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
