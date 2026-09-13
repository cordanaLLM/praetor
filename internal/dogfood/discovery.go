package dogfood

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

const (
	MaxDiscoveryRepositories = 128
	MaxDiscoveryWorkers      = 4
	MaxDiscoveryDuration     = 15 * time.Minute
)

// DiscoveryOptions selects either explicit local input or pinned public inputs.
// Observation never executes source commands or changes the observed source.
type DiscoveryOptions struct {
	ConfigPath  string `json:"config_path,omitempty"`
	Path        string `json:"path,omitempty"`
	PolicyPath  string `json:"policy_path"`
	ArtifactDir string `json:"artifact_dir"`
	Stage       string `json:"stage"`
	Concurrency int    `json:"concurrency"`
	AllowRemote bool   `json:"allow_remote"`
}

type DiscoveryCase struct {
	ID           string               `json:"id"`
	Repository   string               `json:"repository"`
	RequestedSHA string               `json:"requested_sha,omitempty"`
	SourceSHA    string               `json:"source_sha,omitempty"`
	TreeSHA256   string               `json:"tree_sha256,omitempty"`
	Status       string               `json:"status"`
	ReportPath   string               `json:"report_path,omitempty"`
	Error        string               `json:"error,omitempty"`
	Discovery    *CapabilityDiscovery `json:"-"`
}

type DiscoveryOccurrence struct {
	CaseID        string `json:"case_id"`
	Repository    string `json:"repository"`
	SourceSHA     string `json:"source_sha,omitempty"`
	TreeSHA256    string `json:"tree_sha256"`
	ReportPath    string `json:"report_path"`
	EvidenceCount int    `json:"evidence_count"`
}

// DiscoveryCandidate is a review input for Praetor, never an upstream bug or
// permission to dispatch a repair. Recurrence ranks demand, not correctness.
type DiscoveryCandidate struct {
	Key             string                `json:"key"`
	Title           string                `json:"title"`
	Kind            string                `json:"kind"`
	Status          string                `json:"status"`
	RepositoryCount int                   `json:"repository_count"`
	Occurrences     []DiscoveryOccurrence `json:"occurrences"`
}

type DiscoveryReport struct {
	Version      int                  `json:"version"`
	Options      DiscoveryOptions     `json:"options"`
	ConfigSHA256 string               `json:"config_sha256,omitempty"`
	PolicySHA256 string               `json:"policy_sha256"`
	Engine       map[string]string    `json:"engine_build"`
	StartedAt    time.Time            `json:"started_at"`
	FinishedAt   time.Time            `json:"finished_at,omitempty"`
	Status       string               `json:"status"`
	Complete     bool                 `json:"complete"`
	Verified     bool                 `json:"verified"`
	Cases        []DiscoveryCase      `json:"cases"`
	Candidates   []DiscoveryCandidate `json:"candidates"`
	Scope        string               `json:"scope"`
}

// RunDiscovery retains a finite observation run. Complete covers the selected
// rules and inputs only; it does not certify product or application readiness.
func RunDiscovery(ctx context.Context, opts DiscoveryOptions) (*DiscoveryReport, error) {
	if ctx == nil {
		return nil, errors.New("discovery requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDiscoveryDuration)
	defer cancel()
	policy, policySum, cases, configSum, err := prepareDiscovery(ctx, &opts)
	if err != nil {
		return nil, err
	}
	opts.ArtifactDir, err = createSuiteRun(ctx, opts.ArtifactDir)
	if err != nil {
		return nil, err
	}
	report := &DiscoveryReport{Version: 1, Options: opts, ConfigSHA256: configSum,
		PolicySHA256: policySum, Engine: suiteEngine(), StartedAt: time.Now().UTC(),
		Status: "planned", Cases: cases, Candidates: []DiscoveryCandidate{},
		Scope: "Selected Praetor capability availability from immutable file evidence and current adapters; candidates require review and replay. No upstream commands, application verification, agent dispatch or publication."}
	if err := savePublicJSON(filepath.Join(opts.ArtifactDir, "plan.json"), report); err != nil {
		return report, err
	}
	if opts.Stage == "observe" {
		err = executeDiscovery(ctx, report, policy)
	}
	report.FinishedAt = time.Now().UTC()
	persistErr := savePublicJSON(filepath.Join(opts.ArtifactDir, "report.json"), report)
	if persistErr != nil {
		report.Complete, report.Status = false, "failed"
	}
	return report, errors.Join(err, persistErr)
}

func executeDiscovery(ctx context.Context, report *DiscoveryReport, policy DiscoveryPolicy) error {
	var workers sync.WaitGroup
	for worker := 0; worker < report.Options.Concurrency; worker++ {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()
			for index := offset; index < len(report.Cases) && index < MaxDiscoveryRepositories; index += report.Options.Concurrency {
				runDiscoveryCase(ctx, report.Options, policy, &report.Cases[index])
			}
		}(worker)
	}
	workers.Wait()
	report.Candidates = aggregateDiscovery(report.Cases)
	var failures []error
	for i := 0; i < len(report.Cases) && i < MaxDiscoveryRepositories; i++ {
		item := &report.Cases[i]
		if item.Status != "observed" {
			failures = append(failures, fmt.Errorf("%s: %s", item.ID, item.Error))
		}
	}
	report.Complete, report.Status = len(failures) == 0, "observed"
	if !report.Complete {
		report.Status = "partial"
	}
	return errors.Join(failures...)
}
