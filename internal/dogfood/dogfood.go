package dogfood

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

const (
	MaxDogfoodTargets = 50
)

// DogfoodOptions configures the dogfooding verification engine.
type DogfoodOptions struct {
	HostRepoPath     string   `json:"host_repo_path"`
	TargetReposDir   string   `json:"target_repos_dir"`
	RemoteRepos      []string `json:"remote_repos,omitempty"`
	BenchmarkPopular bool     `json:"benchmark_popular,omitempty"`
	DryRun           bool     `json:"dry_run,omitempty"`
	ReportPath       string   `json:"report_path"`
	VerifyOnly       bool     `json:"verify_only"`
	MaxScanTargets   int      `json:"max_scan_targets"`
}

// TargetAdoptionResult records simulation results for a specific repository.
type TargetAdoptionResult struct {
	RepoName  string `json:"repo_name"`
	Archetype string `json:"archetype"`
	DebtCount int    `json:"debt_count"`
	Actions   int    `json:"actions"`
	Passed    bool   `json:"passed"`
}

// DogfoodReport summarizes the end-to-end dogfooding run.
type DogfoodReport struct {
	HostRepoPath       string                 `json:"host_repo_path"`
	Timestamp          time.Time              `json:"timestamp"`
	SelfAuditPassed    bool                   `json:"self_audit_passed"`
	ContextSyncPassed  bool                   `json:"context_sync_passed"`
	TargetsEvaluated   int                    `json:"targets_evaluated"`
	TargetResults      []TargetAdoptionResult `json:"target_results"`
	RemoteResults      []RemoteAdoptionResult `json:"remote_results,omitempty"`
	TotalSkillsAudited int                    `json:"total_skills_audited"`
	OverallPassed      bool                   `json:"overall_passed"`
}

func verifySelfGovernance(ctx context.Context, hostPath string) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, fmt.Errorf("context cancelled before self governance check: %w", err)
	}

	// 1. Verify Context Synchronization
	agentsFile := filepath.Join(hostPath, "AGENTS.md")
	tr := compiler.NewTranspiler()
	vErr := tr.Verify(agentsFile, hostPath)
	synced := vErr == nil

	// 2. Verify HISS Invariants
	scanRes, sErr := hiss.Scan(ctx, hostPath, hiss.ScanOptions{})
	if sErr != nil {
		return synced, false, fmt.Errorf("hiss scan failed: %w", sErr)
	}

	auditPassed := scanRes.TotalInfractions == 0
	return synced, auditPassed, nil
}

func testTargetAdoptions(ctx context.Context, targetsDir string, maxTargets int, report *DogfoodReport) error {
	if targetsDir == "" {
		return nil
	}

	entries, err := os.ReadDir(targetsDir)
	if err != nil {
		return nil
	}

	limit := maxTargets
	if limit <= 0 || limit > MaxDogfoodTargets {
		limit = MaxDogfoodTargets
	}

	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during target testing: %w", err)
		}
		if count >= limit {
			break
		}
		count++

		if !entry.IsDir() {
			continue
		}

		targetPath := filepath.Join(targetsDir, entry.Name())
		gitDir := filepath.Join(targetPath, ".git")
		if _, statErr := os.Stat(gitDir); statErr != nil {
			continue
		}

		plan, aErr := adopt.Adopt(ctx, adopt.AdoptOptions{
			Path:   targetPath,
			DryRun: true,
		})

		res := TargetAdoptionResult{
			RepoName: entry.Name(),
			Passed:   aErr == nil,
		}
		if aErr == nil && plan != nil {
			res.Archetype = plan.Archetype
			res.DebtCount = plan.LegacyDebtCount
			res.Actions = len(plan.CreatedFiles) + len(plan.ReconciledFiles)
		}
		report.TargetResults = append(report.TargetResults, res)
	}

	report.TargetsEvaluated = len(report.TargetResults)
	return nil
}

func auditWorkstationSkills(ctx context.Context, homeDir string, report *DogfoodReport) error {
	if homeDir == "" {
		return nil
	}

	skillRep, err := harvester.AuditSkills(ctx, homeDir, "")
	if err != nil {
		return nil
	}

	report.TotalSkillsAudited = skillRep.TotalSkills
	return nil
}

// RunDogfood executes comprehensive self-governance and multi-repo adoption validation.
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

	// 1. Self-Governance Verification
	ctxSync, selfAudit, err := verifySelfGovernance(ctx, hostPath)
	report.ContextSyncPassed = ctxSync
	report.SelfAuditPassed = selfAudit
	if err != nil && opts.VerifyOnly {
		return report, fmt.Errorf("self governance audit failed: %w", err)
	}

	// 2. Multi-Target Adoption Simulation
	if err := testTargetAdoptions(ctx, opts.TargetReposDir, opts.MaxScanTargets, report); err != nil {
		return report, fmt.Errorf("testing target adoptions: %w", err)
	}

	// 3. Workstation Skills Audit
	homeDir, hErr := os.UserHomeDir()
	if hErr == nil {
		if err := auditWorkstationSkills(ctx, homeDir, report); err != nil {
			return report, fmt.Errorf("audit workstation skills: %w", err)
		}
	}

	// 4. Remote Non-Owned Public Repo Dogfooding
	remoteURLs := opts.RemoteRepos
	if opts.BenchmarkPopular {
		remoteURLs = append(remoteURLs, PopularBenchmarks...)
	}
	if len(remoteURLs) > 0 {
		if err := testRemoteAdoptions(ctx, remoteURLs, report); err != nil {
			return report, fmt.Errorf("testing remote adoptions: %w", err)
		}
	}

	report.OverallPassed = report.ContextSyncPassed && report.SelfAuditPassed

	if err := writeDogfoodReport(opts.ReportPath, report); err != nil {
		return report, err
	}

	return report, nil
}

func writeDogfoodReport(path string, report *DogfoodReport) error {
	if path == "" {
		return nil
	}
	data, mErr := json.MarshalIndent(report, "", "  ")
	if mErr != nil {
		return fmt.Errorf("marshal dogfood report: %w", mErr)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write dogfood report: %w", err)
	}
	return nil
}
