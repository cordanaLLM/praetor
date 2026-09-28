package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// GatingStatus represents the disposition of a gated check.
type GatingStatus string

const (
	StatusAdmitted GatingStatus = "ADMITTED"
	StatusRejected GatingStatus = "REJECTED"

	// ReceiptFileName is the Exit-0 receipt written at the repository root.
	ReceiptFileName = ".standards-receipt.json"
	// ReceiptCommand is the canonical command string recorded in every gate receipt.
	ReceiptCommand = "praetorctl gate run"
	// RepoRunCommand is the `gate run` invocation praetor writes into generated personas and
	// task bodies: ReceiptCommand aimed at the repository in the working directory. Every
	// generated copy derives from it, so a flag rename breaks one test instead of shipping an
	// undefined flag to every adopted repository.
	RepoRunCommand = ReceiptCommand + " --path=."
	// GosecConfigFile is the gosec configuration the security stage must use. It carries
	// an empty exclusion list: every finding is fixed or annotated per line.
	GosecConfigFile = ".gosec.json"
	// TestStageTimeout is the default bound on the race-detector test stage. The effective
	// value comes from testStageTimeout, which lets an operator raise it within a ceiling.
	TestStageTimeout = 180 * time.Second
	// MaxTestStageTimeout caps what the environment may ask for. HISS-02 requires an upper
	// bound on the stage, not that the bound be unreachable, so the override is clamped
	// rather than trusted.
	MaxTestStageTimeout = 30 * time.Minute
	// TestStageTimeoutEnv names the variable that overrides TestStageTimeout.
	TestStageTimeoutEnv = "PRAETOR_TEST_STAGE_TIMEOUT"
	// CleanupTimeout bounds worktree removal after the test stage.
	CleanupTimeout = 30 * time.Second
	// GitQueryTimeout bounds the short git queries used to describe the scanned tree.
	GitQueryTimeout = 15 * time.Second
	// ReceiptFilePerm is the mode of the written receipt: world-readable, owner-writable,
	// because the receipt is a tracked artifact reviewers read.
	ReceiptFilePerm os.FileMode = 0o644
	// maxStages bounds the stage loop (HISS-02).
	maxStages = 16
)

// The stage names, in pipeline order. The prefetch, security and test stages are the toolchain
// stages: they run a language's own commands, and the receipt stage signs nothing unless one of
// them ran for some language (requireVerification).
const (
	stagePrefetch = "Prefetch & Lockfiles"
	stageHISS     = "HISS Invariant Scan"
	stageSecurity = "Security & SCA Scan"
	stageFlavor   = "Flavor Conformance"
	stageTests    = "Race-Detector Tests"
	stageReceipt  = "Ed25519 Exit-0 Receipt"
)

// ErrMissingScanner reports that a required security scanner is absent, which must fail
// the security stage instead of silently passing it.
var ErrMissingScanner = errors.New("required security scanner is not installed")

// StageStatus is the verdict one pipeline stage reached.
//
// A single passed/failed bool recorded a stage that never ran as passed, so the signed gate
// output certified security scans and prefetches that had not executed. Skipped and not
// applicable are therefore verdicts of their own, never folded into passed.
type StageStatus string

const (
	// StagePassed means the stage ran its checks and they held.
	StagePassed StageStatus = "passed"
	// StageFailed means the stage ran and rejected the repository, or could not run at all.
	StageFailed StageStatus = "failed"
	// StageSkipped means the stage applies to this repository but did not run all of its
	// checks here: a dry run, a host that cannot build the race detector, or a toolchain stage
	// that ran for one of the repository's languages and not for another (its reason names
	// each language's outcome).
	StageSkipped StageStatus = "skipped"
	// StageNotApplicable means the repository has nothing this stage checks: neither a go.mod
	// nor a Cargo.lock for the toolchain stages, or a declared profile no flavor implements.
	StageNotApplicable StageStatus = "not_applicable"
)

// StageResult captures the outcome of a single gating pipeline stage.
type StageResult struct {
	Name     string        `json:"name"`
	Status   StageStatus   `json:"status"`
	Duration time.Duration `json:"duration"`
	Message  string        `json:"message,omitempty"`
}

// Failed reports whether the stage rejected the repository.
func (s StageResult) Failed() bool {
	return s.Status == StageFailed
}

// stageSkip is how a stage reports that it did not run its checks. It travels as the
// stage's error so an early return reads like any other, and executeStage turns it into a
// skipped or not-applicable verdict instead of a failure.
type stageSkip struct {
	status StageStatus
	reason string
}

func (s *stageSkip) Error() string { return s.reason }

// skipped reports a stage that applies here but deliberately ran nothing.
func skipped(reason string) error {
	return &stageSkip{status: StageSkipped, reason: reason}
}

// notApplicable reports a stage the repository gives nothing to check.
func notApplicable(reason string) error {
	return &stageSkip{status: StageNotApplicable, reason: reason}
}

// PipelineReport aggregates the entire gated pre-merge verification. WorktreeProblem says why
// WorktreeClean is false: the changed paths, an ignored subtractive input, or a tree whose
// state could not be read.
type PipelineReport struct {
	Status           GatingStatus  `json:"status"`
	RepoDir          string        `json:"repo_dir"`
	Repository       string        `json:"repository,omitempty"`
	CommitSHA        string        `json:"commit_sha,omitempty"`
	WorktreeClean    bool          `json:"worktree_clean"`
	WorktreeProblem  string        `json:"worktree_problem,omitempty"`
	DryRun           bool          `json:"dry_run"`
	ReceiptSignature string        `json:"receipt_signature,omitempty"`
	ReceiptPath      string        `json:"receipt_path,omitempty"`
	Stages           []StageResult `json:"stages"`
	TotalElapsed     time.Duration `json:"total_elapsed"`
	// Complexity is the HISS stage's complexity report: measured and printed, never part of
	// a stage verdict or of the signed stage output. Nil when the HISS stage did not scan.
	Complexity *hiss.ComplexityReport `json:"complexity,omitempty"`
}

// StageOutput renders the canonical, deterministic byte stream whose SHA-256 the Exit-0
// receipt signs. It binds the receipt to the concrete stage results, the scanned commit
// and the cleanliness of the tree that was actually scanned.
func (r *PipelineReport) StageOutput() []byte {
	lines := make([]string, 0, len(r.Stages)+5)
	lines = append(lines,
		lockdown.GateOutputVersion,
		fmt.Sprintf("repository\t%s", r.Repository),
		fmt.Sprintf("commit_sha\t%s", r.CommitSHA),
		lockdown.WorktreeCleanLine(r.WorktreeClean),
		fmt.Sprintf("dry_run\t%t", r.DryRun),
	)
	for i := 0; i < len(r.Stages) && i < maxStages; i++ {
		s := r.Stages[i]
		lines = append(lines, fmt.Sprintf("stage\t%s\t%s\t%s",
			s.Name, s.Status, strings.ReplaceAll(s.Message, "\n", " ")))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// commandRunner executes an external command and returns its standard output; a failure's
// standard error travels in the error. util.RunCommand is the production implementation,
// which also keeps an ambient GIT_DIR or GIT_INDEX_FILE -- set whenever the gate runs from a
// git hook -- away from the go-test stage and the git fixtures its tests create. Tests
// substitute a fake to exercise stage logic hermetically.
type commandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// stageConfig carries the inputs and the injectable seams shared by all stages.
type stageConfig struct {
	repoDir  string
	dryRun   bool
	run      commandRunner
	lookPath func(string) (string, error)
	rep      *PipelineReport
	// scanOpts overrides the HISS scan bounds. Production leaves it zero so the package
	// defaults apply and the gate sees exactly the scope `praetorctl audit` sees; tests set
	// it to reach the truncation path without synthesizing a repository of that size. Its
	// MaxFuncLOC and complexity limits are always replaced by the repository's resolved
	// policy (hissScanOptions).
	scanOpts hiss.ScanOptions
	// boundStage derives the race stage's context under its resolved bound. Production uses
	// withStageBound. Tests start the bound's clock only once their fake suite starts, so how
	// long the real `git worktree add` before it takes cannot decide where it fires (BUG-988).
	boundStage func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	// inspectTree reads the HEAD commit and whether the working tree matches it. Production
	// uses inspectTree; tests substitute an answer so a stage fixture need not be a repository.
	inspectTree func(context.Context, string) treeState
	// verified lists the languages for which at least one toolchain stage ran its checks and
	// passed, in the order they first did. The receipt stage refuses to sign while it is empty.
	verified []string
}

// newStageConfig builds a stage configuration backed by the real toolchain.
func newStageConfig(repoDir string, dryRun bool, rep *PipelineReport) *stageConfig {
	return &stageConfig{
		repoDir:     repoDir,
		dryRun:      dryRun,
		run:         util.RunCommand,
		lookPath:    exec.LookPath,
		rep:         rep,
		boundStage:  withStageBound,
		inspectTree: inspectTree,
	}
}

// RunGatedPipeline executes the six-stage anti-direct-merge gating pipeline: prefetch and
// lockfiles, HISS invariants against the debt baseline, security and SCA scanning, flavor
// conformance, race-detector tests in an isolated worktree, and the Ed25519 Exit-0
// receipt. The prefetch, security and test stages run the Go toolchain where a go.mod is
// present and the Cargo toolchain where a Cargo.lock is (cargo.go); the receipt is refused
// when neither ran any of them (requireVerification).
//
// A dry run changes nothing and reaches no network: it runs only the read-only checks --
// lockfiles, the HISS scan and flavor conformance -- and records the module prefetch, the
// security scanners, the race tests and the receipt as skipped. `go mod download` writes the
// module cache, and govulncheck and `go list` can fetch modules and query the vulnerability
// database, so a dry run that ran them was not one; the Cargo commands are skipped alike.
//
// A run that can mint a receipt first requires the working tree to match HEAD, because the
// scan stages read the working tree while the receipt certifies the commit. A tree with
// changes, or an untracked or ignored debt baseline or gosec configuration, is refused before
// any stage runs (requireCleanTree); a dry run reports the same state and carries on.
func RunGatedPipeline(ctx context.Context, repoDir string, dryRun bool) (*PipelineReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("pipeline: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pipeline cancelled: %w", err)
	}

	start := time.Now()
	rep := &PipelineReport{
		Status:  StatusAdmitted,
		RepoDir: repoDir,
		DryRun:  dryRun,
		Stages:  make([]StageResult, 0, maxStages),
	}
	cfg := newStageConfig(repoDir, dryRun, rep)
	describeTree(ctx, cfg)
	if err := requireCleanTree(cfg); err != nil {
		rep.Status = StatusRejected
		rep.TotalElapsed = time.Since(start)
		return rep, nil //nolint:nilerr // the refusal is reported in rep.Status and its stage
	}

	if err := executeStages(ctx, cfg); err != nil {
		rep.Status = StatusRejected
		rep.TotalElapsed = time.Since(start)
		// A rejection is a pipeline outcome, not a pipeline failure: the failing stage and
		// its message are carried in the report, and the CLI turns StatusRejected into a
		// non-zero exit code.
		return rep, nil //nolint:nilerr // the rejection is reported in rep.Status
	}

	rep.TotalElapsed = time.Since(start)
	return rep, nil
}

// describeTree records the repository identity, the HEAD commit and whether the working
// tree that the scan stages inspect matches it, with the reason when it does not.
func describeTree(ctx context.Context, cfg *stageConfig) {
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()

	cfg.rep.Repository = resolveRepositoryName(gitCtx, cfg.repoDir)
	tree := cfg.inspectTree(ctx, cfg.repoDir)
	cfg.rep.CommitSHA = tree.commit
	cfg.rep.WorktreeClean = tree.problem == ""
	cfg.rep.WorktreeProblem = tree.problem
}

// resolveRepositoryName returns owner/name for the gated repository, falling back to the
// absolute directory's base name. It never records a bare "." or "..".
func resolveRepositoryName(ctx context.Context, repoDir string) string {
	if owner, name, err := util.ResolveRepoIdentity(ctx, repoDir); err == nil {
		return owner + "/" + name
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return filepath.Base(repoDir)
	}
	return filepath.Base(abs)
}

// stage pairs a stage name with its implementation. A stage may return a message that is
// recorded even when it passes; a stage that runs nothing returns skipped or notApplicable,
// so the verdict itself says the work did not happen.
type stage struct {
	name string
	fn   func(context.Context, *stageConfig) (string, error)
}

func executeStages(ctx context.Context, cfg *stageConfig) error {
	stages := []stage{
		{stagePrefetch, runPrefetchStage},
		{stageHISS, runHissStage},
		{stageSecurity, runSecurityStage},
		{stageFlavor, runFlavorStage},
		{stageTests, runTestStage},
		{stageReceipt, runReceiptStage},
	}

	for i := 0; i < len(stages) && i < maxStages; i++ {
		if err := executeStage(ctx, stages[i], cfg); err != nil {
			return err
		}
	}
	return nil
}

// executeStage runs one stage and records its verdict. A stage that reports a skip is
// recorded as skipped or not applicable and lets the pipeline continue; only a real error
// fails the stage and stops the pipeline.
func executeStage(ctx context.Context, s stage, cfg *stageConfig) error {
	sStart := time.Now()
	msg, err := s.fn(ctx, cfg)
	res := StageResult{Name: s.name, Status: StagePassed, Message: msg}
	if skip, ok := errors.AsType[*stageSkip](err); ok {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			cutErr := attributeRunCut(ctx, s.name, err)
			res.Status, res.Message, err = StageFailed, cutErr.Error(), cutErr
		} else {
			res.Status, res.Message, err = skip.status, skip.reason, nil
		}
	} else if err = attributeRunCut(ctx, s.name, err); err != nil {
		res.Status, res.Message = StageFailed, err.Error()
	}
	res.Duration = time.Since(sStart)
	cfg.rep.Stages = append(cfg.rep.Stages, res)
	return err
}

// runPrefetchStage verifies the standards lockfiles, then prefetches each language's
// dependencies: the Go modules, and the Cargo crates where a Cargo.lock pins them (withCargo).
func runPrefetchStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if err := VerifyLockfiles(cfg.repoDir); err != nil {
		return "", err
	}
	msg, err := runGoPrefetch(ctx, cfg)
	return withCargo(ctx, cfg, languagePart{language: languageGo, msg: msg, err: err}, runCargoPrefetch)
}

// runGoPrefetch is the prefetch stage's Go part: go mod verify and go mod download.
func runGoPrefetch(ctx context.Context, cfg *stageConfig) (string, error) {
	// Without a go.mod the prefetch below runs nothing and reports not applicable, which is
	// the truer verdict for a dry run of a non-Go repository too.
	if cfg.dryRun && util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return "", skipped("dry run: lockfiles verified; go mod verify and go mod download not run")
	}
	rep, err := prefetchDependencies(ctx, cfg.repoDir, cfg.run)
	if err != nil {
		return "", err
	}
	if rep.Skipped {
		return "", notApplicable("lockfiles verified; no go.mod: module prefetch skipped")
	}
	return "", nil
}

// runHissStage evaluates the HISS scan against the recorded debt baseline, exactly like
// `praetorctl audit`, so an adopted brownfield repository is gated on new violations
// rather than on its total legacy debt.
func runHissStage(ctx context.Context, cfg *stageConfig) (string, error) {
	// The scan bounds are left to the package defaults. A lower cap here than the one
	// `praetorctl audit` uses made the gate truncate on a report audit completes, so a
	// repository could pass audit and fail the gate for a reason unrelated to its
	// compliance (BUG-829).
	scanOpts, policyWarning, err := hissScanOptions(ctx, cfg)
	if err != nil {
		return "", err
	}
	scanRep, err := hiss.Scan(ctx, cfg.repoDir, scanOpts)
	if err != nil {
		return "", fmt.Errorf("hiss scan error: %w", err)
	}
	// Incomplete covers truncation and files that yielded no analyzable structure. Checking
	// only truncation let an unparseable file through as a clean gate.
	if scanRep.Incomplete() {
		return "", fmt.Errorf("hiss scan error: %w: %s", hiss.ErrScanIncomplete, scanRep.CoverageEvidence())
	}
	if cfg.rep != nil {
		cfg.rep.Complexity = &scanRep.Complexity
	}

	base, err := baseline.LoadBaseline(filepath.Join(cfg.repoDir, BaselineFile))
	if err != nil {
		return "", fmt.Errorf("load debt baseline: %w", err)
	}

	current := hiss.ConvertToBaseline(scanRep.Violations)
	for i := range current {
		current[i].Fingerprint = fmt.Sprintf("%s:%d:%s", current[i].FilePath, current[i].LineNumber, current[i].RuleID)
	}

	ratchet := baseline.EvaluateRatchet(base, current, nil)
	if !ratchet.Passed {
		// The same rendering `praetorctl audit` prints, so the rejection names each new
		// violation's file, line and rule rather than only how many there are (BUG-792).
		return "", fmt.Errorf("hiss ratchet failed against a baseline of %d: %s",
			base.TotalInfractions, ratchet.Summary())
	}
	msg := fmt.Sprintf("%d infractions within the %d baselined limit (function length limit %d)",
		ratchet.CurrentCount, base.TotalInfractions, scanOpts.MaxFuncLOC)
	if policyWarning != "" {
		msg += "; " + policyWarning
	}
	return msg, nil
}

// hissScanOptions returns the scan options with the function-length and complexity limits
// the repository's policy imposes, resolved by config.ResolveRepositoryComplexity: a locked
// repository gets exactly the limits `praetorctl audit` scans with, a manifest without a lock
// gets the HISS-04 ceiling tightened by its overrides, and an unadopted tree gets the ceiling.
// Scanning with the package default instead let a repository whose manifest sets a stricter
// limit pass the gate with functions its audit rejects (BUG-638). A policy that does not
// resolve still scans with the ceiling, and the returned warning names the cause so the stage
// message shows it.
//
// The HISS exceptions the manifest declares and documents ride along
// (config.ResolveRepositoryScanOptions), so the gate accepts exactly the cleanup gotos the
// audit accepts.
func hissScanOptions(ctx context.Context, cfg *stageConfig) (hiss.ScanOptions, string, error) {
	opts, warning, err := config.ResolveRepositoryScanOptions(ctx, cfg.repoDir, cfg.scanOpts)
	if err != nil {
		return opts, "", fmt.Errorf("resolve repository complexity policy: %w", err)
	}
	return opts, warning, nil
}

// runSecurityStage runs each language's scanners: govulncheck and gosec for Go, and cargo audit
// where a Cargo.lock is present (withCargo).
func runSecurityStage(ctx context.Context, cfg *stageConfig) (string, error) {
	msg, err := runGoSecurity(ctx, cfg)
	return withCargo(ctx, cfg, languagePart{language: languageGo, msg: msg, err: err}, runCargoSecurity)
}

// runGoSecurity runs govulncheck and gosec. A missing scanner fails the stage: a security
// gate that certifies a run in which nothing executed is worse than no gate.
func runGoSecurity(ctx context.Context, cfg *stageConfig) (string, error) {
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return "", notApplicable("no go.mod: Go security scanners skipped")
	}
	if cfg.dryRun {
		return "", skipped("dry run: govulncheck and gosec not run")
	}

	if err := requireScanner(cfg, "govulncheck", "go install golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
		return "", err
	}
	if out, err := cfg.run(ctx, cfg.repoDir, "govulncheck", "./..."); err != nil {
		// The findings are on standard output; why govulncheck could not scan at all is on
		// standard error, which the runner carries in err.
		return "", fmt.Errorf("govulncheck found vulnerabilities: %w: %s", err, out)
	}

	if err := requireScanner(cfg, "gosec", "go install github.com/securego/gosec/v2/cmd/gosec@latest"); err != nil {
		return "", err
	}
	confPath := filepath.Join(cfg.repoDir, GosecConfigFile)
	if !util.FileExists(confPath) {
		return "", fmt.Errorf("%s is missing: the security gate refuses to run gosec without its pinned configuration", GosecConfigFile)
	}
	listing, err := cfg.run(ctx, cfg.repoDir, "go", "list", "-f", "{{.Dir}}", "./...")
	if err != nil {
		return "", fmt.Errorf("list Go packages for gosec: %w: %s", err, listing)
	}
	packages, err := resolveSecurityPackages(cfg.repoDir, listing)
	if err != nil {
		return "", err
	}
	if out, err := cfg.run(ctx, cfg.repoDir, "gosec", append([]string{"-conf", GosecConfigFile}, packages...)...); err != nil {
		return "", fmt.Errorf("gosec found security infractions: %w: %s", err, out)
	}
	return "", nil
}

// requireScanner fails closed when a mandatory scanner is not on PATH.
func requireScanner(cfg *stageConfig, binary, installHint string) error {
	if _, err := cfg.lookPath(binary); err != nil {
		return fmt.Errorf("%w: %s not found on PATH (%s): %w", ErrMissingScanner, binary, installHint, err)
	}
	return nil
}

// runFlavorStage audits flavor conformance under the run's context, bounded by
// flavor.DefaultAuditTimeout. The audit checks the context before every file read and
// toolchain lookup, so a run deadline that fires mid-audit stops it (BUG-464).
func runFlavorStage(ctx context.Context, cfg *stageConfig) (string, error) {
	fCtx, cancel := context.WithTimeout(ctx, flavor.DefaultAuditTimeout)
	defer cancel()
	rep, err := flavor.AuditFlavorContext(fCtx, cfg.repoDir, "auto")
	if errors.Is(err, flavor.ErrFlavorNotApplicable) {
		// Not a pass and not a failure: this repository's declared profile has no flavor, so
		// there is nothing for this stage to check. Reporting it is the point -- a skipped
		// stage that reads as a pass is how a gate comes to certify what it never examined.
		return "", notApplicable(err.Error())
	}
	if err != nil {
		return "", fmt.Errorf("flavor audit failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("flavor audit cancelled: %w", err)
	}
	if !rep.Passed {
		return "", fmt.Errorf("flavor audit failed (score: %.1f%%, %d missing templates%s)",
			rep.Score, len(rep.MissingTemplates), invalidSettingsClause(rep))
	}
	return "", nil
}

// invalidSettingsClause names the settings that cost the score, or nothing when none did.
//
// Settings are validated, not merely counted, so a repository can fail this stage with no
// missing template at all -- two present but unparsable settings put a go-library
// repository at 7/9 = 77.8%. Reporting only the score and the template count then tells an
// operator that something is wrong and nothing about which file, in the one place where the
// gate has already blocked the push.
func invalidSettingsClause(rep *flavor.FlavorAuditReport) string {
	paths := rep.InvalidSettingPaths()
	if len(paths) == 0 {
		return ""
	}
	return ", missing or invalid settings: " + strings.Join(paths, ", ")
}

// runTestStage runs each language's test suite against HEAD in an isolated worktree: the Go
// race detector, and cargo test and cargo clippy where a Cargo.lock is present (withCargo).
func runTestStage(ctx context.Context, cfg *stageConfig) (string, error) {
	msg, err := runGoTests(ctx, cfg)
	return withCargo(ctx, cfg, languagePart{language: languageGo, msg: msg, err: err}, runCargoTests)
}

// runGoTests runs the race detector against HEAD in an isolated worktree. A worktree that
// cannot be created fails the stage rather than silently testing the dirty tree, and cleanup
// failures are surfaced instead of dropped (inStageWorktree).
func runGoTests(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: race-detector tests skipped")
	}
	// `go test -race ./...` cannot run where there is no module, exactly as the prefetch
	// and security stages already recognise. Without this the stage failed every adopted
	// non-Go repository with "directory prefix . does not contain main module", which
	// reads as a broken repository rather than an inapplicable stage. Those repositories
	// are gated on their own suites by the verification contract, not here.
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return "", notApplicable("no go.mod: Go race-detector tests skipped")
	}
	// The race detector needs cgo and a host C toolchain. Without this check the stage
	// does not report "no C compiler", it reports `# runtime/cgo` followed by every
	// package failing to build -- which reads as a repository whose whole tree is
	// broken. On a Windows box without gcc that is every push, including a push
	// fixing Windows support, so the gate could not be repaired from the platform it
	// was broken on. `.config/lefthook/scripts/test_hooks.py` already reasons exactly
	// this way for the harness self-tests; this is the same rule for the Go gate.
	if available, absent := raceDetectorAvailable(ctx, cfg); !available {
		return "", skipped(fmt.Sprintf(
			"race detector unavailable (%s): race-detector tests skipped; "+
				"CI runs this leg on Linux with cgo", absent))
	}

	budget := EnvRunBudget()
	bound := budget.StageBound
	err := inStageWorktree(ctx, cfg, bound, func(tCtx context.Context, dir string) error {
		out, testErr := cfg.run(tCtx, dir, "go", "test", "-race", "./...")
		if testErr == nil {
			return nil
		}
		// A stage killed by a deadline is not a failing suite, and printing it as one sends
		// every reader to diagnose a change that was never the cause. The context is the
		// authoritative witness: the child dies of a signal and reports nothing useful. Its
		// cause also says which deadline fired, the stage's own bound or the whole run's.
		// util.RunCommand returns standard output only; build errors and anything else the
		// go command printed on standard error travel in testErr.
		if cutErr := cutError(tCtx, "race-detector tests", bound, dir,
			fmt.Sprintf("%s [%v]", out, testErr)); cutErr != nil {
			return cutErr
		}
		return fmt.Errorf("tests failed in %s: %s (%w)", dir, out, testErr)
	})
	if err != nil {
		return "", err
	}
	return budget.Note, nil
}

// inStageWorktree runs suite in an isolated worktree of HEAD, under the stage bound, and removes
// the worktree afterwards. The worktree is removed under the caller's context rather than the
// expired stage context (removeWorktree), and a removal that fails is joined to suite's error
// instead of dropped: an un-removed worktree would leak into the next gate. Every test suite the
// test stage runs goes through it, so each language's suite is isolated the same way.
func inStageWorktree(ctx context.Context, cfg *stageConfig, bound time.Duration,
	suite func(tCtx context.Context, dir string) error) (err error) {
	tCtx, cancel := cfg.boundStage(ctx, bound)
	defer cancel()

	wtMgr := worktree.NewManager(cfg.repoDir)
	if wtMgr == nil {
		return fmt.Errorf("cannot create a worktree manager for %s", cfg.repoDir)
	}
	taskID := fmt.Sprintf("gate-%d-%d", os.Getpid(), time.Now().UnixNano())
	wt, createErr := createStageWorktree(tCtx, wtMgr, taskID, bound, cfg.repoDir)
	if createErr != nil {
		return createErr
	}
	defer func() {
		if cleanErr := removeWorktree(ctx, wtMgr, taskID); cleanErr != nil {
			err = errors.Join(err, cleanErr)
		}
	}()
	return suite(tCtx, wt.Path)
}

// createStageWorktree creates the isolated test worktree under the stage context.
//
// The worktree is created under the stage bound too, so the bound can fire here first. On
// Windows it did at 50ms: git worktree add outlasted the bound, the kill surfaced as a bare
// "exit status 1", and the stage reported a repository whose worktree could not be created --
// the misattribution #100 describes, one step earlier. The run deadline can fire here as well,
// and is reported as itself rather than as the stage bound (#314).
func createStageWorktree(tCtx context.Context, wtMgr *worktree.Manager, taskID string, bound time.Duration, repoDir string) (*worktree.Worktree, error) {
	wt, err := wtMgr.Create(tCtx, taskID, "HEAD")
	if err == nil {
		return wt, nil
	}
	if cutErr := cutError(tCtx, "creating the isolated test worktree", bound, repoDir, err.Error()); cutErr != nil {
		return nil, cutErr
	}
	return nil, fmt.Errorf("isolated test worktree could not be created in %s: %w", repoDir, err)
}

// stageBoundError reports the test stage cut off by its own deadline while doing what, in dir.
// cutError calls it only once the stage's own deadline is the one that fired.
func stageBoundError(what string, bound time.Duration, dir, output string) error {
	return fmt.Errorf(
		"%s hit the %s stage bound in %s before finishing; "+
			"this is the bound firing, not a test failure. Raise it with %s "+
			"(maximum %s). Output up to the cut: %s",
		what, bound, dir, TestStageTimeoutEnv, MaxTestStageTimeout, output)
}

// raceDetectorAvailable reports whether `go test -race` can build here, and names what
// is missing when it cannot.
//
// Checking CGO_ENABLED alone is not enough, and the difference is the common case: a
// stock Windows Go install reports CGO_ENABLED=1 and CC=gcc while no gcc exists on
// PATH, so the toolchain claims cgo and every race build still fails. The compiler the
// toolchain actually names is therefore resolved, not assumed.
//
// A skip is reported with its reason, never taken silently. A skipped race leg that
// read as a pass would be an unexamined thing certified as clean, which is the defect
// this lattice exists to prevent.
func raceDetectorAvailable(ctx context.Context, cfg *stageConfig) (bool, string) {
	if os.Getenv("CGO_ENABLED") == "0" {
		return false, "CGO_ENABLED=0 in the environment"
	}
	enabled, err := cfg.run(ctx, cfg.repoDir, "go", "env", "CGO_ENABLED")
	if err != nil {
		return false, fmt.Sprintf("go env CGO_ENABLED could not be read: %v", err)
	}
	if strings.TrimSpace(enabled) == "0" {
		return false, "go env reports CGO_ENABLED=0"
	}
	compiler, err := cfg.run(ctx, cfg.repoDir, "go", "env", "CC")
	if err != nil {
		return false, fmt.Sprintf("go env CC could not be read: %v", err)
	}
	compiler = strings.TrimSpace(compiler)
	if compiler == "" {
		return false, "go env names no C compiler"
	}
	if _, err := cfg.lookPath(compiler); err != nil {
		return false, fmt.Sprintf("the C compiler %q named by go env is not on PATH", compiler)
	}
	return true, ""
}

// testStageTimeout resolves the race stage's bound from the environment.
//
// An unusable value is ignored rather than honoured: an empty, unparseable, zero, negative or
// over-ceiling setting falls back to the default and says why, so a typo cannot quietly remove
// the bound or shrink it to nothing. The returned note is surfaced as the stage's reason, so a
// raised bound is visible in the receipt rather than being an invisible local difference.
func testStageTimeout(raw string) (time.Duration, string) {
	if strings.TrimSpace(raw) == "" {
		return TestStageTimeout, ""
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return TestStageTimeout, fmt.Sprintf("%s=%q is not a duration; using the %s default",
			TestStageTimeoutEnv, raw, TestStageTimeout)
	}
	if parsed <= 0 {
		return TestStageTimeout, fmt.Sprintf("%s=%s is not positive; using the %s default",
			TestStageTimeoutEnv, parsed, TestStageTimeout)
	}
	if parsed > MaxTestStageTimeout {
		return MaxTestStageTimeout, fmt.Sprintf("%s=%s exceeds the %s ceiling; clamped",
			TestStageTimeoutEnv, parsed, MaxTestStageTimeout)
	}
	return parsed, fmt.Sprintf("race stage bound raised to %s by %s", parsed, TestStageTimeoutEnv)
}

// removeWorktree tears down the isolated test worktree. It keeps the caller's context
// values but drops its cancellation, because the stage context is typically already
// expired when cleanup runs, and an un-removed worktree would leak into the next gate.
func removeWorktree(ctx context.Context, wtMgr *worktree.Manager, taskID string) error {
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)
	defer cancel()
	if err := wtMgr.Remove(cleanCtx, taskID, true); err != nil {
		return fmt.Errorf("leaked test worktree %s: %w", taskID, err)
	}
	return nil
}

// runReceiptStage signs the real concatenated stage output with the long-lived Ed25519
// key resolved by lockdown.LoadSigningKey. It fails closed when no key is configured, and
// it never mints a receipt for a dry run, which by definition did not run the tests. Nor does
// it mint one unless the tree still matches the HEAD the run started on (confirmTreeUnchanged).
func runReceiptStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context cancelled before receipt could be minted: %w", err)
	}
	rep := cfg.rep
	if cfg.dryRun {
		return "", skipped("dry run: no Exit-0 receipt minted")
	}
	if err := confirmTreeUnchanged(ctx, cfg); err != nil {
		return "", err
	}

	priv, err := lockdown.LoadSigningKey()
	if err != nil {
		return "", fmt.Errorf("Exit-0 receipt cannot be signed: %w", err)
	}

	output := rep.StageOutput()
	receipt, err := lockdown.CreateReceipt(ReceiptCommand, 0, output, rep.CommitSHA, rep.Repository, priv)
	if err != nil {
		return "", fmt.Errorf("create exit-0 receipt: %w", err)
	}

	receiptPath := filepath.Join(cfg.repoDir, ReceiptFileName)
	if err := lockdown.SaveReceiptFile(receiptPath, &lockdown.ReceiptFile{
		ExecutionReceipt: *receipt,
		GateOutput:       string(output),
	}, ReceiptFilePerm); err != nil {
		return "", fmt.Errorf("write receipt: %w", err)
	}

	rep.ReceiptSignature = receipt.Signature
	rep.ReceiptPath = receiptPath
	return fmt.Sprintf("signed %s for %s@%s", ReceiptFileName, rep.Repository, shortSHA(rep.CommitSHA)), nil
}

// shortSHA abbreviates a commit sha for human-readable stage messages.
func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func getGitCommitSHA(ctx context.Context, repoDir string) string {
	out, err := util.RunGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "uncommitted"
	}
	return strings.TrimSpace(out)
}

// The languages whose own toolchain the prefetch, security and test stages run. A language
// counts as present when its marker is at the repository root: go.mod for Go, Cargo.lock for
// Cargo, which is also what its commands hold the build to (--locked).
const (
	languageGo    = "go"
	languageCargo = "cargo"
	// supportedLanguages names them with their markers for the refusal message.
	supportedLanguages = "Go (go.mod) and Cargo (Cargo.lock)"
)

// ErrNothingVerified reports a run in which no toolchain stage ran its checks for any language.
// The receipt stage refuses to sign such a run: a receipt certifying prefetch, security scans
// and tests that were each not applicable or skipped attests to nothing but the HISS scan.
var ErrNothingVerified = errors.New("no verification stage ran for any language")

// unsupportedMarkers are the root files that identify a language the gate runs no toolchain
// for. They are named when a receipt is refused, so the refusal says what the repository holds
// rather than only what the gate lacks. Cargo.toml counts here only without a Cargo.lock.
var unsupportedMarkers = [...]struct{ file, language string }{
	{"Cargo.toml", "cargo without a committed Cargo.lock"},
	{"package.json", "node"},
	{"pyproject.toml", "python"},
	{"requirements.txt", "python"},
	{"setup.py", "python"},
	{"pom.xml", "java"},
	{"build.gradle", "java"},
	{"build.gradle.kts", "kotlin"},
	{"Gemfile", "ruby"},
	{"composer.json", "php"},
	{"mix.exs", "elixir"},
	{"Package.swift", "swift"},
	{"pubspec.yaml", "dart"},
	{"meson.build", "meson"},
	{"CMakeLists.txt", "cmake"},
}

// languagePart is one language's share of a toolchain stage, carried the way a stage carries
// its outcome: a nil err passed, a *stageSkip was skipped or not applicable, and any other
// error failed.
type languagePart struct {
	language string
	msg      string
	err      error
}

// isFailure reports whether err fails a stage rather than recording a skip.
func isFailure(err error) bool {
	_, skip := errors.AsType[*stageSkip](err)
	return err != nil && !skip
}

// partStatus is the verdict a part's err records.
func partStatus(err error) StageStatus {
	if err == nil {
		return StagePassed
	}
	if skip, ok := errors.AsType[*stageSkip](err); ok {
		return skip.status
	}
	return StageFailed
}

// noteVerified records that part's language ran a toolchain stage and passed it.
func (c *stageConfig) noteVerified(part languagePart) {
	if part.err == nil && !slices.Contains(c.verified, part.language) {
		c.verified = append(c.verified, part.language)
	}
}

// withCargo completes a toolchain stage whose Go part has already run, by running its Cargo
// part where a Cargo.lock is present.
//
// Without a Cargo.lock it returns the Go part exactly as the stage returned it before Cargo
// support, message and verdict alike, so a Go repository's stage lines and signed output are
// unchanged. A Go part that failed stops the stage before Cargo runs, as any failure stops the
// pipeline. Otherwise each present language contributes a "language: outcome" clause to the
// stage's reason (combineParts), which is how the signed stage output records what ran for which
// language.
func withCargo(ctx context.Context, cfg *stageConfig, goPart languagePart,
	cargo func(context.Context, *stageConfig) (string, error)) (string, error) {
	cfg.noteVerified(goPart)
	if !util.FileExists(filepath.Join(cfg.repoDir, CargoLockFile)) || isFailure(goPart.err) {
		return goPart.msg, goPart.err
	}
	msg, err := cargo(ctx, cfg)
	if isFailure(err) {
		return "", fmt.Errorf("%s: %w", languageCargo, err)
	}
	cargoPart := languagePart{language: languageCargo, msg: msg, err: err}
	cfg.noteVerified(cargoPart)
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return combineParts(cargoPart)
	}
	return combineParts(goPart, cargoPart)
}

// combineParts folds the parts of the languages a repository holds into one stage verdict, none
// of them failed. The stage passed only when every part passed. When some ran and some did not,
// it is recorded as skipped, never as passed: a Cargo audit that did not run must not read as a
// pass because the Go scanners beside it did. The reason names each language's outcome.
func combineParts(parts ...languagePart) (string, error) {
	clauses := make([]string, 0, len(parts))
	passed, skippedAny := 0, false
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		outcome := part.msg
		switch partStatus(part.err) {
		case StagePassed:
			passed++
			if outcome == "" {
				outcome = "passed"
			}
		case StageSkipped:
			skippedAny = true
			outcome = part.err.Error()
		default:
			outcome = part.err.Error()
		}
		clauses = append(clauses, part.language+": "+outcome)
	}
	reason := strings.Join(clauses, "; ")
	switch {
	case passed == len(parts):
		return reason, nil
	case passed > 0 || skippedAny:
		return "", skipped(reason)
	default:
		return "", notApplicable(reason)
	}
}

// requireVerification refuses a receipt for a run in which no toolchain stage ran for any
// language: the prefetch, security and test stages were each not applicable or skipped.
func requireVerification(cfg *stageConfig) error {
	if len(cfg.verified) > 0 {
		return nil
	}
	return nothingVerifiedError(cfg.repoDir)
}

// nothingVerifiedError names why nothing ran: a Cargo.lock whose stages could not run here, the
// languages at the repository root the gate runs no toolchain for, or that it recognises none.
func nothingVerifiedError(repoDir string) error {
	notes := make([]string, 0, 2)
	if util.FileExists(filepath.Join(repoDir, CargoLockFile)) {
		notes = append(notes, "Cargo.lock is present, but no Cargo stage ran here; each stage's reason says why")
	}
	if unsupported := unsupportedLanguages(repoDir); len(unsupported) > 0 {
		notes = append(notes, "unsupported languages at the repository root: "+strings.Join(unsupported, ", "))
	}
	if len(notes) == 0 {
		notes = append(notes, "no language marker the gate recognises is at the repository root")
	}
	return fmt.Errorf("%w: %s, %s and %s each ran nothing, so no Exit-0 receipt is signed; "+
		"the gate runs the toolchains of %s; %s",
		ErrNothingVerified, stagePrefetch, stageSecurity, stageTests, supportedLanguages, strings.Join(notes, "; "))
}

// unsupportedLanguages lists the languages unsupportedMarkers finds at the repository root, each
// once and with the files that identified it, in the table's order.
func unsupportedLanguages(repoDir string) []string {
	hasLock := util.FileExists(filepath.Join(repoDir, CargoLockFile))
	order := make([]string, 0, len(unsupportedMarkers))
	files := make(map[string][]string, len(unsupportedMarkers))
	for i := 0; i < len(unsupportedMarkers); i++ {
		marker := unsupportedMarkers[i]
		if (hasLock && marker.file == "Cargo.toml") || !util.FileExists(filepath.Join(repoDir, marker.file)) {
			continue
		}
		if _, seen := files[marker.language]; !seen {
			order = append(order, marker.language)
		}
		files[marker.language] = append(files[marker.language], marker.file)
	}
	named := make([]string, 0, len(order))
	for i := 0; i < len(order); i++ {
		named = append(named, fmt.Sprintf("%s (%s)", order[i], strings.Join(files[order[i]], ", ")))
	}
	return named
}

const (
	// CargoLockFile marks a Cargo workspace whose dependency graph is committed. The gate runs
	// the Cargo toolchain only where it is present: every Cargo command below is held to it with
	// --locked, and without it there is nothing to hold the build to.
	CargoLockFile = "Cargo.lock"
	// CargoAuditTimeout bounds cargo audit, which fetches the RustSec advisory database before
	// it scans Cargo.lock.
	CargoAuditTimeout = 3 * time.Minute
	// cargoInstallHint says where the Cargo toolchain comes from when cargo is not on PATH.
	cargoInstallHint = "install the Rust toolchain from https://rustup.rs"
	// cargoAuditInstallHint installs the cargo audit subcommand.
	cargoAuditInstallHint = "cargo install cargo-audit --locked"
	// maxCargoOutputBytes bounds how much of a failed cargo command's standard output a stage
	// message carries; its standard error is bounded by util.RunCommand already.
	maxCargoOutputBytes = util.MaxErrorBodyBytes
)

// cargoTestCommands are the test stage's Cargo commands, run in order in the isolated worktree.
var cargoTestCommands = [...][]string{
	{"test", "--workspace", "--locked"},
	{"clippy", "--workspace", "--all-targets", "--", "-D", "warnings"},
}

// requireCargo reports the Cargo part of a stage as not run when cargo is not on PATH. A runner
// without the Rust toolchain is reported, never passed (HISS-21).
func requireCargo(cfg *stageConfig) error {
	if _, err := cfg.lookPath("cargo"); err != nil {
		return skipped(fmt.Sprintf("not run: cargo is not on PATH (%s)", cargoInstallHint))
	}
	return nil
}

// cargoFailure describes a cargo command that failed under a bound of its own. When that bound
// fired while the stage's context was still live, it says so rather than reporting a finding;
// a cut by the run's deadline is named by attributeRunCut instead.
func cargoFailure(ctx, cmdCtx context.Context, what string, bound time.Duration, out string, err error) error {
	excerpt := util.TruncateExcerpt(out, maxCargoOutputBytes)
	if cmdCtx.Err() != nil && ctx.Err() == nil {
		return fmt.Errorf("%s did not finish within its %s bound, so this is not a finding: %w: %s",
			what, bound, err, excerpt)
	}
	return fmt.Errorf("%s failed: %w: %s", what, err, excerpt)
}

// runCargoPrefetch is the prefetch stage's Cargo part: cargo fetch --locked downloads the
// crates Cargo.lock pins and fails when the lockfile no longer matches the manifests.
func runCargoPrefetch(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo fetch --locked not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	pCtx, cancel := context.WithTimeout(ctx, DefaultPrefetchTimeout)
	defer cancel()
	if out, err := cfg.run(pCtx, cfg.repoDir, "cargo", "fetch", "--locked"); err != nil {
		return "", cargoFailure(ctx, pCtx, "cargo fetch --locked", DefaultPrefetchTimeout, out, err)
	}
	return "cargo fetch --locked passed", nil
}

// runCargoSecurity is the security stage's Cargo part: cargo audit checks Cargo.lock against the
// RustSec advisory database. Unlike the Go scanners it is optional: where cargo-audit is not
// installed the part is reported as not run, with the command that installs it, and the stage is
// never recorded as passed on its account (combineParts).
func runCargoSecurity(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo audit not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	if _, err := cfg.lookPath("cargo-audit"); err != nil {
		return "", skipped(fmt.Sprintf("cargo audit not run: cargo-audit is not installed (%s)", cargoAuditInstallHint))
	}
	aCtx, cancel := context.WithTimeout(ctx, CargoAuditTimeout)
	defer cancel()
	if out, err := cfg.run(aCtx, cfg.repoDir, "cargo", "audit"); err != nil {
		return "", cargoFailure(ctx, aCtx, "cargo audit", CargoAuditTimeout, out, err)
	}
	return "cargo audit passed", nil
}

// runCargoTests is the test stage's Cargo part: cargo test and cargo clippy with warnings denied,
// against HEAD in an isolated worktree under the same bound as the Go race detector
// (inStageWorktree), so a raised PRAETOR_TEST_STAGE_TIMEOUT applies to it too.
func runCargoTests(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: cargo test and cargo clippy not run")
	}
	if err := requireCargo(cfg); err != nil {
		return "", err
	}
	budget := EnvRunBudget()
	bound := budget.StageBound
	err := inStageWorktree(ctx, cfg, bound, func(tCtx context.Context, dir string) error {
		for i := 0; i < len(cargoTestCommands); i++ {
			if runErr := runCargoSuite(tCtx, cfg, bound, dir, cargoTestCommands[i]); runErr != nil {
				return runErr
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	msg := "cargo test --workspace --locked and cargo clippy --workspace --all-targets -- -D warnings passed"
	if budget.Note != "" {
		msg += "; " + budget.Note
	}
	return msg, nil
}

// runCargoSuite runs one cargo command of the test stage in the worktree dir. A cut by the stage
// bound or the run deadline is reported as the cut, as the race stage reports one (cutError).
func runCargoSuite(tCtx context.Context, cfg *stageConfig, bound time.Duration, dir string, args []string) error {
	out, err := cfg.run(tCtx, dir, "cargo", args...)
	if err == nil {
		return nil
	}
	what := "cargo " + strings.Join(args, " ")
	excerpt := util.TruncateExcerpt(out, maxCargoOutputBytes)
	if cutErr := cutError(tCtx, what, bound, dir, fmt.Sprintf("%s [%v]", excerpt, err)); cutErr != nil {
		return cutErr
	}
	return fmt.Errorf("%s failed in %s: %s (%w)", what, dir, excerpt, err)
}
