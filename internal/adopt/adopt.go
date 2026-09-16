package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Invariant bounds (HISS-02: every loop in this package carries one of these caps).
const (
	maxInfractionsCap   = 10000
	defaultTimeout      = 30 * time.Second
	defaultMaxFuncLOC   = 60
	maxAdoptSteps       = 32
	maxTranspileTargets = 64
	maxEditorFiles      = 256
)

// Canonical repository-relative paths written by adoption.
const (
	manifestFile     = ".standards.yaml"
	lockFile         = ".standards.lock"
	baselineFile     = ".standards-baseline.json"
	agentsFile       = "AGENTS.md"
	devcontainerFile = ".devcontainer/devcontainer.json"
	workingDirPath   = ".workingdir"
	defaultOwner     = "cordanaLLM"
)

// RepositoryState describes the adoption state of a target codebase.
type RepositoryState string

const (
	StateGreenfield RepositoryState = "greenfield"
	StatePartial    RepositoryState = "partial"
	StateBrownfield RepositoryState = "brownfield"
)

// AdoptOptions controls repository adoption and template compliance.
type AdoptOptions struct {
	Path              string   `json:"path"`
	Profile           string   `json:"profile"`
	Facets            []string `json:"facets"`
	DryRun            bool     `json:"dry_run"`
	Force             bool     `json:"force"`
	RecordBaseline    bool     `json:"record_baseline"`
	SkipGitValidation bool     `json:"skip_git_validation"`
	// SkipHookActivation leaves generated hooks inactive in disposable analysis clones.
	SkipHookActivation bool `json:"skip_hook_activation,omitempty"`
	// LockSourceRoot selects the verified Praetor bundle used for new lock pins.
	LockSourceRoot string `json:"lock_source_root,omitempty"`
	// VerificationLimits overrides bounded metadata discovery for explicit adopters.
	VerificationLimits *VerificationLimits `json:"verification_limits,omitempty"`
}

// ActionDetail describes a specific planned or executed action on a target file.
type ActionDetail struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "create", "reconcile", "merge", "append", "skip"
	Details string `json:"details"`
}

// AdoptReport details the actions executed or simulated during adoption.
//
// Errors lists failures that left the repository short of the advertised state and
// must make the caller exit non-zero; Warnings lists deliberate safety skips (for
// example hooks that were not activated because their configuration was not written
// by praetor) that the caller should print but that do not fail the run.
type AdoptReport struct {
	Verification *VerificationPlan `json:"verification,omitempty"`
	// EffectivePolicy is the resolved planned/applied snapshot. Its source bytes
	// remain excluded by config's JSON contract; absence never implies defaults.
	EffectivePolicy *config.EffectivePolicy `json:"effective_policy,omitempty"`
	State           RepositoryState         `json:"state"`
	Archetype       string                  `json:"archetype"`
	Facets          []string                `json:"facets"`
	CreatedFiles    []string                `json:"created_files"`
	ReconciledFiles []string                `json:"reconciled_files"`
	ActionDetails   []ActionDetail          `json:"action_details,omitempty"`
	DebtBreakdown   map[string]int          `json:"debt_breakdown,omitempty"`
	LegacyDebtCount int                     `json:"legacy_debt_count"`
	// BaselineStatus distinguishes an observed zero from a skipped or unevaluated scan.
	BaselineStatus string   `json:"baseline_status"`
	DryRun         bool     `json:"dry_run"`
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

// adoptSession carries the resolved inputs of one adoption run through the step chain.
type adoptSession struct {
	repoPath     string
	repoName     string
	arch         string
	facets       []string
	opts         AdoptOptions
	report       *AdoptReport
	policy       *config.EffectivePolicy
	verification *VerificationPlan
	// declined carries adoption.decline from the repository's existing manifest, read before
	// the chain runs so a repository's recorded decision applies to the run that follows it.
	declined []string
}

// adoptStep is one reconciliation step of the adoption chain.
type adoptStep func(ctx context.Context, s *adoptSession) error

// Adopt brings any repository to 100% template and governance compliance.
//
// When a step fails, the returned report still lists every file written before the
// failure so that callers can show what a partially adopted repository now contains.
func Adopt(ctx context.Context, opts AdoptOptions) (*AdoptReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("adopt: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("adopt cancelled: %w", err)
	}

	normPath, err := resolveTargetPath(opts)
	if err != nil {
		return nil, err
	}
	report := newAdoptionReport(normPath, opts)
	verification, err := resolveVerificationPlanWithLimits(ctx, normPath, opts.VerificationLimits)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, fmt.Errorf("resolve project verification: %w", err)
	}

	arch := resolveArchetype(normPath, opts.Profile)
	if opts.Profile == "" && containsRuntime(verification, "dotnet") {
		arch = "app-service"
	}
	report.Archetype = arch
	report.Verification = verification
	s := &adoptSession{
		repoPath:     normPath,
		repoName:     resolveRepoName(ctx, normPath),
		arch:         arch,
		facets:       report.Facets,
		opts:         opts,
		report:       report,
		verification: verification,
		declined:     declaredDeclines(ctx, normPath),
	}
	report.addWarning("%s", verification.notice())

	if err := executeAdoptSteps(ctx, s); err != nil {
		report.addError("%s", err)
		return report, err
	}
	report.EffectivePolicy = s.policy
	return report, nil
}

func newAdoptionReport(path string, opts AdoptOptions) *AdoptReport {
	return &AdoptReport{
		State:           DetectState(path),
		Archetype:       resolveArchetype(path, opts.Profile),
		Facets:          resolveFacets(opts.Facets),
		CreatedFiles:    make([]string, 0),
		ReconciledFiles: make([]string, 0),
		ActionDetails:   make([]ActionDetail, 0),
		DebtBreakdown:   make(map[string]int),
		DryRun:          opts.DryRun,
		BaselineStatus:  "not_run",
		Errors:          make([]string, 0),
		Warnings:        make([]string, 0),
	}
}

// resolveTargetPath normalises opts.Path and validates that it is an adoptable target.
func resolveTargetPath(opts AdoptOptions) (string, error) {
	normPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return "", fmt.Errorf("resolve repo path %q: %w", opts.Path, err)
	}
	if !opts.SkipGitValidation {
		if err := ValidateAdoptionTarget(normPath); err != nil {
			return "", fmt.Errorf("adoption validation failed: %w", err)
		}
		return normPath, nil
	}
	info, err := os.Stat(normPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("target path %q must be an existing directory", normPath)
	}
	return normPath, nil
}

// DetectState determines if a repository is greenfield, partial, or brownfield.
func DetectState(repoPath string) RepositoryState {
	manifestExists := fileExists(filepath.Join(repoPath, manifestFile))
	lockExists := fileExists(filepath.Join(repoPath, lockFile))
	agentsExists := fileExists(filepath.Join(repoPath, agentsFile))
	baselineExists := fileExists(filepath.Join(repoPath, baselineFile))

	if !manifestExists && !lockExists && !agentsExists {
		return StateGreenfield
	}
	if manifestExists && lockExists && agentsExists && baselineExists {
		return StateBrownfield
	}
	return StatePartial
}

// resolveArchetype decides which profile a repository is adopted under.
//
// The marker table this used to carry now lives in internal/classify, because it was one of
// three copies that disagreed with each other. What is left here is the precedence: an operator's
// explicit profile beats detection, and where nothing matches the fallback is named rather than
// returned as though it were a match.
func resolveArchetype(repoPath, explicitProfile string) string {
	decision := classify.Resolve(
		classify.Explicit(explicitProfile),
		classify.ByMarkers(repoPath),
	)
	return decision.Or(classify.FallbackArchetype)
}

// resolveOwner returns the owner from the origin remote or directory layout, falling
// back to the fleet default. The lookup runs under the caller's context.
func resolveOwner(ctx context.Context, repoPath string) string {
	owner, _, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil || owner == "" {
		return defaultOwner
	}
	return owner
}

// resolveRepoName returns the repository name from the origin remote or directory
// layout, falling back to the directory basename.
func resolveRepoName(ctx context.Context, repoPath string) string {
	_, repo, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil || repo == "" {
		return filepath.Base(repoPath)
	}
	return repo
}

func resolveFacets(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
}

// adoptSteps is the reconciliation chain, in order. It is a function so the step names
// have exactly one definition: a second list would drift from the one that runs.
func adoptSteps() []namedStep {
	return []namedStep{
		{"manifest", reconcileManifest},
		{"lockfile", reconcileLockfile},
		{"policy-catalog", reconcilePolicyCatalog},
		{"baseline", reconcileBaseline},
		{"agent-harness", reconcileAgentHarness},
		{"dev-container", reconcileDevContainer},
		{"editors", reconcileEditors},
		{"makefile", reconcileMakefile},
		{"git-ignore", reconcileGitIgnore},
		{"formatter-ignore", reconcileFormatterIgnore},
		{"contributing", reconcileContributing},
		{"pull-request-template", reconcilePullRequestTemplate},
		{"security-policy", reconcileSecurityPolicy},
		{"adr", reconcileADR},
		{"readme", reconcileReadme},
		{"branch-ruleset", reconcileBranchRuleset},
		{"labels", reconcileLabels},
		{"paperclip", reconcilePaperclip},
		{"agent-definitions", reconcileAgentDefinitions},
		{"working-dir-and-flavor", reconcileWorkingDirAndFlavor},
		{"git-hooks", reconcileGitHooks},
	}
}

// executeAdoptSteps runs the reconciliation chain in order, stopping at the first
// failure and observing context cancellation between steps.
func executeAdoptSteps(ctx context.Context, s *adoptSession) error {
	steps := adoptSteps()
	known := make([]string, 0, len(steps))
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		known = append(known, steps[i].name)
	}
	declined, err := declinedArtifacts(s.declined, known)
	if err != nil {
		return err
	}
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("adopt cancelled: %w", err)
		}
		// A decline is recorded, not silent: the report says the artefact was refused by the
		// manifest, so a reader can tell a declined surface from one adoption forgot.
		if declined[steps[i].name] {
			s.report.recordSkipped(steps[i].name, "Declined by adoption.decline in "+manifestFile)
			continue
		}
		if err := steps[i].run(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// forcedManifestNote explains why --force left the manifest alone, and says what to do instead.
// A repository whose manifest is genuinely wrong has to remove it deliberately; that is a
// visible act, whereas the previous behaviour overwrote the declaration without saying so.
func forcedManifestNote(forced bool) string {
	if forced {
		return "Existing standards manifest preserved; --force refreshes scaffolds, not declared " +
			"profiles and facets. Remove " + manifestFile + " to rescaffold it deliberately"
	}
	return "Existing standards manifest verified present"
}

func reconcileManifest(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return err
	}
	// --force regenerates scaffolds, never the manifest. The manifest is the repository's own
	// declaration of what it is: an input to governance rather than an output of it. Rewriting
	// it from detected markers replaced a declared profile and facet set with guessed ones and
	// still reported success, which is governance data loss dressed as adoption.
	if fileExists(full) {
		s.report.recordReconciled(manifestFile, forcedManifestNote(s.opts.Force))
		return nil
	}
	manifest := newAdoptionManifest(ctx, s)
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := s.write(full, data, filePerm); err != nil {
		return err
	}
	s.report.recordCreated(manifestFile, fmt.Sprintf("Scaffolded standards manifest (Owner: %s, Profile: %s)", manifest.Repository.Owner, s.arch))
	return nil
}

// reconcileBaseline records the legacy-debt baseline. A scan that does not complete is
// an error: persisting an empty baseline would silently mis-anchor the HISS-13 ratchet.
func reconcileBaseline(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, baselineFile)
	if err != nil {
		return err
	}
	existed := fileExists(full)
	if existed && !s.opts.RecordBaseline {
		s.report.BaselineStatus = "existing"
		return s.verifyExistingBaseline(full)
	}

	base := &baseline.Baseline{Version: 1, Infractions: make([]baseline.Infraction, 0)}
	if s.opts.RecordBaseline {
		if err := scanLegacyDebt(ctx, s.repoPath, base, s.report, adoptionScanLimit(s)); err != nil {
			s.report.BaselineStatus = "failed"
			return err
		}
		s.report.BaselineStatus = "scanned"
	} else {
		s.report.BaselineStatus = "skipped"
	}
	s.report.LegacyDebtCount = base.TotalInfractions
	if !s.opts.DryRun {
		if err := baseline.SaveBaseline(full, base); err != nil {
			s.report.BaselineStatus = "failed"
			return fmt.Errorf("save baseline: %w", err)
		}
	}
	detail := fmt.Sprintf("Recorded %d legacy debt infractions into baseline", base.TotalInfractions)
	if existed {
		s.report.recordReconciled(baselineFile, "Rescanned and "+lowerFirst(detail))
		return nil
	}
	s.report.recordCreated(baselineFile, detail)
	return nil
}

// verifyExistingBaseline keeps an existing baseline and exposes its debt count so that
// later steps (the README badge) reflect the recorded state.
func (s *adoptSession) verifyExistingBaseline(full string) error {
	base, err := baseline.LoadBaseline(full)
	if err != nil {
		s.report.addError("baseline: existing %s is unreadable: %v", baselineFile, err)
		s.report.BaselineStatus = "failed"
		return fmt.Errorf("load existing baseline: %w", err)
	} else {
		s.report.LegacyDebtCount = base.TotalInfractions
	}
	s.report.recordReconciled(baselineFile, "Technical debt baseline verified present")
	return nil
}

// scanLegacyDebt fills base with the current HISS infractions of repoPath. The scan is
// bounded by defaultTimeout and derived from the caller's context.
func scanLegacyDebt(ctx context.Context, repoPath string, base *baseline.Baseline, report *AdoptReport, maxFuncLOC int) error {
	scanCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	scanRep, err := hiss.Scan(scanCtx, repoPath, hiss.ScanOptions{
		MaxFuncLOC: maxFuncLOC,
		Cap:        maxInfractionsCap,
	})
	if err != nil {
		return fmt.Errorf("scan legacy debt in %s: %w", repoPath, err)
	}

	if scanRep.Truncated {
		return fmt.Errorf("scan legacy debt in %s: %w", repoPath, hiss.ErrScanTruncated)
	}

	for i := 0; i < len(scanRep.Violations) && i < maxInfractionsCap; i++ {
		v := scanRep.Violations[i]
		// The recorded path is normalised so a baseline written on one platform is readable
		// as the same record on another. Comparison normalises too, so an already-committed
		// Windows baseline keeps working; this stops new ones from being written that way.
		path := baseline.NormalizePath(v.FilePath)
		base.Infractions = append(base.Infractions, baseline.Infraction{
			RuleID:      v.RuleID,
			FilePath:    path,
			LineNumber:  v.LineNumber,
			Symbol:      v.Symbol,
			Message:     v.Message,
			Fingerprint: fmt.Sprintf("%s:%d:%s", path, v.LineNumber, v.RuleID),
		})
	}
	base.TotalInfractions = len(base.Infractions)
	for k, count := range scanRep.Breakdown {
		report.DebtBreakdown[k] = count
	}
	return nil
}

func reconcileDevContainer(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, devcontainerFile)
	if err != nil {
		return err
	}
	if fileExists(full) && !s.opts.Force {
		s.report.recordReconciled(devcontainerFile, "Existing DevContainer preserved; container startup has not been verified by adoption")
		s.report.addWarning("Existing DevContainer preserved; bootstrap readiness requires separate verification.")
		return nil
	}
	bundle, err := prepareAdoptDevContainer(ctx, s)
	if err != nil {
		return fmt.Errorf("prepare devcontainer bootstrap: %w", err)
	}
	if bundle.Spec().State == devcontainer.BootstrapUnavailable {
		s.report.addWarning("DevContainer bootstrap unavailable: %s", bundle.Spec().Reason)
	}
	if !s.opts.DryRun {
		if err := devcontainer.WriteBundle(ctx, full, bundle, s.opts.Force); err != nil {
			return err
		}
	}
	s.report.recordCreated(devcontainerFile, fmt.Sprintf("Prepared DevContainer for archetype '%s'; bootstrap %s, execution unverified", s.arch, bundle.Spec().State))
	for _, artifact := range bundle.Artifacts {
		s.report.recordCreated(filepath.ToSlash(filepath.Join(".devcontainer", artifact.Name)), "Prepared exact DevContainer bootstrap companion")
	}
	return nil
}

// reconcileEditors writes IDE configurations that do not exist yet. Existing files are
// only regenerated with Force, and .clang-tidy/.editorconfig are always preserved
// because they carry hand-tuned project settings.
func reconcileEditors(_ context.Context, s *adoptSession) error {
	edOpts := editor.DefaultOptions()
	edOpts.WorkspaceRoot = s.repoPath
	edOpts.Archetype = s.arch
	set, err := editor.Synthesize(edOpts)
	if err != nil {
		return fmt.Errorf("synthesize editors: %w", err)
	}

	for i := 0; i < len(set.Files) && i < maxEditorFiles; i++ {
		if err := s.reconcileEditorFile(set.Files[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *adoptSession) reconcileEditorFile(f editor.GeneratedFile) error {
	full, err := repoFile(s.repoPath, f.Path)
	if err != nil {
		return err
	}
	if !fileExists(full) {
		if err := s.write(full, []byte(f.Content), filePerm); err != nil {
			return err
		}
		s.report.recordCreated(f.Path, fmt.Sprintf("Synthesized %s IDE configuration for archetype '%s'", f.Editor, s.arch))
		return nil
	}
	if s.opts.Force && !isUserOwnedEditorFile(f.Path) {
		if err := s.write(full, []byte(f.Content), filePerm); err != nil {
			return err
		}
		s.report.recordReconciled(f.Path, fmt.Sprintf("Regenerated %s IDE configuration for archetype '%s'", f.Editor, s.arch))
		return nil
	}
	s.report.recordReconciled(f.Path, fmt.Sprintf("Existing %s IDE configuration preserved (use --force to regenerate)", f.Editor))
	return nil
}

// isUserOwnedEditorFile reports whether an IDE file is never overwritten, mirroring the
// preservation rule of the editor package.
func isUserOwnedEditorFile(rel string) bool {
	return rel == ".clang-tidy" || rel == ".editorconfig"
}

func reconcileWorkingDirAndFlavor(ctx context.Context, s *adoptSession) error {
	if !s.opts.DryRun {
		if err := state.InitWorkingDirContext(ctx, s.repoPath); err != nil {
			s.report.addError("workingdir init: %v", err)
		}
		detectedFlv := flavor.DetectFlavor(s.repoPath)
		if _, err := flavor.ApplyFlavor(ctx, s.repoPath, detectedFlv, false); err != nil {
			s.report.addError("apply flavor %s: %v", detectedFlv, err)
		}
	}
	s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{
		Path:    workingDirPath,
		Action:  actionCreate,
		Details: "Initialized canonical session state ledger and bug/question journals",
	})
	return nil
}

// lowerFirst lower-cases the first byte of an ASCII detail string.
func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]+('a'-'A')) + s[1:]
}
