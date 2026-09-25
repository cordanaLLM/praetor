package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
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

// ErrMissingScanner reports that a required security scanner is absent, which must fail
// the security stage instead of silently passing it.
var ErrMissingScanner = errors.New("required security scanner is not installed")

// StageResult captures the outcome of a single gating pipeline stage.
type StageResult struct {
	Name     string        `json:"name"`
	Passed   bool          `json:"passed"`
	Duration time.Duration `json:"duration"`
	Message  string        `json:"message,omitempty"`
}

// PipelineReport aggregates the entire gated pre-merge verification.
type PipelineReport struct {
	Status           GatingStatus  `json:"status"`
	RepoDir          string        `json:"repo_dir"`
	Repository       string        `json:"repository,omitempty"`
	CommitSHA        string        `json:"commit_sha,omitempty"`
	WorktreeClean    bool          `json:"worktree_clean"`
	DryRun           bool          `json:"dry_run"`
	ReceiptSignature string        `json:"receipt_signature,omitempty"`
	ReceiptPath      string        `json:"receipt_path,omitempty"`
	Stages           []StageResult `json:"stages"`
	TotalElapsed     time.Duration `json:"total_elapsed"`
}

// StageOutput renders the canonical, deterministic byte stream whose SHA-256 the Exit-0
// receipt signs. It binds the receipt to the concrete stage results, the scanned commit
// and the cleanliness of the tree that was actually scanned.
func (r *PipelineReport) StageOutput() []byte {
	lines := make([]string, 0, len(r.Stages)+5)
	lines = append(lines,
		"praetor-gate-output/v1",
		fmt.Sprintf("repository\t%s", r.Repository),
		fmt.Sprintf("commit_sha\t%s", r.CommitSHA),
		fmt.Sprintf("worktree_clean\t%t", r.WorktreeClean),
		fmt.Sprintf("dry_run\t%t", r.DryRun),
	)
	for i := 0; i < len(r.Stages) && i < maxStages; i++ {
		s := r.Stages[i]
		lines = append(lines, fmt.Sprintf("stage\t%s\t%t\t%s",
			s.Name, s.Passed, strings.ReplaceAll(s.Message, "\n", " ")))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// commandRunner executes an external command. util.RunCommand is the production
// implementation; tests substitute a fake to exercise stage logic hermetically.
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
	// it to reach the truncation path without synthesizing a repository of that size.
	scanOpts hiss.ScanOptions
}

// newStageConfig builds a stage configuration backed by the real toolchain.
func newStageConfig(repoDir string, dryRun bool, rep *PipelineReport) *stageConfig {
	return &stageConfig{
		repoDir:  repoDir,
		dryRun:   dryRun,
		run:      util.RunCommand,
		lookPath: exec.LookPath,
		rep:      rep,
	}
}

// RunGatedPipeline executes the six-stage anti-direct-merge gating pipeline: prefetch and
// lockfiles, HISS invariants against the debt baseline, security and SCA scanning, flavor
// conformance, race-detector tests in an isolated worktree, and the Ed25519 Exit-0
// receipt. A dry run skips the test stage and therefore mints no receipt.
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
	describeTree(ctx, repoDir, rep)

	cfg := newStageConfig(repoDir, dryRun, rep)
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
// tree that the scan stages inspect is clean.
func describeTree(ctx context.Context, repoDir string, rep *PipelineReport) {
	gitCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()

	rep.Repository = resolveRepositoryName(gitCtx, repoDir)
	rep.CommitSHA = getGitCommitSHA(gitCtx, repoDir)
	status, err := util.RunGit(gitCtx, repoDir, "status", "--porcelain")
	rep.WorktreeClean = err == nil && strings.TrimSpace(status) == ""
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
// recorded even when it passes, which is how skipped work stays visible.
type stage struct {
	name string
	fn   func(context.Context, *stageConfig) (string, error)
}

func executeStages(ctx context.Context, cfg *stageConfig) error {
	stages := []stage{
		{"Prefetch & Lockfiles", runPrefetchStage},
		{"HISS Invariant Scan", runHissStage},
		{"Security & SCA Scan", runSecurityStage},
		{"Flavor Conformance", runFlavorStage},
		{"Race-Detector Tests", runTestStage},
		{"Ed25519 Exit-0 Receipt", runReceiptStage},
	}

	for i := 0; i < len(stages) && i < maxStages; i++ {
		if err := executeStage(ctx, stages[i], cfg); err != nil {
			return err
		}
	}
	return nil
}

func executeStage(ctx context.Context, s stage, cfg *stageConfig) error {
	sStart := time.Now()
	msg, err := s.fn(ctx, cfg)
	err = attributeRunCut(ctx, s.name, err)
	res := StageResult{
		Name:     s.name,
		Passed:   err == nil,
		Duration: time.Since(sStart),
		Message:  msg,
	}
	if err != nil {
		res.Message = err.Error()
	}
	cfg.rep.Stages = append(cfg.rep.Stages, res)
	return err
}

func runPrefetchStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if err := VerifyLockfiles(cfg.repoDir); err != nil {
		return "", err
	}
	rep, err := PrefetchDependencies(ctx, cfg.repoDir)
	if err != nil {
		return "", err
	}
	if rep.Skipped {
		return "no go.mod: module prefetch skipped", nil
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
	scanRep, err := hiss.Scan(ctx, cfg.repoDir, cfg.scanOpts)
	if err != nil {
		return "", fmt.Errorf("hiss scan error: %w", err)
	}
	// Incomplete covers truncation and files that yielded no analyzable structure. Checking
	// only truncation let an unparseable file through as a clean gate.
	if scanRep.Incomplete() {
		return "", fmt.Errorf("hiss scan error: %w: %s", hiss.ErrScanIncomplete, scanRep.CoverageEvidence())
	}

	base, err := baseline.LoadBaseline(filepath.Join(cfg.repoDir, ".standards-baseline.json"))
	if err != nil {
		return "", fmt.Errorf("load debt baseline: %w", err)
	}

	current := hiss.ConvertToBaseline(scanRep.Violations)
	for i := range current {
		current[i].Fingerprint = fmt.Sprintf("%s:%d:%s", current[i].FilePath, current[i].LineNumber, current[i].RuleID)
	}

	ratchet := baseline.EvaluateRatchet(base, current, nil)
	if !ratchet.Passed {
		return "", fmt.Errorf("hiss ratchet failed: %d infractions (%d new, baseline %d)",
			ratchet.CurrentCount, len(ratchet.NewViolations), base.TotalInfractions)
	}
	return fmt.Sprintf("%d infractions within the %d baselined limit",
		ratchet.CurrentCount, base.TotalInfractions), nil
}

// runSecurityStage runs govulncheck and gosec. A missing scanner fails the stage: a
// security gate that certifies a run in which nothing executed is worse than no gate.
func runSecurityStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return "no go.mod: Go security scanners skipped", nil
	}

	if err := requireScanner(cfg, "govulncheck", "go install golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
		return "", err
	}
	if out, err := cfg.run(ctx, cfg.repoDir, "govulncheck", "./..."); err != nil {
		return "", fmt.Errorf("govulncheck found vulnerabilities: %s", out)
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

func runFlavorStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("flavor audit cancelled: %w", err)
	}
	rep, err := flavor.AuditFlavor(cfg.repoDir, "auto")
	if errors.Is(err, flavor.ErrFlavorNotApplicable) {
		// Not a pass and not a failure: this repository's declared profile has no flavor, so
		// there is nothing for this stage to check. Reporting it is the point -- a skipped
		// stage that reads as a pass is how a gate comes to certify what it never examined.
		return "skipped: " + err.Error(), nil
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

// runTestStage runs the race detector against HEAD in an isolated worktree. A worktree
// that cannot be created fails the stage rather than silently testing the dirty tree,
// and cleanup failures are surfaced instead of dropped.
func runTestStage(ctx context.Context, cfg *stageConfig) (msg string, err error) {
	if cfg.dryRun {
		return "dry run: race-detector tests skipped", nil
	}
	// `go test -race ./...` cannot run where there is no module, exactly as the prefetch
	// and security stages already recognise. Without this the stage failed every adopted
	// non-Go repository with "directory prefix . does not contain main module", which
	// reads as a broken repository rather than an inapplicable stage. Those repositories
	// are gated on their own suites by the verification contract, not here.
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return "no go.mod: Go race-detector tests skipped", nil
	}
	// The race detector needs cgo and a host C toolchain. Without this check the stage
	// does not report "no C compiler", it reports `# runtime/cgo` followed by every
	// package failing to build -- which reads as a repository whose whole tree is
	// broken. On a Windows box without gcc that is every push, including a push
	// fixing Windows support, so the gate could not be repaired from the platform it
	// was broken on. `.config/lefthook/scripts/test_hooks.py` already reasons exactly
	// this way for the harness self-tests; this is the same rule for the Go gate.
	if available, absent := raceDetectorAvailable(ctx, cfg); !available {
		return fmt.Sprintf(
			"race detector unavailable (%s): race-detector tests skipped; "+
				"CI runs this leg on Linux with cgo", absent), nil
	}

	budget := EnvRunBudget()
	bound := budget.StageBound
	tCtx, cancel := withStageBound(ctx, bound)
	defer cancel()

	wtMgr := worktree.NewManager(cfg.repoDir)
	if wtMgr == nil {
		return "", fmt.Errorf("cannot create a worktree manager for %s", cfg.repoDir)
	}
	taskID := fmt.Sprintf("gate-%d-%d", os.Getpid(), time.Now().UnixNano())
	wt, createErr := createStageWorktree(tCtx, wtMgr, taskID, bound, cfg.repoDir)
	if createErr != nil {
		return "", createErr
	}
	defer func() {
		if cleanErr := removeWorktree(ctx, wtMgr, taskID); cleanErr != nil {
			err = errors.Join(err, cleanErr)
		}
	}()

	if out, testErr := cfg.run(tCtx, wt.Path, "go", "test", "-race", "./..."); testErr != nil {
		// A stage killed by a deadline is not a failing suite, and printing it as one sends
		// every reader to diagnose a change that was never the cause. The context is the
		// authoritative witness: the child dies of a signal and reports nothing useful. Its
		// cause also says which deadline fired, the stage's own bound or the whole run's.
		if cutErr := cutError(tCtx, "race-detector tests", bound, wt.Path, out); cutErr != nil {
			return "", cutErr
		}
		return "", fmt.Errorf("tests failed in %s: %s (%w)", wt.Path, out, testErr)
	}
	return budget.Note, nil
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
// it never mints a receipt for a dry run, which by definition did not run the tests.
func runReceiptStage(_ context.Context, cfg *stageConfig) (string, error) {
	rep := cfg.rep
	if cfg.dryRun {
		return "dry run: no Exit-0 receipt minted", nil
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
