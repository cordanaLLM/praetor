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
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxDogfoodTargets bounds how many local target repositories are simulated.
	MaxDogfoodTargets = 50
	// MaxTargetDirEntries bounds the scan of the targets directory itself (HISS-02), so a
	// directory full of non-repository entries cannot turn into an unbounded loop.
	MaxTargetDirEntries = 2000
	// reportFilePerm keeps the dogfood report owner-only.
	reportFilePerm os.FileMode = 0o600
)

// ErrGovernanceFailed is returned by RunDogfood in VerifyOnly mode when the host repository
// fails its own context-sync or HISS audit.
var ErrGovernanceFailed = errors.New("dogfood: host repository failed self governance")

// DogfoodOptions configures the dogfooding verification engine.
type DogfoodOptions struct {
	HostRepoPath     string   `json:"host_repo_path"`
	TargetReposDir   string   `json:"target_repos_dir"`
	RemoteRepos      []string `json:"remote_repos,omitempty"`
	BenchmarkPopular bool     `json:"benchmark_popular,omitempty"`
	// ApplyAdoption switches the local target simulation from a dry run to a real adoption.
	// The zero value simulates, so a caller that forgets the field can never mutate someone
	// else's repository.
	// DryRun preserves the earlier API and overrides ApplyAdoption when true.
	DryRun         bool   `json:"dry_run,omitempty"`
	ApplyAdoption  bool   `json:"apply_adoption,omitempty"`
	ReportPath     string `json:"report_path"`
	VerifyOnly     bool   `json:"verify_only"`
	MaxScanTargets int    `json:"max_scan_targets"`
	// HomeDir overrides the workstation home directory whose agent skills are audited. It
	// exists so tests (and sandboxed runs) never read the developer's real home; empty means
	// os.UserHomeDir.
	HomeDir string `json:"home_dir,omitempty"`
	// SkipWorkstationAudit disables the workstation skills audit entirely.
	// SkipWorkstationSkills is the earlier spelling; either skip flag disables the audit.
	SkipWorkstationSkills bool `json:"skip_workstation_skills,omitempty"`
	SkipWorkstationAudit  bool `json:"skip_workstation_audit,omitempty"`
}

// TargetAdoptionResult records simulation results for a specific repository.
type TargetAdoptionResult struct {
	RepoName  string `json:"repo_name"`
	Archetype string `json:"archetype"`
	DebtCount int    `json:"debt_count"`
	Actions   int    `json:"actions"`
	Passed    bool   `json:"passed"`
	Error     string `json:"error,omitempty"`
}

// DogfoodReport summarizes the end-to-end dogfooding run.
type DogfoodReport struct {
	HostRepoPath       string                 `json:"host_repo_path"`
	Timestamp          time.Time              `json:"timestamp"`
	SelfAuditPassed    bool                   `json:"self_audit_passed"`
	ContextSyncPassed  bool                   `json:"context_sync_passed"`
	ContextSyncError   string                 `json:"context_sync_error,omitempty"`
	TargetsEvaluated   int                    `json:"targets_evaluated"`
	TargetResults      []TargetAdoptionResult `json:"target_results"`
	RemoteResults      []RemoteAdoptionResult `json:"remote_results,omitempty"`
	SkippedRemotes     []string               `json:"skipped_remotes,omitempty"`
	TotalSkillsAudited int                    `json:"total_skills_audited"`
	OverallPassed      bool                   `json:"overall_passed"`
}

// governanceResult is the outcome of the host repository's own governance verification.
type governanceResult struct {
	synced      bool
	syncErr     string
	auditPassed bool
}

// verifySelfGovernance verifies cross-agent context synchronisation and the HISS invariants
// of the host repository.
func verifySelfGovernance(ctx context.Context, hostPath string) (governanceResult, error) {
	res := governanceResult{}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("context cancelled before self governance check: %w", err)
	}

	agentsFile := filepath.Join(hostPath, "AGENTS.md")
	tr := compiler.NewTranspiler()
	if vErr := tr.Verify(agentsFile, hostPath); vErr != nil {
		res.syncErr = vErr.Error()
	} else {
		res.synced = true
	}

	scanRes, sErr := hiss.Scan(ctx, hostPath, hiss.ScanOptions{})
	if sErr != nil {
		return res, fmt.Errorf("hiss scan failed: %w", sErr)
	}

	res.auditPassed = scanRes.TotalInfractions == 0 && !scanRes.Truncated
	return res, nil
}

// targetScanLimit normalises the caller's target bound.
func targetScanLimit(maxTargets int) int {
	if maxTargets <= 0 || maxTargets > MaxDogfoodTargets {
		return MaxDogfoodTargets
	}
	return maxTargets
}

// adoptTarget simulates (or applies) adoption on one target repository.
func adoptTarget(ctx context.Context, targetPath, repoName string, apply bool) TargetAdoptionResult {
	plan, aErr := adopt.Adopt(ctx, adopt.AdoptOptions{
		Path:   targetPath,
		DryRun: !apply,
	})

	res := TargetAdoptionResult{RepoName: repoName, Passed: aErr == nil}
	if aErr != nil {
		res.Error = aErr.Error()
		return res
	}
	if plan != nil {
		res.Archetype = plan.Archetype
		res.DebtCount = plan.LegacyDebtCount
		res.Actions = len(plan.CreatedFiles) + len(plan.ReconciledFiles)
	}
	return res
}

// testTargetAdoptions runs the adoption simulation over every git repository directly under
// targetsDir. A directory that cannot be read is an error: a mistyped --targets path must
// not be reported as "zero targets, all good". Only directories that actually hold a git
// repository consume the target budget.
func testTargetAdoptions(ctx context.Context, opts DogfoodOptions, report *DogfoodReport) error {
	if opts.TargetReposDir == "" {
		return nil
	}

	entries, err := os.ReadDir(opts.TargetReposDir)
	if err != nil {
		return fmt.Errorf("read targets directory %s: %w", opts.TargetReposDir, err)
	}

	limit := targetScanLimit(opts.MaxScanTargets)
	for i := 0; i < len(entries) && i < MaxTargetDirEntries; i++ {
		if cErr := ctx.Err(); cErr != nil {
			return fmt.Errorf("context cancelled during target testing: %w", cErr)
		}
		if len(report.TargetResults) >= limit {
			break
		}
		entry := entries[i]
		if !entry.IsDir() {
			continue
		}
		targetPath := filepath.Join(opts.TargetReposDir, entry.Name())
		if !util.PathExists(filepath.Join(targetPath, ".git")) {
			continue
		}
		report.TargetResults = append(report.TargetResults, adoptTarget(ctx, targetPath, entry.Name(), opts.ApplyAdoption && !opts.DryRun))
	}

	report.TargetsEvaluated = len(report.TargetResults)
	return nil
}

// auditWorkstationSkills records how many agent skill manifests the workstation holds.
func auditWorkstationSkills(ctx context.Context, homeDir string, report *DogfoodReport) error {
	if homeDir == "" {
		return nil
	}

	skillRep, err := harvester.AuditSkills(ctx, homeDir, "")
	if err != nil {
		return fmt.Errorf("audit workstation skills in %s: %w", homeDir, err)
	}

	report.TotalSkillsAudited = skillRep.TotalSkills
	return nil
}

// resolveAuditHome returns the home directory whose skills should be audited.
func resolveAuditHome(opts DogfoodOptions) (string, error) {
	if opts.SkipWorkstationAudit || opts.SkipWorkstationSkills {
		return "", nil
	}
	if opts.HomeDir != "" {
		return opts.HomeDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return home, nil
}

// runAdoptionSuites executes the local target and remote benchmark simulations.
func runAdoptionSuites(ctx context.Context, opts DogfoodOptions, report *DogfoodReport) error {
	if err := testTargetAdoptions(ctx, opts, report); err != nil {
		return fmt.Errorf("testing target adoptions: %w", err)
	}

	homeDir, hErr := resolveAuditHome(opts)
	if hErr != nil {
		return hErr
	}
	if err := auditWorkstationSkills(ctx, homeDir, report); err != nil {
		return err
	}

	remoteURLs := make([]string, 0, len(opts.RemoteRepos)+len(PopularBenchmarks))
	remoteURLs = append(remoteURLs, opts.RemoteRepos...)
	if opts.BenchmarkPopular {
		remoteURLs = append(remoteURLs, PopularBenchmarks...)
	}
	if len(remoteURLs) == 0 {
		return nil
	}
	if err := testRemoteAdoptions(ctx, remoteURLs, report); err != nil {
		return fmt.Errorf("testing remote adoptions: %w", err)
	}
	return nil
}

// computeOverallPassed folds every simulation result into the overall verdict. A run in
// which an adoption simulation failed is not a passing run.
func computeOverallPassed(report *DogfoodReport) bool {
	passed := report.ContextSyncPassed && report.SelfAuditPassed
	for i := 0; i < len(report.TargetResults) && i < MaxDogfoodTargets; i++ {
		passed = passed && report.TargetResults[i].Passed
	}
	for i := 0; i < len(report.RemoteResults) && i < MaxRemoteTargets; i++ {
		passed = passed && report.RemoteResults[i].Passed
	}
	return passed
}

// governanceFailure renders the VerifyOnly error for a failed host audit.
func governanceFailure(report *DogfoodReport) error {
	if report.ContextSyncPassed && report.SelfAuditPassed {
		return nil
	}
	return fmt.Errorf("%w: context sync passed=%t (%s), HISS audit passed=%t",
		ErrGovernanceFailed, report.ContextSyncPassed, report.ContextSyncError, report.SelfAuditPassed)
}

// RunDogfood executes comprehensive self-governance and multi-repo adoption validation.
// In VerifyOnly mode it returns an error whenever the host repository itself fails, which
// is what makes `--verify-only` able to turn a CI run red.
func RunDogfood(ctx context.Context, opts DogfoodOptions) (*DogfoodReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before dogfooding: %w", err)
	}

	hostPath := opts.HostRepoPath
	if hostPath == "" {
		hostPath = "."
	}

	report := &DogfoodReport{
		HostRepoPath:  hostPath,
		Timestamp:     time.Now(),
		TargetResults: make([]TargetAdoptionResult, 0),
		RemoteResults: make([]RemoteAdoptionResult, 0),
	}

	gov, err := verifySelfGovernance(ctx, hostPath)
	report.ContextSyncPassed = gov.synced
	report.ContextSyncError = gov.syncErr
	report.SelfAuditPassed = gov.auditPassed
	if err != nil {
		return report, fmt.Errorf("self governance audit failed: %w", err)
	}
	if opts.VerifyOnly {
		if gErr := governanceFailure(report); gErr != nil {
			return report, gErr
		}
	}

	if sErr := runAdoptionSuites(ctx, opts, report); sErr != nil {
		return report, sErr
	}

	report.OverallPassed = computeOverallPassed(report)

	if wErr := writeDogfoodReport(opts.ReportPath, report); wErr != nil {
		return report, wErr
	}

	return report, nil
}

// writeDogfoodReport serialises the report to an owner-only JSON file.
func writeDogfoodReport(path string, report *DogfoodReport) error {
	if path == "" {
		return nil
	}
	data, mErr := json.MarshalIndent(report, "", "  ")
	if mErr != nil {
		return fmt.Errorf("marshal dogfood report: %w", mErr)
	}
	if err := util.WriteFileSecure(path, data, reportFilePerm); err != nil {
		return fmt.Errorf("write dogfood report: %w", err)
	}
	return nil
}
