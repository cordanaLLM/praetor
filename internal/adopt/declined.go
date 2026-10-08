package adopt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// maxDeclinedArtifacts bounds the declared list so a malformed manifest cannot make
// adoption iterate without limit.
const maxDeclinedArtifacts = 64

// namedStep pairs a reconciliation step with the name a repository uses to decline it.
type namedStep struct {
	name string
	run  adoptStep
}

// mandatoryArtifacts cannot be declined. Declining the manifest or the lockfile would leave
// a repository that claims adoption while carrying nothing that records what it adopted, and
// the baseline is what every later audit compares against. The documentation gate already
// has one off switch, the docs:seo-portal facet, whose removal converges every surface it
// owns; a decline would skip only the assets and leave verify-all calling a missing runner.
// The API compatibility gate's off switch is likewise the api:public-contract facet: audit
// locks its files while the facet is declared, so a decline would leave audit failing.
var mandatoryArtifacts = map[string]string{
	"manifest":               "the manifest is what records the declaration itself",
	"lockfile":               "the lockfile is what pins the policies the manifest names",
	"baseline":               "every later audit compares against the baseline",
	"documentation-gate":     "remove the docs:seo-portal facet instead; it converges every documentation surface",
	"api-compatibility-gate": "remove the api:public-contract facet instead; it removes the gate and its workflow",
}

// DeclineAudit is what audit does with the artefact of a declined adoption step.
type DeclineAudit string

const (
	// DeclineAuditNone marks a step whose artefact no audit gate reads: its decline changes no
	// verdict.
	DeclineAuditNone DeclineAudit = "none"
	// DeclineAuditSkipped marks a step whose artefact an audit gate reads: that gate looks the
	// decline up (AuditDecline) and passes, naming it, instead of requiring the artefact.
	DeclineAuditSkipped DeclineAudit = "skipped"
	// DeclineAuditRetained marks a step whose artefact, or the effect it has, audit requires
	// whatever the decline (declineContract.retains). The decline only stops adoption writing it,
	// and adoption and audit both say what stays required.
	DeclineAuditRetained DeclineAudit = "retained"
)

// declineContract is the audit side of one declinable adoption step.
type declineContract struct {
	audit DeclineAudit
	// mcp reports that standards_audit reads the artefact too; its gate honours the decline as
	// the CLI gate does.
	mcp bool
	// retains names what audit still requires while the step is declined (DeclineAuditRetained).
	retains string
}

// declineContracts is the audit contract of every declinable step. Every step of the chain is
// here or in mandatoryArtifacts, never both (TestDeclineContracts_Boundary_CoverEveryStep), so
// adoption cannot accept a decline that audit then fails on without saying so (#600).
var declineContracts = map[string]declineContract{
	"git-ignore": {audit: DeclineAuditRetained,
		retains: "Git to ignore the agent evidence directory .workingdir/evidence/, and the documentation gate's " +
			"private paths, through the repository's own ignore rules"},
	"policy-catalog": {audit: DeclineAuditRetained, mcp: true,
		retains: "the catalog the lock pins under .config/archetypes, which adoption and audit resolve the effective policy from"},
	"agent-harness": {audit: DeclineAuditRetained, mcp: true,
		retains: "the text register block in AGENTS.md, its caveman lint, and the compile-context projection of every " +
			"agent_clients target, none with agent_clients: [] (ADR-0010 decisions 5 and 11)"},
	"dev-container":          {audit: DeclineAuditSkipped},
	"makefile":               {audit: DeclineAuditSkipped},
	"editors":                {audit: DeclineAuditNone},
	"formatter-ignore":       {audit: DeclineAuditSkipped},
	"renovate-ignore":        {audit: DeclineAuditNone},
	"actionlint-labels":      {audit: DeclineAuditNone},
	"contributing":           {audit: DeclineAuditNone},
	"pull-request-template":  {audit: DeclineAuditNone},
	"security-policy":        {audit: DeclineAuditNone},
	"adr":                    {audit: DeclineAuditNone},
	"readme":                 {audit: DeclineAuditSkipped},
	reuseGateStep:            {audit: DeclineAuditNone},
	"working-dir-and-flavor": {audit: DeclineAuditNone},
	"branch-ruleset":         {audit: DeclineAuditSkipped, mcp: true},
	"labels":                 {audit: DeclineAuditSkipped, mcp: true},
	"paperclip":              {audit: DeclineAuditSkipped},
	"agent-definitions":      {audit: DeclineAuditSkipped},
	"git-hooks":              {audit: DeclineAuditSkipped, mcp: true},
	"agent-hooks":            {audit: DeclineAuditNone},
}

// DeclineContract is the audit contract of one declinable adoption step.
type DeclineContract struct {
	Step  string
	Audit DeclineAudit
	// MCP reports that standards_audit reads the step's artefact too.
	MCP bool
	// Retains names what audit still requires while the step is declined.
	Retains string
}

// DeclineContracts lists the audit contract of every declinable step in chain order. The CLI and
// MCP audit tests enumerate it, so a step that becomes declinable has to say what audit does with
// its artefact, and a gate that reads that artefact has to look the decline up.
func DeclineContracts() []DeclineContract {
	names := adoptStepNames()
	contracts := make([]DeclineContract, 0, len(names))
	for i := 0; i < len(names) && i < maxAdoptSteps; i++ {
		if contract, ok := declineContracts[names[i]]; ok {
			contracts = append(contracts, DeclineContract{Step: names[i], Audit: contract.audit,
				MCP: contract.mcp, Retains: contract.retains})
		}
	}
	return contracts
}

// DeclineVerdict is what an audit gate reads from adoption.decline for the step whose artefact it
// checks (AuditDecline).
type DeclineVerdict struct {
	Step     string
	Declined bool
	// Retains names what audit still requires under the decline; empty when the step is not
	// declined or its decline covers every check of the gate.
	Retains string
}

// AuditDecline is the reader every audit gate, CLI and MCP, looks a decline up through: the step
// resolved by ManifestArtifactDeclined, the policy adoption applies to its own run, joined with
// the step's audit contract. An unknown or mandatory name anywhere in the list fails the gate
// closed, as it fails adoption, and so does asking for a step that cannot be declined.
func AuditDecline(manifest *config.Manifest, step string) (DeclineVerdict, error) {
	declined, err := ManifestArtifactDeclined(manifest, step)
	if err != nil {
		return DeclineVerdict{}, err
	}
	name := declinedName(step)
	contract, ok := declineContracts[name]
	if !ok {
		return DeclineVerdict{}, fmt.Errorf("adoption artefact %q cannot be declined", name)
	}
	verdict := DeclineVerdict{Step: name, Declined: declined}
	if declined {
		verdict.Retains = contract.retains
	}
	return verdict, nil
}

// Line is the report line of a gate whose step is declined. A skipped artefact passes with the
// decline named; a retained decline is information, printed once the checks it leaves required
// have passed.
func (v DeclineVerdict) Line(subject string) string {
	if v.Retains == "" {
		return "[PASS] " + subject + " declined by adoption.decline."
	}
	return "[INFO] " + subject + " declined by adoption.decline; audit still requires " + v.Retains + "."
}

// Narrow adds to err, the failure of a check the declined step does not cover, that the decline
// does not cover it and what audit still requires, so the failure never reads as a decline audit
// ignored. Any other err, nil included, is returned unchanged.
func (v DeclineVerdict) Narrow(err error) error {
	if err == nil || !v.Declined || v.Retains == "" {
		return err
	}
	return fmt.Errorf("%w (adoption.decline lists %s, which does not cover this check: audit still requires %s)",
		err, v.Step, v.Retains)
}

// declinedDetail is the report entry of a step adoption skips because the manifest declines it,
// naming what audit still requires when the decline is retained.
func declinedDetail(step string) string {
	detail := "Declined by adoption.decline in " + manifestFile
	if retains := declineContracts[step].retains; retains != "" {
		detail += "; audit still requires " + retains
	}
	return detail
}

// declinedReaders resolve, read-only, what a declined step leaves the steps after it without.
// Only the policy-catalog step has such an output: the effective policy (#603).
var declinedReaders = map[string]adoptStep{"policy-catalog": readDeclinedPolicyCatalog}

// skipDeclinedStep records a step the manifest declines instead of running it. A decline is
// recorded, not silent: the report says the manifest refused the artefact, so a reader can tell a
// declined surface from one adoption forgot. A step whose output later steps read resolves it
// read-only first (declinedReaders).
func skipDeclinedStep(ctx context.Context, s *adoptSession, name string) error {
	from := s.report.mark()
	if read, ok := declinedReaders[name]; ok {
		if err := read(ctx, s); err != nil {
			s.report.recordStep(name, StepFailed, from)
			return err
		}
	}
	s.report.recordSkipped(name, declinedDetail(name))
	s.report.recordStep(name, StepDeclined, from)
	return nil
}

// declinedArtifacts resolves the manifest's decline list into a lookup, rejecting names that
// match no artefact and names that may not be declined.
//
// An unknown name is an error rather than a no-op on purpose. A silently ignored typo reads
// exactly like a working declaration until the artefact it was meant to suppress reappears.
func declinedArtifacts(declared []string, known []string) (map[string]bool, error) {
	if len(declared) > maxDeclinedArtifacts {
		return nil, fmt.Errorf("adoption declines at most %d artefacts, got %d", maxDeclinedArtifacts, len(declared))
	}
	valid := make(map[string]bool, len(known))
	for i := 0; i < len(known) && i < maxAdoptSteps; i++ {
		valid[known[i]] = true
	}
	declined := make(map[string]bool, len(declared))
	for i := 0; i < len(declared) && i < maxDeclinedArtifacts; i++ {
		name := declinedName(declared[i])
		if name == "" {
			continue
		}
		if reason, mandatory := mandatoryArtifacts[name]; mandatory {
			return nil, fmt.Errorf("adoption cannot decline %q: %s", name, reason)
		}
		if !valid[name] {
			return nil, fmt.Errorf("adoption cannot decline unknown artefact %q; known artefacts are %s",
				name, strings.Join(sortedNames(known), ", "))
		}
		declined[name] = true
	}
	return declined, nil
}

// ArtifactDeclined resolves one named adoption step through the same bounded policy used by
// Adopt. Auditors call this instead of maintaining a second decline parser that can drift.
func ArtifactDeclined(declared []string, artifact string) (bool, error) {
	known := adoptStepNames()
	declined, err := declinedArtifacts(declared, known)
	if err != nil {
		return false, err
	}
	name := strings.ToLower(strings.TrimSpace(artifact))
	for i := 0; i < len(known) && i < maxAdoptSteps; i++ {
		if known[i] == name {
			return declined[name], nil
		}
	}
	return false, fmt.Errorf("unknown adoption artefact %q", name)
}

// ManifestArtifactDeclined resolves one adoption step against the adoption.decline list the
// manifest records. A manifest without an adoption policy declines nothing; an invalid list
// fails closed through ArtifactDeclined, so every auditor reads declines one way.
func ManifestArtifactDeclined(manifest *config.Manifest, artifact string) (bool, error) {
	return ArtifactDeclined(manifestDeclines(manifest), artifact)
}

// RepositoryArtifactDeclined resolves one adoption step against the adoption.decline list of the
// manifest at repoPath (ManifestArtifactDeclined). A repository without a manifest declines
// nothing. A manifest that cannot be read or decoded is an error, so a writer outside adoption,
// such as flavor apply rendering the branch ruleset, fails closed instead of writing a file the
// repository may have declined.
func RepositoryArtifactDeclined(ctx context.Context, repoPath, artifact string) (bool, error) {
	manifest, err := loadDeclaredManifest(ctx, repoPath)
	if err != nil {
		return false, err
	}
	return ManifestArtifactDeclined(manifest, artifact)
}

// manifestDeclines is the adoption.decline list a manifest records; nil declines nothing.
func manifestDeclines(manifest *config.Manifest) []string {
	if manifest == nil || manifest.Adoption == nil {
		return nil
	}
	return manifest.Adoption.Decline
}

// manifestProfiles is the profile list a manifest declares; nil declares none.
func manifestProfiles(manifest *config.Manifest) []string {
	if manifest == nil {
		return nil
	}
	return manifest.Profiles
}

func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// declaredManifest reads the repository's existing manifest once, before the chain runs, so
// its recorded decisions (adoption.decline, the declared profiles and facets) govern the run
// that follows rather than the run after next. It returns nil when there is no manifest yet,
// which is what a first adoption means, and when the manifest cannot be read: the manifest
// step parses it strictly and fails the run with the reason. unreadable tells the second case
// from the first, so the report never presents the facets of a manifest it could not read as
// the ones a first adoption declares (adoptionFacets).
func declaredManifest(ctx context.Context, repoPath string) (manifest *config.Manifest, unreadable bool) {
	manifest, err := loadDeclaredManifest(ctx, repoPath)
	if err != nil {
		return nil, true
	}
	return manifest, false
}

// loadDeclaredManifest reads and strictly decodes the repository's manifest (one bounded
// document, BUG-857) and applies every manifest validation, as config.LoadManifest does. A missing manifest is (nil, nil). A manifest that exists but cannot be
// resolved, read or decoded is an error, so a caller that writes on the strength of "not
// declined" (EnsurePrivateIgnore) fails closed instead of treating an unreadable decision as
// no decision.
func loadDeclaredManifest(ctx context.Context, repoPath string) (*config.Manifest, error) {
	full, err := repoFile(repoPath, manifestFile)
	if err != nil {
		return nil, fmt.Errorf("resolve the adoption manifest: %w", err)
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestFile, err)
	}
	if !exists {
		return nil, nil
	}
	manifest, err := config.ParseManifest(manifestFile, data)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", manifestFile, err)
	}
	return manifest, nil
}

// adoptStepNames returns the name of every step in the adoption chain, so tests and error
// messages enumerate the real chain rather than a second list that can drift from it.
func adoptStepNames() []string {
	steps := adoptSteps()
	names := make([]string, 0, len(steps))
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		names = append(names, steps[i].name)
	}
	return names
}

// declinedName normalizes one adoption.decline entry to the step name it declines.
func declinedName(entry string) string {
	return strings.ToLower(strings.TrimSpace(entry))
}

// declines reports whether the manifest's adoption.decline names step. Steps that plan an
// artefact another step writes read it, so a declined writer never leaves a planned file
// that nothing produces.
func (s *adoptSession) declines(step string) bool {
	for i := 0; i < len(s.declined) && i < maxDeclinedArtifacts; i++ {
		if declinedName(s.declined[i]) == step {
			return true
		}
	}
	return false
}
