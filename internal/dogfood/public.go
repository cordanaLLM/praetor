package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxPublicRepositories bounds a retained public loop invocation.
	MaxPublicRepositories = 8
	// MaxPublicAttempts bounds reconciliation, including the idempotence recheck.
	MaxPublicAttempts = 3
	publicLoopTimeout = 5 * time.Minute
)

// ErrPublicLoopFailed means at least one retained public case did not verify.
var ErrPublicLoopFailed = errors.New("public dogfood loop did not verify every repository")

// PublicLoopOptions selects explicit public sources and a retained evidence directory.
// Repository URLs must be curated HTTPS URLs, optionally suffixed #<commit SHA>.
// Apply authorizes Praetor scaffolding only inside newly created disposable clones;
// upstream code, hooks, build scripts and tests are never executed.
type PublicLoopOptions struct {
	Repositories []string `json:"repositories"`
	SourceRoot   string   `json:"source_root"`
	ArtifactDir  string   `json:"artifact_dir"`
	Apply        bool     `json:"apply"`
	MaxAttempts  int      `json:"max_attempts"`
}

// PublicAttempt retains actual reconciliation and verification outcomes.
type PublicAttempt struct {
	Number       int                 `json:"number"`
	Adoption     *adopt.AdoptReport  `json:"adoption,omitempty"`
	Verification *PublicVerification `json:"verification,omitempty"`
	TreeDigest   string              `json:"tree_digest,omitempty"`
	ChangedFiles []string            `json:"changed_files,omitempty"`
	Error        string              `json:"error,omitempty"`
}

// PublicRepositoryResult distinguishes plans, verified applications and failures.
type PublicRepositoryResult struct {
	Repository         string             `json:"repository"`
	RequestedSHA       string             `json:"requested_sha,omitempty"`
	SourceSHA          string             `json:"source_sha,omitempty"`
	Checkout           string             `json:"checkout"`
	Status             string             `json:"status"`
	Plan               *adopt.AdoptReport `json:"plan,omitempty"`
	OriginalScan       *hiss.ScanReport   `json:"original_scan,omitempty"`
	OriginalTreeDigest string             `json:"original_tree_digest,omitempty"`
	Attempts           []PublicAttempt    `json:"attempts,omitempty"`
	Error              string             `json:"error,omitempty"`
}

// PublicLoopReport names the durable evidence and the exact verification scope.
type PublicLoopReport struct {
	Options   PublicLoopOptions        `json:"options"`
	RunDir    string                   `json:"run_dir"`
	StartedAt time.Time                `json:"started_at"`
	Results   []PublicRepositoryResult `json:"results"`
	Verified  bool                     `json:"verified"`
	Scope     string                   `json:"scope"`
}

// RunPublicLoop clones immutable public inputs, plans, applies and rechecks Praetor
// governance with a bounded attempt budget. Every clone and failure is retained.
// A successful dry run is planned, never Verified. No workstation data is read.
func RunPublicLoop(ctx context.Context, opts PublicLoopOptions) (*PublicLoopReport, error) {
	if ctx == nil {
		return nil, errors.New("public dogfood requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, publicLoopTimeout)
	defer cancel()
	sources, err := validatePublicOptions(ctx, &opts)
	if err != nil {
		return nil, err
	}
	runDir, err := createPublicRun(opts.ArtifactDir)
	if err != nil {
		return nil, err
	}
	report := &PublicLoopReport{Options: opts, RunDir: runDir, StartedAt: time.Now().UTC(),
		Scope: "Praetor lock pins/digests, generated context, HISS debt ratchet and repeat-apply stability; upstream builds/tests are not executed"}
	ctx, err = publicCommandContext(ctx, runDir)
	if err != nil {
		return report, err
	}
	for i := 0; i < len(sources) && i < MaxPublicRepositories; i++ {
		result := runPublicRepository(ctx, opts, sources[i], filepath.Join(runDir, fmt.Sprintf("repo-%02d", i+1)))
		report.Results = append(report.Results, result)
		if err := savePublicJSON(filepath.Join(runDir, "report.json"), report); err != nil {
			return report, err
		}
	}
	report.Verified = opts.Apply && publicResultsVerified(report.Results)
	if err := savePublicJSON(filepath.Join(runDir, "report.json"), report); err != nil {
		return report, err
	}
	if publicResultsFailed(report.Results) {
		return report, ErrPublicLoopFailed
	}
	return report, nil
}

func runPublicRepository(ctx context.Context, opts PublicLoopOptions, source publicSource, dir string) PublicRepositoryResult {
	result := PublicRepositoryResult{Repository: source.url, RequestedSHA: source.sha, Checkout: filepath.Join(dir, "checkout"), Status: "failed"}
	if err := util.MkdirSecure(dir, 0o700); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := executePublicRepository(ctx, opts, source, &result); err != nil {
		result.Error = err.Error()
	}
	if err := savePublicJSON(filepath.Join(dir, "result.json"), result); err != nil {
		result.Status = "failed"
		if result.Error != "" {
			err = fmt.Errorf("%s; %w", result.Error, err)
		}
		result.Error = err.Error()
	}
	return result
}

func executePublicRepository(ctx context.Context, opts PublicLoopOptions, source publicSource, result *PublicRepositoryResult) error {
	sha, err := clonePublicSource(ctx, source, result.Checkout)
	if err != nil {
		return err
	}
	result.SourceSHA = sha
	original, err := snapshotPublicTree(ctx, result.Checkout)
	if err != nil {
		return err
	}
	result.OriginalTreeDigest = original.digest()
	result.OriginalScan, err = scanPublicTree(ctx, result.Checkout)
	if err != nil {
		return err
	}
	adoptOpts := adopt.AdoptOptions{Path: result.Checkout, DryRun: true, RecordBaseline: true, SkipHookActivation: true, LockSourceRoot: opts.SourceRoot}
	result.Plan, err = adopt.Adopt(ctx, adoptOpts)
	if err := publicAdoptionError(result.Plan, err); err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	if !opts.Apply {
		result.Status = "planned"
		return nil
	}
	return reconcilePublicRepository(ctx, opts, result, original, adoptOpts)
}

func reconcilePublicRepository(ctx context.Context, opts PublicLoopOptions, result *PublicRepositoryResult, original publicTree, adoptOpts adopt.AdoptOptions) error {
	previous := ""
	for i := 0; i < opts.MaxAttempts && i < MaxPublicAttempts; i++ {
		adoptOpts.DryRun = false
		adoptOpts.RecordBaseline = i == 0
		attempt := applyPublicAttempt(ctx, adoptOpts, original, result.OriginalScan, i+1)
		result.Attempts = append(result.Attempts, attempt)
		if attempt.Error != "" {
			return fmt.Errorf("attempt %d: %s", i+1, attempt.Error)
		}
		if previous == attempt.TreeDigest {
			result.Status = "verified"
			return nil
		}
		previous = attempt.TreeDigest
	}
	return fmt.Errorf("repository did not stabilize within %d applications", opts.MaxAttempts)
}

func publicAdoptionError(report *adopt.AdoptReport, err error) error {
	if err != nil {
		return err
	}
	if report == nil {
		return errors.New("adoption returned no report")
	}
	if len(report.Errors) != 0 {
		return fmt.Errorf("adoption reported incomplete work: %v", report.Errors)
	}
	return nil
}

func publicResultsVerified(results []PublicRepositoryResult) bool {
	for i := 0; i < len(results) && i < MaxPublicRepositories; i++ {
		if results[i].Status != "verified" {
			return false
		}
	}
	return len(results) > 0
}

func publicResultsFailed(results []PublicRepositoryResult) bool {
	for i := 0; i < len(results) && i < MaxPublicRepositories; i++ {
		if results[i].Status == "failed" {
			return true
		}
	}
	return false
}

func savePublicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	if err := util.WriteFileNoFollow(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("retain evidence: %w", err)
	}
	return nil
}

func createPublicRun(root string) (string, error) {
	if info, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		if err := util.MkdirSecure(root, 0o700); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", errors.New("artifact root is not a directory")
	}
	dir, err := os.MkdirTemp(root, "public-loop-*")
	if err != nil {
		return "", fmt.Errorf("create retained public run: %w", err)
	}
	return dir, nil
}
