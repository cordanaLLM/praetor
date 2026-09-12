package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MaxRemoteTargets     = 20
	DefaultRemoteTimeout = 2 * time.Minute
)

// ErrInvalidRepoURL is returned when a remote repository URL is empty, would be parsed
// as a git option, or carries shell metacharacters.
var ErrInvalidRepoURL = errors.New("dogfood: invalid remote repository URL")

// PopularBenchmarks contains curated open-source repositories representing diverse archetypes.
var PopularBenchmarks = []string{
	"https://github.com/gin-gonic/gin",
	"https://github.com/spf13/cobra",
	"https://github.com/pallets/flask",
	"https://github.com/sveltejs/template",
	"https://github.com/google/googletest",
	"https://github.com/BurntSushi/ripgrep",
	"https://github.com/fastify/fastify",
	"https://github.com/spring-projects/spring-petclinic",
}

// RemoteAdoptionResult records adoption and HISS governance simulation for an external repository.
type RemoteAdoptionResult struct {
	RepoURL          string `json:"repo_url"`
	Archetype        string `json:"archetype"`
	DebtCount        int    `json:"debt_count"`
	HISSInfractions  int    `json:"hiss_infractions"`
	SimulatedActions int    `json:"simulated_actions"`
	ReadinessGrade   string `json:"readiness_grade"`
	DurationMs       int64  `json:"duration_ms"`
	Passed           bool   `json:"passed"`
	Error            string `json:"error,omitempty"`
}

// calculateReadinessGrade assigns an adoption readiness rating based on debt and HISS infractions.
func calculateReadinessGrade(debtCount, hissInfractions int) string {
	if hissInfractions == 0 && debtCount <= 5 {
		return "A"
	}
	if hissInfractions <= 10 && debtCount <= 20 {
		return "B"
	}
	if hissInfractions <= 50 {
		return "C"
	}
	return "F"
}

// validateRepoURL rejects URLs that cannot safely be handed to git as a positional
// argument: empty values, values git would parse as an option, and values carrying shell
// metacharacters or control bytes (util.ValidateExecArg).
func validateRepoURL(repoURL string) (string, error) {
	trimmed := strings.TrimSpace(repoURL)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidRepoURL)
	}
	if err := util.ValidateExecArg(trimmed); err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrInvalidRepoURL, trimmed, err)
	}
	return trimmed, nil
}

// cloneEphemeralRepo clones a remote repository into a temporary directory with depth 1.
// The clone runs through util.RunCommand, the single audited exec entry point, under a
// DefaultRemoteTimeout deadline (HISS-02).
func cloneEphemeralRepo(ctx context.Context, repoURL, targetDir string) error {
	trimmed, err := validateRepoURL(repoURL)
	if err != nil {
		return err
	}

	cloneCtx, cancel := context.WithTimeout(ctx, DefaultRemoteTimeout)
	defer cancel()

	output, err := util.RunCommand(cloneCtx, "", "git", "clone", "--depth", "1", "--single-branch", "--", trimmed, targetDir)
	if err != nil {
		return fmt.Errorf("git clone failed (%s): %w", output, err)
	}
	return nil
}

// testSingleRemoteAdoption executes dry-run adoption and HISS audit on a single remote URL.
func testSingleRemoteAdoption(ctx context.Context, repoURL string) (*RemoteAdoptionResult, error) {
	startTime := time.Now()
	res := &RemoteAdoptionResult{
		RepoURL: repoURL,
	}

	tempDir, err := os.MkdirTemp("", "praetor-remote-dogfood-*")
	if err != nil {
		return nil, fmt.Errorf("creating ephemeral sandbox: %w", err)
	}
	defer func() {
		rmErr := os.RemoveAll(tempDir)
		if rmErr != nil {
			return
		}
	}()

	if cloneErr := cloneEphemeralRepo(ctx, repoURL, tempDir); cloneErr != nil {
		res.Error = cloneErr.Error()
		res.DurationMs = time.Since(startTime).Milliseconds()
		return res, nil
	}

	plan, aErr := adopt.Adopt(ctx, adopt.AdoptOptions{
		Path:   tempDir,
		DryRun: true,
	})
	if aErr != nil {
		res.Error = fmt.Sprintf("adoption simulation error: %v", aErr)
		res.DurationMs = time.Since(startTime).Milliseconds()
		return res, nil
	}

	scanRes, sErr := hiss.Scan(ctx, tempDir, hiss.ScanOptions{})
	if sErr == nil && scanRes != nil {
		res.HISSInfractions = scanRes.TotalInfractions
	}

	if plan != nil {
		res.Archetype = plan.Archetype
		res.DebtCount = plan.LegacyDebtCount
		res.SimulatedActions = len(plan.CreatedFiles) + len(plan.ReconciledFiles)
	}

	res.ReadinessGrade = calculateReadinessGrade(res.DebtCount, res.HISSInfractions)
	res.Passed = res.Error == ""
	res.DurationMs = time.Since(startTime).Milliseconds()
	return res, nil
}

// testRemoteAdoptions runs dogfooding simulation across a list of remote public repositories.
func testRemoteAdoptions(ctx context.Context, remoteURLs []string, report *DogfoodReport) error {
	limit := len(remoteURLs)
	if limit > MaxRemoteTargets {
		limit = MaxRemoteTargets
	}

	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during remote adoption dogfooding: %w", err)
		}

		res, err := testSingleRemoteAdoption(ctx, remoteURLs[i])
		if err != nil {
			return fmt.Errorf("remote adoption failed on %s: %w", remoteURLs[i], err)
		}
		if res != nil {
			report.RemoteResults = append(report.RemoteResults, *res)
		}
	}
	return nil
}
