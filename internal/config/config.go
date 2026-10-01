package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
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
	// DefaultBranch declares the repository's default branch, the branch the branch protection
	// ruleset protects. Empty leaves it to the checkout (internal/forge.RepositoryDefaultBranch:
	// the origin remote's HEAD, then "main"). A checkout without that ref, such as a CI clone,
	// needs the declaration when the default branch is not main. ValidBranchName decides what
	// a declared name may be.
	DefaultBranch string `yaml:"default_branch,omitempty"`
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

// branchProtectionKeys is the closed key set of a branch_protection section.
var branchProtectionKeys = []string{
	"enforce_linear_history", "require_signed_commits", "required_approving_reviewers",
	"dismiss_stale_reviews", "review_mode",
}

// UnmarshalYAML distinguishes an omitted review mode from an explicitly null
// value before decoding the remaining branch-protection fields normally. Keys are
// checked here because yaml.Node.Decode drops the caller's KnownFields setting, so a
// misspelled key would otherwise be discarded while the section reads as configured.
func (b *BranchProtectionPolicy) UnmarshalYAML(node *yaml.Node) error {
	if err := requireKnownKeys(node, "branch_protection", branchProtectionKeys); err != nil {
		return err
	}
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

// MemoryPolicy is the ADR-0002 memory-allocation dimension. Each control is a ban, so true
// is the stricter value on both: ZeroFrameMalloc joins over StandardHeap.
type MemoryPolicy struct {
	ZeroFrameMalloc    bool `yaml:"zero_frame_malloc"`
	BannedAllocInTicks bool `yaml:"banned_alloc_in_ticks"`
}

// ErrorUnwrapMode is the ADR-0002 error-unwrap dimension. StrictBan joins over
// AllowWithComment; the empty value means no layer declared the dimension.
type ErrorUnwrapMode string

const (
	ErrorUnwrapsAllowWithComment ErrorUnwrapMode = "allow_with_comment"
	ErrorUnwrapsStrictBan        ErrorUnwrapMode = "strict_ban"
)

// UnmarshalYAML rejects unknown, empty, and non-string modes at the source boundary.
func (m *ErrorUnwrapMode) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return errors.New("error_unwraps must be a string enum")
	}
	mode := ErrorUnwrapMode(node.Value)
	if mode == "" || !mode.known() {
		return fmt.Errorf("unsupported error_unwraps mode %q", mode)
	}
	*m = mode
	return nil
}

func (m ErrorUnwrapMode) known() bool {
	return m == "" || m == ErrorUnwrapsAllowWithComment || m == ErrorUnwrapsStrictBan
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
	// The audit compares it with the live value and fails on drift, plan reports it, and
	// nothing writes it to the forge yet (docs/guides/actions-live-checks.md).
	Actions *ActionsPolicy `yaml:"actions,omitempty"`
	// CI governs `ci filter` (HISS-18): internal/cifilter reads it through EffectiveCI, so
	// setting either switch to false makes the filter run more gates, never fewer.
	CI *CIPolicy `yaml:"ci,omitempty"`
}

// EffectiveCI returns the manifest's CI policy, or DefaultCIPolicy when the manifest declares
// no `overrides.ci` block. A declared block is read as written: a key it omits is false, the
// stricter setting.
func (o Overrides) EffectiveCI() CIPolicy {
	if o.CI == nil {
		return DefaultCIPolicy()
	}
	return *o.CI
}

// DefaultCIPolicy is the HISS-18 behaviour a repository gets without an `overrides.ci`
// block: diff-aware filtering on, and docs-only or state-only changes skip heavy gates.
func DefaultCIPolicy() CIPolicy {
	return CIPolicy{DiffAwareFiltering: true, SkipHeavyGatesOnDocsOrState: true}
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
	// DiffAwareFiltering false makes `ci filter` select the full verification matrix for
	// every change set.
	DiffAwareFiltering bool `yaml:"diff_aware_filtering"`
	// SkipHeavyGatesOnDocsOrState false makes a docs-only or state-only change run the full
	// matrix instead of skipping the race and security gates.
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
	// HISS declares the exceptions to the adopted HISS directives this repository documents
	// (hiss.exceptions). It is repository-only and stays out of ResolvedPolicy, like Register:
	// no fleet or profile layer can loosen a repository's rule. The audit's scan and the
	// generated harnesses read it through Manifest.CleanupGotoException, so both grant exactly
	// the same exception.
	HISS *HISSPolicy `yaml:"hiss,omitempty"`
	// Documentation tunes the locked documentation gate within bounds: larger Markdown
	// inventory bounds and style exclusions for partial, generated and fixture Markdown. The
	// gate reads it from this file at run time (#532, #534).
	Documentation *DocumentationPolicy `yaml:"documentation,omitempty"`
	// DocsSurfaces maps the repository's user-facing surfaces to the documentation that
	// describes them; `praetorctl docs references --base=<rev>` fails a change that touches a
	// surface without its documentation (#608). It is repository-only, like Register and HISS.
	DocsSurfaces []DocsSurface `yaml:"docs_surfaces,omitempty"`
	// WorkflowRuns declares the workflows the audit's live run check should expect to fail or
	// not to have run yet, each with a reason (#612). It is repository-only and stays out of
	// ResolvedPolicy, like HISS.
	WorkflowRuns *WorkflowRunsPolicy `yaml:"workflow_runs,omitempty"`
	// DevContainer holds the settings `praetorctl devcontainer` reads beside the profiles and
	// facets: the freshness bounds of the committed bundle (#338). It is repository-only, like
	// Documentation.
	DevContainer *DevContainerPolicy `yaml:"devcontainer,omitempty"`
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
// Memory and ErrorUnwraps are omitted from JSON while unset, so a snapshot retained before
// those dimensions existed still re-seals to the digest it recorded.
type ResolvedPolicy struct {
	Complexity       ComplexityPolicy
	BranchProtection BranchProtectionPolicy
	SupplyChain      SupplyChainPolicy
	Linters          []string
	DevFeatures      []string
	Memory           MemoryPolicy    `json:",omitzero"`
	ErrorUnwraps     ErrorUnwrapMode `json:",omitempty"`
}

// LoadManifest reads and parses a .standards.yaml file through the bounded regular-file read
// every manifest reader shares (readConfigFile).
func LoadManifest(path string) (*Manifest, error) {
	data, err := readConfigFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest at %s: %w", path, err)
	}

	return parseManifest(path, data)
}

func parseManifest(path string, data []byte) (*Manifest, error) {
	m, err := DecodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest at %s: %w", path, err)
	}
	if err := validateManifestReviewPolicy(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestRegister(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestRepository(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := m.HISS.validate(); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestDocumentation(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := ValidateDocsSurfaces(m.DocsSurfaces); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestActions(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}
	if err := validateManifestDevContainer(m); err != nil {
		return nil, fmt.Errorf("failed to validate manifest at %s: %w", path, err)
	}

	return m, nil
}

// DecodeManifest parses the manifest with no unknown fields, so a misspelled key is an
// error rather than a silently ignored line. A key that is quietly dropped reads as
// configured while the repository is governed by the built-in defaults instead, which
// silently loosens policy exactly where an operator believed they had tightened it. A second
// YAML document is refused for the same reason: it used to be ignored here while other
// readers of the file acted on whichever document they decoded (BUG-857). An empty manifest
// decodes to the zero Manifest.
//
// It is LoadManifest's decoder without the policy validations, for a reader that already holds
// the manifest bytes, such as a lock builder, so no reader decodes a manifest by other rules.
func DecodeManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := util.DecodeYAMLDocument(data, &m, util.YAMLDocumentOptions{KnownFields: true, AllowEmpty: true}); err != nil {
		return nil, err
	}
	return &m, nil
}

// RenderManifest is the one text Praetor writes a new manifest as (adopt, init, onboarding):
// one document yamllint's default rules accept, from util.EncodeYAMLDocument. yaml.Marshal's
// text, which each writer used before, opens with no document start and runs a
// register.sources digest past 80 columns, so an adopter linting its tree failed on it (BUG-782).
func RenderManifest(m *Manifest) ([]byte, error) {
	if m == nil {
		return nil, errors.New("render manifest: no manifest")
	}
	data, err := util.EncodeYAMLDocument(m)
	if err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	return data, nil
}

func validateManifestReviewPolicy(m *Manifest) error {
	if m == nil || m.Overrides.BranchProtection == nil {
		return nil
	}
	_, _, err := m.Overrides.BranchProtection.EffectiveReviewRequirements()
	return err
}

// validateManifestRepository rejects a repository.source or repository.default_branch that is
// set and malformed.
func validateManifestRepository(m *Manifest) error {
	if err := validateManifestRepositorySource(m); err != nil {
		return err
	}
	if m == nil || m.Repository.DefaultBranch == "" || ValidBranchName(m.Repository.DefaultBranch) {
		return nil
	}
	return fmt.Errorf("repository.default_branch %q must be %s", m.Repository.DefaultBranch, branchNameRule)
}

// validateManifestRepositorySource rejects a repository.source that is not an "<owner>/<name>"
// identity. Empty is valid: it is the canonical repository's shape, and the shape every
// manifest had before the field existed. A source equal to owner/name is valid too -- it is
// redundant, not wrong, and manifestIdentity in internal/forge reads it the same either way.
func validateManifestRepositorySource(m *Manifest) error {
	if m == nil || m.Repository.Source == "" || ValidRepositoryIdentity(m.Repository.Source) {
		return nil
	}
	return fmt.Errorf("repository.source %q is not an owner/name identity", m.Repository.Source)
}

// ValidRepositoryIdentity reports whether value is an "<owner>/<name>" repository identity: two
// nonempty parts joined by exactly one slash.
func ValidRepositoryIdentity(value string) bool {
	owner, name, ok := strings.Cut(value, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
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
		Linters:      []string{"govet"},
		DevFeatures:  []string{"common-utils"},
		ErrorUnwraps: ErrorUnwrapsAllowWithComment,
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

	return &ResolvedPolicy{
		Complexity:       joinComplexity(a.Complexity, b.Complexity),
		BranchProtection: joinBranchProtection(a.BranchProtection, b.BranchProtection),
		SupplyChain:      joinSupplyChain(a.SupplyChain, b.SupplyChain),
		// Linters and DevFeatures: cumulative deduplicated union.
		Linters:      unionStrings(a.Linters, b.Linters),
		DevFeatures:  unionStrings(a.DevFeatures, b.DevFeatures),
		Memory:       joinMemory(a.Memory, b.Memory),
		ErrorUnwraps: joinErrorUnwraps(a.ErrorUnwraps, b.ErrorUnwraps),
	}
}

// joinComplexity keeps the lower positive cap (greatest lower bound); zero is no bound.
func joinComplexity(a, b ComplexityPolicy) ComplexityPolicy {
	return ComplexityPolicy{
		MaxCyclomatic: minPositive(a.MaxCyclomatic, b.MaxCyclomatic),
		MaxCognitive:  minPositive(a.MaxCognitive, b.MaxCognitive),
		MaxFuncLOC:    minPositive(a.MaxFuncLOC, b.MaxFuncLOC),
		MaxStatements: minPositive(a.MaxStatements, b.MaxStatements),
	}
}

// joinBranchProtection keeps every enabled control and the higher reviewer count. A
// policy-layer join never enables the repository-only review relaxation.
func joinBranchProtection(a, b BranchProtectionPolicy) BranchProtectionPolicy {
	return BranchProtectionPolicy{
		EnforceLinearHistory:       a.EnforceLinearHistory || b.EnforceLinearHistory,
		RequireSignedCommits:       a.RequireSignedCommits || b.RequireSignedCommits,
		DismissStaleReviews:        a.DismissStaleReviews || b.DismissStaleReviews,
		RequiredApprovingReviewers: max(a.RequiredApprovingReviewers, b.RequiredApprovingReviewers),
		ReviewMode:                 joinReviewMode(a.ReviewMode, b.ReviewMode),
	}
}

// joinSupplyChain keeps the higher SLSA level and every mandatory attestation.
func joinSupplyChain(a, b SupplyChainPolicy) SupplyChainPolicy {
	return SupplyChainPolicy{
		SLSALevel:     max(a.SLSALevel, b.SLSALevel),
		EnforceCosign: a.EnforceCosign || b.EnforceCosign,
		RequireSBOM:   a.RequireSBOM || b.RequireSBOM,
	}
}

// joinMemory keeps every allocation ban: ZeroFrameMalloc joins over StandardHeap.
func joinMemory(a, b MemoryPolicy) MemoryPolicy {
	return MemoryPolicy{
		ZeroFrameMalloc:    a.ZeroFrameMalloc || b.ZeroFrameMalloc,
		BannedAllocInTicks: a.BannedAllocInTicks || b.BannedAllocInTicks,
	}
}

// joinErrorUnwraps: StrictBan joins over AllowWithComment, and a declared mode over an
// undeclared one. An unknown mode is preserved so digest verification can reject it.
func joinErrorUnwraps(a, b ErrorUnwrapMode) ErrorUnwrapMode {
	switch {
	case !a.known():
		return a
	case !b.known():
		return b
	case a == ErrorUnwrapsStrictBan || b == ErrorUnwrapsStrictBan:
		return ErrorUnwrapsStrictBan
	case a == "":
		return b
	default:
		return a
	}
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
	mode := b.ReviewMode
	if o.ReviewMode != "" {
		mode = o.ReviewMode
	}
	*b = joinBranchProtection(*b, *o)
	b.ReviewMode = mode
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
	*s = joinSupplyChain(*s, *o)
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
