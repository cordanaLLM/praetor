package dogfood

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"time"
)

// MaxSuiteDuration is a ceiling for the complete case set, including replay.
const MaxSuiteDuration = 15 * time.Minute

// SuiteOptions selects a config snapshot, explicit stage, and new private output.
type SuiteOptions struct {
	ConfigPath  string `json:"config_path"`
	SourceRoot  string `json:"source_root"`
	ArtifactDir string `json:"artifact_dir"`
	Stage       string `json:"stage"`
	// InputRoot confines transcript paths embedded in the config for hosted callers.
	InputRoot   string `json:"input_root,omitempty"`
	AllowRemote bool   `json:"allow_remote"`
}

// SuiteCase retains the requested input and actual bounded results, including errors.
type SuiteCase struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Status     string            `json:"status"`
	Repository string            `json:"repository,omitempty"`
	Transcript *SuiteTranscript  `json:"transcript,omitempty"`
	Public     *PublicLoopReport `json:"public,omitempty"`
	Ingestion  *SuiteReplayPass  `json:"ingestion,omitempty"`
	Replay     *SuiteReplayPass  `json:"replay,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// SuiteReport never equates planned declarations, empty inputs or partial runs
// with verification. Transcript payloads are retained only in private caches.
type SuiteReport struct {
	Version      int               `json:"version"`
	Options      SuiteOptions      `json:"options"`
	ConfigSHA256 string            `json:"config_sha256"`
	Engine       map[string]string `json:"engine_build"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   time.Time         `json:"finished_at,omitempty"`
	Status       string            `json:"status"`
	Verified     bool              `json:"verified"`
	Cases        []SuiteCase       `json:"cases"`
	Scope        string            `json:"scope"`
}

// RunSuite validates every declaration before creating evidence. Plan records
// declarations only; verify executes all cases and a same-cache transcript replay.
// This is a finite invocation, not a scheduler, application test runner or agent dispatcher.
func RunSuite(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
	if ctx == nil {
		return nil, errors.New("suite requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxSuiteDuration)
	defer cancel()
	if opts.Stage != "plan" && opts.Stage != "verify" {
		return nil, errors.New("suite stage must be plan or verify")
	}
	config, sum, err := loadSuiteConfig(ctx, opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := validateSuitePolicy(config, opts); err != nil {
		return nil, err
	}
	opts.ArtifactDir, err = createSuiteRun(ctx, opts.ArtifactDir)
	if err != nil {
		return nil, err
	}
	report := &SuiteReport{Version: 1, Options: opts, ConfigSHA256: sum, Engine: suiteEngine(), StartedAt: time.Now().UTC(), Status: "planned",
		Cases: suiteCases(config), Scope: "Plan validates declarations only. Verify checks Praetor public adoption stability and complete observed-event ingestion/replay; no upstream application tests, verified-fact extraction, provider dispatch or promotion"}
	if err := savePublicJSON(filepath.Join(opts.ArtifactDir, "plan.json"), report); err != nil {
		return report, err
	}
	if opts.Stage == "verify" {
		err = executeSuite(ctx, report)
	}
	return finishSuite(report, err)
}

func finishSuite(report *SuiteReport, runErr error) (*SuiteReport, error) {
	report.FinishedAt = time.Now().UTC()
	persistErr := savePublicJSON(filepath.Join(report.Options.ArtifactDir, "report.json"), report)
	if persistErr != nil {
		report.Status, report.Verified = "failed", false
	}
	return report, errors.Join(runErr, persistErr)
}

func suiteCases(config *SuiteConfig) []SuiteCase {
	cases := make([]SuiteCase, 0, len(config.PublicRepositories)+len(config.Transcripts))
	for i := 0; i < len(config.PublicRepositories); i++ {
		cases = append(cases, SuiteCase{ID: fmt.Sprintf("public-%02d", i+1), Kind: "public", Status: "planned", Repository: config.PublicRepositories[i]})
	}
	for i := 0; i < len(config.Transcripts); i++ {
		source := config.Transcripts[i]
		cases = append(cases, SuiteCase{ID: source.ID, Kind: "transcript", Status: "planned", Transcript: &source})
	}
	return cases
}

func executeSuite(ctx context.Context, report *SuiteReport) error {
	report.Status = "running"
	var failures []error
	for i := 0; i < len(report.Cases) && i < MaxSuiteCases; i++ {
		result := &report.Cases[i]
		caseErr := executeSuiteCase(ctx, report.Options, i, result)
		if caseErr != nil {
			result.Error = caseErr.Error()
			failures = append(failures, fmt.Errorf("case %d (%s): %w", i+1, result.ID, caseErr))
		}
		path := filepath.Join(report.Options.ArtifactDir, fmt.Sprintf("case-%02d.json", i+1))
		if err := savePublicJSON(path, result); err != nil {
			failures = append(failures, err)
		}
	}
	report.Verified = len(failures) == 0
	report.Status = "verified"
	if !report.Verified {
		report.Status = "failed"
	}
	return errors.Join(failures...)
}

func executeSuiteCase(ctx context.Context, opts SuiteOptions, index int, result *SuiteCase) error {
	result.Status = "failed"
	if err := ctx.Err(); err != nil {
		result.Status = "skipped_due_to_context"
		return err
	}
	dir := filepath.Join(opts.ArtifactDir, fmt.Sprintf("case-%02d", index+1))
	var err error
	if result.Kind == "public" {
		result.Public, err = RunPublicLoop(ctx, PublicLoopOptions{Repositories: []string{result.Repository}, SourceRoot: opts.SourceRoot, ArtifactDir: dir, Apply: true, MaxAttempts: 2})
		if err == nil && (result.Public == nil || !result.Public.Verified) {
			err = errors.New("public case did not verify")
		}
	} else {
		err = verifySuiteTranscript(ctx, dir, result)
	}
	if err == nil {
		result.Status = "verified"
	}
	return err
}

func suiteEngine() map[string]string {
	result := map[string]string{"revision": "unavailable"}
	if info, ok := debug.ReadBuildInfo(); ok {
		result["go_version"] = info.GoVersion
		for i := 0; i < len(info.Settings) && i < 64; i++ {
			setting := info.Settings[i]
			if setting.Key == "vcs.revision" {
				result["revision"] = setting.Value
			}
			if setting.Key == "vcs.modified" {
				result["modified"] = setting.Value
			}
		}
	}
	return result
}
