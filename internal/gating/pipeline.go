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
	// TestStageTimeout bounds the race-detector test stage.
	TestStageTimeout = 180 * time.Second
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
	opts := hiss.ScanOptions{Cap: 1000, MaxFuncLOC: 60}
	scanRep, err := hiss.Scan(ctx, cfg.repoDir, opts)
	if err != nil {
		return "", fmt.Errorf("hiss scan error: %w", err)
	}
	if scanRep.Truncated {
		return "", fmt.Errorf("hiss scan error: %w", hiss.ErrScanTruncated)
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
	if out, err := cfg.run(ctx, cfg.repoDir, "gosec", "-conf", GosecConfigFile, "./..."); err != nil {
		return "", fmt.Errorf("gosec found security infractions: %s", out)
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
	if err != nil {
		return "", fmt.Errorf("flavor audit failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("flavor audit cancelled: %w", err)
	}
	if !rep.Passed {
		return "", fmt.Errorf("flavor audit failed (score: %.1f%%, %d missing templates)", rep.Score, len(rep.MissingTemplates))
	}
	return "", nil
}

// runTestStage runs the race detector against HEAD in an isolated worktree. A worktree
// that cannot be created fails the stage rather than silently testing the dirty tree,
// and cleanup failures are surfaced instead of dropped.
func runTestStage(ctx context.Context, cfg *stageConfig) (msg string, err error) {
	if cfg.dryRun {
		return "dry run: race-detector tests skipped", nil
	}

	tCtx, cancel := context.WithTimeout(ctx, TestStageTimeout)
	defer cancel()

	wtMgr := worktree.NewManager(cfg.repoDir)
	if wtMgr == nil {
		return "", fmt.Errorf("cannot create a worktree manager for %s", cfg.repoDir)
	}
	taskID := fmt.Sprintf("gate-%d-%d", os.Getpid(), time.Now().UnixNano())
	wt, createErr := wtMgr.Create(tCtx, taskID, "HEAD")
	if createErr != nil {
		return "", fmt.Errorf("isolated test worktree could not be created in %s: %w", cfg.repoDir, createErr)
	}
	defer func() {
		if cleanErr := removeWorktree(ctx, wtMgr, taskID); cleanErr != nil {
			err = errors.Join(err, cleanErr)
		}
	}()

	if out, testErr := cfg.run(tCtx, wt.Path, "go", "test", "-race", "./..."); testErr != nil {
		return "", fmt.Errorf("tests failed in %s: %s (%w)", wt.Path, out, testErr)
	}
	return "", nil
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
