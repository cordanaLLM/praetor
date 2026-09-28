package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant bounds (HISS-02: every loop in this package carries one of these caps).
const (
	maxInfractionsCap   = 10000
	defaultTimeout      = 30 * time.Second
	maxAdoptSteps       = 32
	maxTranspileTargets = 64
	maxEditorFiles      = 256
)

// Canonical repository-relative paths written by adoption.
const (
	manifestFile     = config.ManifestFileName
	lockFile         = config.LockFileName
	baselineFile     = ".standards-baseline.json"
	agentsFile       = "AGENTS.md"
	devcontainerFile = ".devcontainer/devcontainer.json"
	workingDirPath   = ".workingdir"
	// flavorReportPath names the flavor scaffold in the report when no template path applies.
	flavorReportPath = "flavor"
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
//
// "replace" marks existing bytes that were neither the content adoption writes nor an earlier
// Praetor text of it, overwritten all the same: --force over a drifted scaffold or an edited
// audit-locked file (lock, pinned catalog, DevContainer bundle, Makefile documentation block),
// and, on any run, a hand-edited vendor context file (vendor_targets.go). Its Details
// carry a bounded line delta and where the prior bytes were kept, or why they were not
// (replaceExisting); the path is listed in ReconciledFiles, never in CreatedFiles.
type ActionDetail struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "create", "reconcile", "merge", "append", "replace", "remove", "skip"
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
	// Previews shows, in a dry run only, the content or the diff of each file adoption renders
	// from repository state (FilePreview): the branch protection ruleset.
	Previews        []FilePreview  `json:"previews,omitempty"`
	DebtBreakdown   map[string]int `json:"debt_breakdown,omitempty"`
	LegacyDebtCount int            `json:"legacy_debt_count"`
	// BaselineStatus distinguishes an observed zero from a skipped or unevaluated scan.
	BaselineStatus string   `json:"baseline_status"`
	DryRun         bool     `json:"dry_run"`
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	// Steps records each reached step of the chain; Pillars derives the governance pillar
	// lines from it.
	Steps []StepOutcome `json:"steps,omitempty"`
}

// adoptSession carries the resolved inputs of one adoption run through the step chain.
type adoptSession struct {
	repoPath string
	// repoName labels generated prose (the harness title, the contributor guide). It is the
	// resolved repository name, else the checkout directory's name, and is never written into
	// an identity field: those read identity, which stays empty when unresolved.
	repoName     string
	identity     repoIdentity
	arch         string
	facets       []string
	opts         AdoptOptions
	report       *AdoptReport
	policy       *config.EffectivePolicy
	verification *VerificationPlan
	// declined carries adoption.decline from the repository's existing manifest, read before
	// the chain runs so a repository's recorded decision applies to the run that follows it.
	declined []string
	// cleanupGoto is the cleanup-goto exception the existing manifest declares and documents
	// (config.Manifest.CleanupGotoException), read before the chain runs like declined: the
	// legacy-debt scan honours it as the audit does, and exceptions states it in the harnesses
	// (harnessExceptions).
	cleanupGoto hiss.CleanupGoto
	exceptions  hisscatalog.Exception
	// paperclipLimit is the function length the Paperclip harness states, resolved once per run
	// (harnessFuncLOC) so every step renders the harness the manifest step bound.
	paperclipLimit struct {
		resolved bool
		limit    int
	}
	// dryRunWrites holds, in a dry run only, what each file the run would scaffold or remove
	// comes to (planDryRunWrite, planDryRunRemoval), so a later step previews against the tree
	// the run leaves rather than the one on disk.
	dryRunWrites map[string][]byte
	// rulesetBaseline is the policy and the workflows of the repository as this adoption found
	// it, read before any step writes (readRulesetBaseline); nil without a ruleset on disk.
	rulesetBaseline *forge.RulesetBaseline
	// backupStamp names this run's directory below adoptBackupRoot; backupPath fixes it on
	// first use when the session was built without one.
	backupStamp string
	// defaultBranch is the repository.default_branch the manifest this adoption creates declares
	// (resolveDefaultBranch, forge.DefaultBranchToDeclare); empty when there is nothing to declare
	// or the manifest exists.
	defaultBranch string
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
	declared := declaredManifest(ctx, normPath)
	decision := resolveArchetype(normPath, opts.Profile, manifestProfiles(declared))
	report := newAdoptionReport(normPath, opts, decision)
	verification, err := resolveVerificationPlanWithLimits(ctx, normPath, opts.VerificationLimits)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, fmt.Errorf("resolve project verification: %w", err)
	}
	report.Archetype = adoptionArchetype(decision, verification)
	report.Verification = verification
	cleanupGoto, undocumented := declared.CleanupGotoException(normPath)
	if undocumented != "" {
		report.addWarning("%s", undocumented)
	}
	s := &adoptSession{
		repoPath:     normPath,
		arch:         report.Archetype,
		facets:       report.Facets,
		opts:         opts,
		report:       report,
		verification: verification,
		declined:     manifestDeclines(declared),
		cleanupGoto:  cleanupGoto,
		exceptions:   harnessExceptions(cleanupGoto),
		backupStamp:  newBackupStamp(),
	}
	if err := s.resolveIdentity(ctx); err != nil {
		report.addError("%s", err)
		return report, err
	}
	if err := s.resolveDefaultBranch(ctx, declared); err != nil {
		report.addError("%s", err)
		return report, err
	}
	report.addWarning("%s", verification.notice())
	s.rulesetBaseline = readRulesetBaseline(ctx, s)

	if err := executeAdoptSteps(ctx, s); err != nil {
		report.addError("%s", err)
		return report, err
	}
	report.EffectivePolicy = s.policy
	return report, nil
}

func newAdoptionReport(path string, opts AdoptOptions, decision classify.Result) *AdoptReport {
	report := &AdoptReport{
		State:           DetectState(path),
		Archetype:       decision.Or(classify.FallbackArchetype),
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
	if explicit := strings.TrimSpace(opts.Profile); decision.Source == classify.SourceDeclared && explicit != "" &&
		explicit != decision.Archetype {
		report.addWarning("--profile %s ignored: %s declares %s, and adoption never rewrites a declared profile",
			explicit, manifestFile, decision.Archetype)
	}
	return report
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
// three copies that disagreed with each other. What is left here is the precedence classify.Resolve
// documents: the profile the repository's own manifest declares, then an operator's explicit
// profile, then detection. The manifest is never rewritten, so adopting under any other profile
// would scaffold for one archetype while the lock and audit enforce the declared one.
func resolveArchetype(repoPath, explicitProfile string, declaredProfiles []string) classify.Result {
	return classify.Resolve(
		classify.FromDeclaration(declaredProfiles),
		classify.Explicit(explicitProfile),
		classify.ByMarkers(repoPath),
	)
}

// adoptionArchetype names the archetype for a decision. A .NET runtime is served by
// app-service when only markers (or nothing) decided; a declared or explicit profile stands.
func adoptionArchetype(decision classify.Result, verification *VerificationPlan) string {
	pinned := decision.Source == classify.SourceDeclared || decision.Source == classify.SourceExplicit
	if !pinned && containsRuntime(verification, "dotnet") {
		return "app-service"
	}
	return decision.Or(classify.FallbackArchetype)
}

// repoIdentity is the forge identity adoption records: owner and name from the origin remote
// (util.ReadOriginRemote). Both stay empty without one; adoption substitutes no default
// owner and never reads identity from the checkout path, whose parent directory names
// wherever the checkout happens to sit rather than the repository's owner. host and path are
// the remote's host and full repository path, which forgeHost compares with an identity.
type repoIdentity struct {
	owner string
	name  string
	host  string
	path  string
}

// forgeHost returns the origin remote's host when the remote's repository path is exactly
// owner/name, the identity a manifest records, and "" otherwise: the remote then does not
// say which forge hosts that repository. A nested namespace such as group/sub/name on
// GitLab is not owner/name either, so its host is never paired with the last two segments.
func (id repoIdentity) forgeHost(owner, name string) string {
	if id.host == "" || owner == "" || name == "" || !strings.EqualFold(id.path, owner+"/"+name) {
		return ""
	}
	return id.host
}

func (id repoIdentity) resolved() bool {
	return id.owner != "" && id.name != ""
}

// coordinate is the "owner/name" form, or "" when the identity is unresolved.
func (id repoIdentity) coordinate() string {
	if !id.resolved() {
		return ""
	}
	return id.owner + "/" + id.name
}

// resolveIdentity fills the session's identity and prose label under the caller's context. An
// unresolved identity is a warning, because every identity field adoption writes stays empty.
// The origin remote is the only source: the checkpoint evaluator checks the policy's repository
// against that remote, so no other source could name a repository it accepts. A remote read
// git did not answer (a cancelled context, a git failure) is an error, not an unresolved
// identity: adoption must not write an empty identity for a question it never got answered.
func (s *adoptSession) resolveIdentity(ctx context.Context) error {
	remote, err := util.ReadOriginRemote(ctx, s.repoPath)
	if err == nil {
		s.identity = repoIdentity{owner: remote.Owner, name: remote.Repo, host: remote.Host, path: remote.Path}
		s.repoName = remote.Repo
		return nil
	}
	if !errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return fmt.Errorf("resolve repository identity: %w", err)
	}
	s.repoName = filepath.Base(s.repoPath)
	s.report.addWarning("repository identity unresolved (%v): adoption writes no repository.owner or repository.name "+
		"and installs no checkpoint lifecycle. praetorctl audit fails until %s names both: set them by hand, since "+
		"adoption never rewrites an existing manifest, and add an origin remote naming <owner>/<repo> before "+
		"re-running adoption to install the checkpoint lifecycle", err, manifestFile)
	return nil
}

func resolveFacets(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return config.DefaultFacets()
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
		{"documentation-gate", reconcileDocumentationGate},
		{"makefile", reconcileMakefile},
		{"git-ignore", reconcileGitIgnore},
		{"formatter-ignore", reconcileFormatterIgnore},
		{"renovate-ignore", reconcileRenovateIgnore},
		{"contributing", reconcileContributing},
		{"pull-request-template", reconcilePullRequestTemplate},
		{"security-policy", reconcileSecurityPolicy},
		{"adr", reconcileADR},
		{"readme", reconcileReadme},
		// Ahead of branch-ruleset: the flavor scaffolds CI workflows, and the ruleset's
		// required status checks are derived from the workflows present. Run after it, the
		// first adoption certified a ruleset missing the scaffolded jobs and the next run
		// rewrote it.
		{"working-dir-and-flavor", reconcileWorkingDirAndFlavor},
		{"branch-ruleset", reconcileBranchRuleset},
		{"labels", reconcileLabels},
		{"paperclip", reconcilePaperclip},
		{"agent-definitions", reconcileAgentDefinitions},
		{"git-hooks", reconcileGitHooks},
		{"agent-hooks", reconcileAgentHooks},
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
	if err := preflightAgentSurfaces(ctx, s, declined); err != nil {
		return err
	}
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("adopt cancelled: %w", err)
		}
		// A decline is recorded, not silent: the report says the artefact was refused by the
		// manifest, so a reader can tell a declined surface from one adoption forgot.
		name := steps[i].name
		if declined[name] {
			s.report.recordSkipped(name, "Declined by adoption.decline in "+manifestFile)
			s.report.recordStep(name, StepDeclined, s.report.mark())
			continue
		}
		from := s.report.mark()
		if err := steps[i].run(ctx, s); err != nil {
			s.report.recordStep(name, StepFailed, from)
			return err
		}
		s.report.recordStep(name, StepCompleted, from)
	}
	return nil
}

// preflightAgentSurfaces refuses, before the first step writes anything, an agent file the
// agent-harness, agent-definitions or agent-hooks step would refuse to write: a vendor file,
// canonical persona or persona copy behind a symlinked directory such as .agents or .github, a
// native hook file, or the backup root a merge copies it to, that is a symlink or sits behind
// one, or an existing one that is not a regular text file or, for a hook file, cannot be
// merged. Those steps write through the root-pinned writer, which refuses the same files at
// write time; checked only there, the refusal came after the manifest, the vendor files, the
// pull request template and the workflows were written, and left a half-adopted repository.
// A declined step's files are not checked, and a dry run is checked too, so its preview does
// not report a run that would fail. Under --force the backup root is checked whatever the steps
// (preflightForceBackupRoot): a replaced scaffold is backed up there from any step. Without it,
// the root is checked when the agent-harness step may replace a hand-edited vendor file
// (preflightVendorBackupRoot).
func preflightAgentSurfaces(ctx context.Context, s *adoptSession, declined map[string]bool) error {
	if !declined["agent-harness"] {
		if err := compiler.CheckVendorTargets(ctx, s.repoPath); err != nil {
			return fmt.Errorf("agent-harness preflight: %w", err)
		}
		if err := preflightVendorBackupRoot(ctx, s); err != nil {
			return fmt.Errorf("agent-harness preflight: %w", err)
		}
	}
	if !declined["agent-definitions"] {
		if err := preflightPersonas(ctx, s.repoPath); err != nil {
			return fmt.Errorf("agent-definitions preflight: %w", err)
		}
	}
	if !declined["agent-hooks"] {
		if err := preflightAgentHooks(ctx, s.repoPath); err != nil {
			return fmt.Errorf("agent-hooks preflight: %w", err)
		}
	}
	return preflightForceBackupRoot(ctx, s)
}

// preflightPersonas runs the persona writer's refusals over every persona agent-definitions
// writes, before any is written.
func preflightPersonas(ctx context.Context, repoPath string) error {
	personas := generatedPersonas()
	names := make([]string, 0, len(personas))
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		names = append(names, filepath.Base(personas[i].rel))
	}
	return compiler.CheckPersonaTargets(ctx, repoPath, names)
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
		return reconcileExistingManifest(ctx, s, full)
	}
	return createAdoptionManifest(ctx, s, full)
}

func createAdoptionManifest(ctx context.Context, s *adoptSession, full string) error {
	manifest, harness, err := newAdoptionManifest(ctx, s)
	if err != nil {
		return err
	}
	data, err := config.RenderManifest(manifest)
	if err != nil {
		return err
	}
	if err := s.write(full, data, filePerm); err != nil {
		return err
	}
	repository := s.identity.coordinate()
	if repository == "" {
		repository = "unresolved"
	}
	note := fmt.Sprintf("Scaffolded standards manifest (Repository: %s, Profile: %s; "+
		"visibility left unset, adoption cannot observe it)", repository, s.arch)
	if manifest.Repository.DefaultBranch != "" {
		note += fmt.Sprintf("; repository.default_branch: %s recorded from the origin remote's HEAD, "+
			"so a checkout without it renders the same ruleset", manifest.Repository.DefaultBranch)
	}
	if manifest.Register == nil {
		note += "; register.sources not added: " + unboundSourcesReason(harness)
	}
	s.report.recordCreated(manifestFile, note)
	return nil
}

func reconcileExistingManifest(ctx context.Context, s *adoptSession, full string) error {
	data, _, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	plan, changed, err := planExistingManifest(ctx, s, full, data)
	if err != nil {
		return err
	}
	if changed && !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, full, plan.data,
			contextopt.ReplaceOptions{Expected: data, Exists: true, Mode: filePerm}); err != nil {
			return err
		}
	}
	s.report.recordReconciled(manifestFile, plan.note)
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

	if !s.opts.RecordBaseline {
		// Recording was declined and there is no baseline to keep, so write nothing. Writing an
		// empty one is not neutral: it asserts total_infractions: 0 with a fresh timestamp, it is
		// indistinguishable on disk from a scan that genuinely found no debt, and the ratchet
		// then reads every pre-existing infraction as new -- so a repository adopted this way
		// could not commit, and the audit blamed its existing code rather than this flag (#138).
		s.report.BaselineStatus = "skipped"
		s.report.recordSkipped(baselineFile, "Not recorded (--record-baseline=false); no baseline was written. "+
			"Record one before committing, or every existing infraction is ratcheted as new debt")
		return nil
	}
	commit, err := state.RecordedCommit(ctx, s.repoPath)
	if err != nil {
		s.report.BaselineStatus = "failed"
		return fmt.Errorf("baseline commit: %w", err)
	}
	// The session resolved the repository once (resolveIdentity); "" when the origin remote
	// names none, as every other identity field adoption writes.
	base := &baseline.Baseline{Version: 1, Repository: s.identity.coordinate(), CommitSHA: commit,
		Infractions: make([]baseline.Infraction, 0)}
	if err := scanLegacyDebt(ctx, s.repoPath, base, s.report, s.legacyDebtScanOptions()); err != nil {
		s.report.BaselineStatus = "failed"
		return err
	}
	s.report.BaselineStatus = "scanned"
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
		s.report.LegacyDebtCount = base.Count()
	}
	s.report.recordReconciled(baselineFile, "Technical debt baseline verified present")
	return nil
}

// legacyDebtScanOptions scans the legacy debt as the audit will judge it: under the function
// length the audit enforces (adoptionScanLimit) and the cleanup-goto exception the manifest
// declares and documents.
func (s *adoptSession) legacyDebtScanOptions() hiss.ScanOptions {
	return hiss.ScanOptions{MaxFuncLOC: adoptionScanLimit(s), Cap: maxInfractionsCap, CleanupGoto: s.cleanupGoto}
}

// scanLegacyDebt fills base with the current HISS infractions of repoPath. The scan is
// bounded by defaultTimeout and derived from the caller's context.
func scanLegacyDebt(ctx context.Context, repoPath string, base *baseline.Baseline, report *AdoptReport, opts hiss.ScanOptions) error {
	scanCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	scanRep, err := hiss.Scan(scanCtx, repoPath, opts)
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
	plan, err := devcontainer.PlanBundle(ctx, full, bundle, s.opts.Force)
	if err != nil {
		return err
	}
	configDetail := fmt.Sprintf("Prepared DevContainer for archetype '%s'; bootstrap %s, execution unverified", s.arch, bundle.Spec().State)
	return publishDevContainerPlan(ctx, s, plan, configDetail)
}

// publishDevContainerPlan publishes a planned DevContainer bundle (a dry run writes nothing)
// and records each of its files: an existing file holding operator bytes, which only --force
// admits, is replaced with a backup (replaceExistingAll), Praetor's own unedited unavailable
// placeholder is refreshed, a file already holding its bytes is verified and an absent one is
// created. The plan binds every write to the bytes it observed.
func publishDevContainerPlan(ctx context.Context, s *adoptSession, plan *devcontainer.BundlePlan, configDetail string) error {
	files := plan.Files()
	replaced := make([]replacement, 0, len(files))
	for _, file := range files {
		if devContainerFileStateOf(file) == devContainerReplaced {
			replaced = append(replaced, replacement{rel: devContainerRel(file), before: file.Before, after: file.After,
				detail: devContainerDetail(file, configDetail)})
		}
	}
	if err := s.replaceExistingAll(ctx, replaced, plan.Publish); err != nil {
		return err
	}
	for _, file := range files {
		rel, detail := devContainerRel(file), devContainerDetail(file, configDetail)
		switch devContainerFileStateOf(file) {
		case devContainerCreated:
			s.report.recordCreated(rel, detail)
		case devContainerUnchanged:
			s.report.recordReconciled(rel, "Verified unchanged: "+lowerFirst(detail))
		case devContainerRefreshed:
			s.report.recordReconciled(rel, "Refreshed Praetor's own unedited unavailable placeholder: "+lowerFirst(detail))
		case devContainerReplaced:
			// Recorded by replaceExistingAll with its line delta and backup.
		}
	}
	return nil
}

// devContainerFileState is what a bundle write does with one planned file.
type devContainerFileState int

const (
	devContainerCreated   devContainerFileState = iota + 1 // absent, created
	devContainerUnchanged                                  // already holds its planned bytes
	devContainerRefreshed                                  // Praetor's own unedited placeholder
	devContainerReplaced                                   // operator bytes, replaced under --force
)

// devContainerFileStateOf classifies one planned bundle file.
func devContainerFileStateOf(file devcontainer.BundleFile) devContainerFileState {
	switch {
	case !file.Existed:
		return devContainerCreated
	case bytes.Equal(file.Before, file.After):
		return devContainerUnchanged
	case file.Placeholder:
		return devContainerRefreshed
	}
	return devContainerReplaced
}

// devContainerRel is the repository path of a planned bundle file, all of which lie in the
// DevContainer directory.
func devContainerRel(file devcontainer.BundleFile) string {
	return path.Join(path.Dir(devcontainerFile), filepath.Base(file.Path))
}

// devContainerDetail is what adoption writes at a planned bundle file.
func devContainerDetail(file devcontainer.BundleFile, configDetail string) string {
	if devContainerRel(file) == devcontainerFile {
		return configDetail
	}
	return "Prepared exact DevContainer bootstrap companion"
}

// reconcileEditors writes the IDE configurations of the selected editors that do not exist yet
// and resolves each existing one without overwriting it (reconcileEditorFile).
func reconcileEditors(ctx context.Context, s *adoptSession) error {
	declared, err := config.LoadDeclaredTooling(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read editors selection from %s: %w", manifestFile, err)
	}
	selection, err := editor.SelectEditors(declared.Editors)
	if err != nil {
		return fmt.Errorf("editors in %s: %w", manifestFile, err)
	}
	if len(selection.NotApplicable) > 0 {
		s.report.recordNotApplicable("editors", "Not selected by editors in "+manifestFile+": "+
			strings.Join(selection.NotApplicable, ", "))
	}
	if len(selection.Editors) == 0 {
		return nil
	}
	edOpts := editor.DefaultOptions()
	edOpts.WorkspaceRoot = s.repoPath
	edOpts.Editors = selection.Editors
	edOpts.Archetype = s.arch
	// The session already resolved the policy this repository is adopted under; the editor
	// projections state its ceilings rather than a literal of their own (issue #360).
	if s.policy != nil {
		edOpts.Complexity = s.policy.Policy.Complexity
	}
	set, err := s.synthesizeEditors(ctx, edOpts)
	if err != nil {
		return fmt.Errorf("synthesize editors: %w", err)
	}

	for i := 0; i < len(set.Files) && i < maxEditorFiles; i++ {
		if err := s.reconcileEditorFile(ctx, set.Files[i]); err != nil {
			return err
		}
	}
	return nil
}

// synthesizeEditors observes the workspace to decide which languages are present, so it runs
// under the caller's deadline rather than a background one, and under the entry bound this run's
// verification walk resolved rather than a fixed one of the editor's own: one
// --verification-max-entries value reaches both walks, and a scan that still stops at the bound
// names that flag (issue #535).
func (s *adoptSession) synthesizeEditors(ctx context.Context, opts editor.Options) (*editor.EditorConfigSet, error) {
	opts.MaxWorkspaceFiles = s.discoveryEntries()
	set, err := editor.SynthesizeContext(ctx, opts)
	if errors.Is(err, editor.ErrWorkspaceScanBound) {
		return nil, entriesBound.exceeded(opts.MaxWorkspaceFiles, err)
	}
	return set, err
}

// discoveryEntries is the entry bound this run's verification plan resolved, which the operator's
// --verification-max-entries raises; a session without a resolved plan keeps the default.
func (s *adoptSession) discoveryEntries() int {
	if s.verification == nil || s.verification.Limits == nil {
		return maxVerificationEntries
	}
	return s.verification.Limits.MaxEntries
}

func reconcileWorkingDirAndFlavor(ctx context.Context, s *adoptSession) error {
	if !s.opts.DryRun {
		if err := state.InitWorkingDirContext(ctx, s.repoPath); err != nil {
			s.report.addError("workingdir init: %v", err)
		}
	}
	s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{
		Path:    workingDirPath,
		Action:  actionCreate,
		Details: "Initialized canonical session state ledger and bug/question journals",
	})
	if !s.opts.DryRun {
		s.applyDetectedFlavor(ctx)
	}
	return nil
}

// applyDetectedFlavor scaffolds the flavor of the profile this adoption records, and records
// what it did.
//
// The flavor is resolved under s.arch, the profile adoption writes into .standards.yaml or reads
// from it, so adoption scaffolds exactly the flavor `flavor audit` measures afterwards. It used
// to detect across the whole flavor catalog and ignore that profile (BUG-940): a Go service whose
// package.json only held commit tooling was adopted as framework and scaffolded as
// typescript-node, and a repository declaring gitops-infra received python-ml templates for a
// PyTorch dependency. Before that it scaffolded go-library for a repository nothing matched and
// discarded the apply report, so neither the written templates nor a total write failure reached
// the adoption report.
func (s *adoptSession) applyDetectedFlavor(ctx context.Context) {
	name, err := flavor.ResolveForProfile(s.repoPath, s.arch)
	if err != nil {
		s.report.recordSkipped(flavorReportPath, flavorSkipDetail(s.arch, err))
		return
	}
	// Templates only: the branch-ruleset step renders the ruleset once every workflow of the
	// run exists, under the policy this run pins, and honours adoption.decline.
	applied, err := flavor.ApplyFlavorWith(ctx, s.repoPath, name, flavor.ApplyOptions{TemplatesOnly: true})
	s.recordFlavorReport(applied)
	if err != nil {
		s.report.addError("apply flavor %s: %v", name, err)
	}
}

// flavorSkipDetail says why adoption scaffolded no flavor for profile: the profile has no flavor
// at all, or none of its flavors matches the repository. Neither is replaced by a guess.
func flavorSkipDetail(profile string, err error) string {
	reason := "no registered flavor matches this repository"
	switch {
	case errors.Is(err, flavor.ErrFlavorNotApplicable):
		reason = fmt.Sprintf("profile %s has no flavor", profile)
	case profile != "":
		reason = fmt.Sprintf("no registered flavor of profile %s matches this repository", profile)
	}
	return "Not applicable: " + reason + ", so no flavor templates were scaffolded; " +
		"run `praetorctl flavor apply --flavor=<name>` to choose one"
}

// recordFlavorReport lists the templates a flavor apply created and the existing files it left
// alone. A skipped template is an action detail only: the file is either the operator's or one
// an earlier adoption step already listed. So is a covered one, a template the repository
// configures under another accepted name, such as a .yamllint.yaml where os-image scaffolds
// .yamllint.yml (flavor.ApplyReport.CoveredTemplates).
//
// A detected flavor can still hold back a template whose body cannot work in this repository
// (flavor.ApplyReport.UnmetTemplates), such as typescript-node's npm CI job in a pnpm project.
// Each is a warning: the ruleset derived next requires no check for it, and the operator learns
// what the flavor audit will report missing.
func (s *adoptSession) recordFlavorReport(applied *flavor.ApplyReport) {
	if applied == nil {
		return
	}
	for _, rel := range applied.CreatedTemplates {
		s.report.recordCreated(rel, fmt.Sprintf("Scaffolded %s flavor template", applied.Flavor))
	}
	// An unedited earlier text of a template is Praetor's own, refreshed without --force
	// (flavor.TemplateItem.Prior), so it is reconciled like any other earlier Praetor text.
	for _, rel := range applied.RefreshedTemplates {
		s.report.recordReconciled(rel, fmt.Sprintf("Refreshed an earlier Praetor text to the current %s flavor template", applied.Flavor))
	}
	for _, rel := range applied.SkippedTemplates {
		s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{
			Path:    rel,
			Action:  actionSkip,
			Details: fmt.Sprintf("Existing file kept; the %s flavor template was not written over it", applied.Flavor),
		})
	}
	// A covered template is recorded against the file in use: the canonical name was never
	// written, so reporting it would name a file the repository does not have.
	for _, covered := range applied.CoveredTemplates {
		s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{
			Path:   covered.InUse,
			Action: actionSkip,
			Details: fmt.Sprintf("Existing configuration kept; the %s flavor template %s was not written beside it",
				applied.Flavor, covered.Path),
		})
	}
	for _, unmet := range applied.UnmetTemplates {
		s.report.addWarning("flavor %s did not scaffold %s", applied.Flavor, unmet)
	}
}

// lowerFirst lower-cases the first byte of an ASCII detail string.
func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]+('a'-'A')) + s[1:]
}
