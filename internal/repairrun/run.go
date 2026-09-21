package repairrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

// MaxDuration bounds one execution, including reproduction, provider and verification.
const MaxDuration = 5 * time.Minute

// JobStatus reports durable admission without opening report-linked sources.
type JobStatus struct {
	CaseID       string `json:"case_id"`
	ExecutionKey string `json:"execution_key"`
	Status       string `json:"status"`
}

// Report describes a scoped candidate; private original corpus is never supplied to it.
type Report struct {
	Version           int         `json:"version"`
	Status            string      `json:"status"`
	ExecutionKey      string      `json:"execution_key"`
	SourceSHA         string      `json:"source_sha"`
	ConfigSHA256      string      `json:"config_sha256"`
	CaseID            string      `json:"case_id"`
	JobID             string      `json:"job_id"`
	StateDir          string      `json:"state_dir"`
	AttemptDir        string      `json:"attempt_dir"`
	Consumed          bool        `json:"consumed"`
	CandidateVerified bool        `json:"candidate_verified"`
	Baseline          *TestResult `json:"baseline,omitempty"`
	Candidate         *TestResult `json:"candidate,omitempty"`
	ChangedFiles      []string    `json:"changed_files"`
	Usage             *Usage      `json:"usage,omitempty"`
	ActualModel       string      `json:"actual_model"`
	ErrorCategory     string      `json:"error_category"`
	Jobs              []JobStatus `json:"jobs"`
	// Register fields preserve both manifest resolutions used by this execution.
	Register               string `json:"register,omitempty"`
	RegisterSource         string `json:"register_source,omitempty"`
	MaxOutputTokens        int    `json:"max_output_tokens,omitempty"`
	PromptRegister         string `json:"prompt_register,omitempty"`
	PromptRegisterSource   string `json:"prompt_register_source,omitempty"`
	RegisterManifestSHA256 string `json:"register_manifest_sha256"`
	// Runtime validation records prove every engine-owned segment was checked. Resolution
	// metadata alone never implies that emitted text followed it.
	JobInstructionsValidation     *config.EmissionValidation `json:"job_instructions_validation,omitempty"`
	PromptValidation              *config.EmissionValidation `json:"prompt_validation,omitempty"`
	RequestInstructionsValidation *config.EmissionValidation `json:"request_instructions_validation,omitempty"`
	SummaryValidation             *config.EmissionValidation `json:"summary_validation,omitempty"`
}

type generator func(context.Context, ProviderConfig, string) (*Proposal, error)
type verifier func(context.Context, Config, string) (*TestResult, []byte, error)
type admission struct {
	settings *configuration
	plan     *dogfood.RepairPlan
	report   *Report
	selected *dogfood.RepairJob
}

// Run executes at most one previously unattempted eligible case from a retained report.
func Run(ctx context.Context, configPath, reportPath string) (*Report, error) {
	return run(ctx, configPath, reportPath, Generate, verifyWorkspace)
}

// Status reads config, retained report and local admission state without writes or dispatch.
func Status(ctx context.Context, configPath, reportPath string) (*Report, error) {
	return StatusWithinRoot(ctx, configPath, reportPath, "")
}

// StatusWithinRoot confines consumed paths before reading the same configuration snapshot.
func StatusWithinRoot(ctx context.Context, configPath, reportPath, inputRoot string) (_ *Report, err error) {
	a, err := prepare(ctx, configPath, reportPath, inputRoot)
	if err != nil {
		return nil, err
	}
	root, err := openState(a.settings.config.StateDir, false)
	if errors.Is(err, os.ErrNotExist) {
		return a.report, a.selectJob(ctx, nil)
	}
	if err != nil {
		return a.report, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, busy, err := lockState(root, false)
	if errors.Is(err, os.ErrNotExist) {
		return a.report, errors.New("existing repair state lacks its lock")
	}
	if err != nil {
		return a.report, err
	}
	if busy {
		a.report.Status = "busy"
		return a.report, nil
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	return a.report, a.selectJob(ctx, root)
}

func run(ctx context.Context, configPath, reportPath string, generate generator, verify verifier) (_ *Report, err error) {
	a, err := prepare(ctx, configPath, reportPath, "")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.settings.config.TimeoutSeconds)*time.Second)
	defer cancel()
	root, err := openState(a.settings.config.StateDir, true)
	if err != nil {
		return a.report, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, busy, err := lockState(root, true)
	if err != nil {
		return a.report, err
	}
	if busy {
		a.report.Status = "busy"
		return a.report, nil
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if err := a.selectJob(ctx, root); err != nil {
		return a.report, err
	}
	if a.report.Status != "ready" {
		return a.report, nil
	}
	if err := checkStateCapacity(root); err != nil {
		a.report.Status = "resource_blocked"
		return a.report, err
	}
	return a.report, a.execute(ctx, root, generate, verify)
}

func prepare(ctx context.Context, configPath, reportPath, boundary string) (*admission, error) {
	if ctx == nil {
		return nil, errors.New("repair execution requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings, err := loadConfig(ctx, configPath, boundary)
	if err != nil {
		return nil, err
	}
	if err := confine(boundary, reportPath); err != nil {
		return nil, err
	}
	report, err := dogfood.LoadRepairReport(ctx, reportPath)
	if err != nil {
		return nil, err
	}
	policyHash, err := policyIdentity(ctx, settings.config)
	if err != nil {
		return nil, err
	}
	plan, err := dogfood.PlanRepairs(ctx, report, settings.config.RepairPolicy)
	if err != nil && !errors.Is(err, dogfood.ErrRepairsBlocked) {
		return nil, err
	}
	confirmed, err := policyIdentity(ctx, settings.config)
	if err != nil || confirmed != policyHash {
		return nil, errors.New("routing inputs changed while preparing execution")
	}
	settings.digest = bytesSHA([]byte(settings.digest + policyHash))
	result := &Report{Version: 1, Status: "blocked", SourceSHA: settings.config.SourceSHA, ConfigSHA256: settings.digest, StateDir: settings.config.StateDir, ChangedFiles: []string{}, Jobs: []JobStatus{}}
	return &admission{settings: settings, plan: plan, report: result}, nil
}

func policyIdentity(ctx context.Context, cfg Config) (string, error) {
	identity := ""
	for _, path := range []string{cfg.RepairPolicy.RoutingConfig, cfg.RepairPolicy.UsagePath} {
		if path == "" {
			continue
		}
		root, err := openDirectory(filepath.Dir(path))
		if err != nil {
			return "", err
		}
		data, readErr := readFile(ctx, root, filepath.Base(path), 1<<20, false)
		if err := errors.Join(readErr, root.Close()); err != nil {
			return "", err
		}
		identity += bytesSHA(data)
	}
	return bytesSHA([]byte(identity)), nil
}
