package repairrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

type execution struct {
	admission *admission
	root      *os.Root
	candidate *os.Root
	baseline  sourceManifest
	generate  generator
	verify    verifier
}

func (a *admission) execute(ctx context.Context, state *os.Root, generate generator, verify verifier) (err error) {
	if err := state.Mkdir(a.report.ExecutionKey, 0o700); err != nil {
		return err
	}
	root, err := openChild(state, a.report.ExecutionKey)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	a.report.Status, a.report.Consumed = "running", true
	begin := started{Version: 1, ExecutionKey: a.report.ExecutionKey, SourceSHA: a.report.SourceSHA, ConfigSHA256: a.report.ConfigSHA256, StartedAt: time.Now().UTC()}
	if err := writeJSON(root, "started.json", begin); err != nil {
		return err
	}
	if err := errors.Join(syncDirectory(root), syncDirectory(state)); err != nil {
		return err
	}
	e := execution{admission: a, root: root, generate: generate, verify: verify}
	runErr := e.work(ctx)
	if runErr != nil && a.report.CandidateVerified {
		a.report.CandidateVerified = false
		a.report.Status = "failed"
	}
	if runErr != nil && a.report.Status == "running" {
		a.report.Status, a.report.ErrorCategory = "failed", "execution_prerequisite"
	}
	if ctx.Err() != nil {
		a.report.Status, a.report.ErrorCategory = "timeout", "execution_deadline"
		a.report.CandidateVerified = false
	}
	if err := writeJSON(root, "result.json", a.report); err != nil {
		a.report.CandidateVerified = false
		a.report.Status = "failed"
		return errors.Join(runErr, err)
	}
	return errors.Join(runErr, syncDirectory(root))
}

func (e *execution) work(ctx context.Context) (err error) {
	a, cfg := e.admission, e.admission.settings.config
	e.baseline, err = materialize(ctx, cfg, e.root, a.report.AttemptDir)
	if err != nil {
		return err
	}
	if err := writeJSON(e.root, "baseline-manifest.json", e.baseline); err != nil {
		return err
	}
	e.candidate, err = openDirectory(filepath.Join(a.report.AttemptDir, "candidate"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, e.candidate.Close()) }()
	baseline, err := e.check(ctx, "baseline")
	a.report.Baseline = baseline
	if err != nil {
		return err
	}
	if baseline.Passed {
		a.report.Status = "not_reproduced"
		return nil
	}
	return e.generateCandidate(ctx)
}

func (e *execution) check(ctx context.Context, name string) (*TestResult, error) {
	cfg, dir := e.admission.settings.config, filepath.Join(e.admission.report.AttemptDir, "candidate")
	result, log, err := e.verify(ctx, cfg, dir)
	if writeErr := writeNew(e.root, name+".log", log); writeErr != nil {
		return result, errors.Join(err, writeErr)
	}
	if err != nil {
		return result, err
	}
	if result == nil {
		return nil, errors.New("verification omitted its outcome")
	}
	return result, writeJSON(e.root, name+".json", result)
}

func (e *execution) generateCandidate(ctx context.Context) error {
	if err := e.requireUnchangedBaseline(ctx); err != nil {
		return err
	}
	prompt, err := e.prepareProviderPrompt(ctx)
	if err != nil {
		return err
	}
	proposal, err := e.requestProposal(ctx, prompt)
	if err != nil {
		return err
	}
	return e.applyAndVerifyProposal(ctx, proposal)
}

func (e *execution) requireUnchangedBaseline(ctx context.Context) error {
	before, err := snapshotCandidate(ctx, e.candidate)
	if err != nil {
		return err
	}
	changes, err := changedFiles(e.baseline, before, nil)
	if err != nil || len(changes) != 0 {
		return errors.New("baseline verification changed source")
	}
	return nil
}

func (e *execution) prepareProviderPrompt(ctx context.Context) (string, error) {
	a, cfg := e.admission, e.admission.settings.config
	prompt, validations, err := buildPrompt(ctx, cfg, e.candidate, *a.selected, a.report.Baseline.Tests)
	a.report.JobInstructionsValidation = validations.Job
	a.report.PromptValidation = validations.Prompt
	if err != nil {
		if validationFailed(validations.Job) || validationFailed(validations.Prompt) {
			a.report.Status, a.report.ErrorCategory = "failed", "prompt_register_contract"
		}
		return "", err
	}
	requestValidation, err := validateProviderRequestInstructions(*a.selected)
	a.report.RequestInstructionsValidation = requestValidation
	if err != nil {
		a.report.Status, a.report.ErrorCategory = "failed", "prompt_register_contract"
		return "", err
	}
	if err := writeNew(e.root, "prompt.json.txt", []byte(prompt)); err != nil {
		return "", err
	}
	return prompt, nil
}

func (e *execution) requestProposal(ctx context.Context, prompt string) (*Proposal, error) {
	a, cfg := e.admission, e.admission.settings.config
	proposal, err := e.generate(ctx, jobProvider(cfg.Provider, a.selected), prompt)
	if err != nil {
		a.report.Status, a.report.ErrorCategory = "agent_failed", "provider_request"
		return nil, errors.New("provider did not produce a candidate")
	}
	if proposal == nil {
		a.report.Status = "agent_failed"
		return nil, errors.New("provider returned no candidate")
	}
	a.report.Usage, a.report.ActualModel = &proposal.Usage, proposal.ActualModel
	summaryValidation, summaryErr := validateProposalSummary(*a.selected, proposal.Summary)
	a.report.SummaryValidation = summaryValidation
	if err := writeJSON(e.root, "proposal.json", proposal); err != nil {
		return nil, err
	}
	if summaryErr != nil {
		a.report.Status, a.report.ErrorCategory = "change_rejected", "summary_register_contract"
		return nil, summaryErr
	}
	return proposal, nil
}

func (e *execution) applyAndVerifyProposal(ctx context.Context, proposal *Proposal) error {
	a, cfg := e.admission, e.admission.settings.config
	patch, err := applyProposal(ctx, cfg, e.candidate, e.baseline, proposal)
	if err != nil {
		a.report.Status, a.report.ErrorCategory = "change_rejected", "candidate_contract"
		return err
	}
	if err := writeNew(e.root, "patch.diff", patch); err != nil {
		return err
	}
	return e.verifyCandidate(ctx)
}

// jobProvider selects provider instructions from the prompt-surface row and applies the
// output budget of the task row. The budget can only lower the configured limit: the run
// configuration is the operator's spend cap, and a manifest row must not raise it. The
// response check reads the same field, so an ignored budget is rejected like any overrun.
func jobProvider(provider ProviderConfig, job *dogfood.RepairJob) ProviderConfig {
	if job != nil {
		provider.runtimePromptRegister = config.TextRegister(job.PromptRegister)
	}
	if job != nil && job.MaxOutputTokens > 0 && job.MaxOutputTokens < provider.MaxOutputTokens {
		provider.MaxOutputTokens = job.MaxOutputTokens
	}
	return provider
}

func validateProposalSummary(job dogfood.RepairJob, summary string) (*config.EmissionValidation, error) {
	resolution := config.Resolution{Register: config.TextRegister(job.Register), MaxTokens: job.MaxOutputTokens,
		Source: job.RegisterSource, ManifestSHA256: job.RegisterManifestSHA256}
	validation, err := config.ValidateEmission(resolution, config.SurfaceAgent, caveman.KindReturn, summary)
	if err != nil {
		return &validation, fmt.Errorf("repair proposal summary: %w", err)
	}
	return &validation, nil
}

func validateProviderRequestInstructions(job dogfood.RepairJob) (*config.EmissionValidation, error) {
	resolution := config.Resolution{Register: config.TextRegister(job.PromptRegister), Source: job.PromptRegisterSource,
		ManifestSHA256: job.RegisterManifestSHA256}
	validation, err := config.ValidateEmission(resolution, config.SurfacePrompts, caveman.KindMessage, providerInstructions(resolution.Register))
	if err != nil {
		return &validation, fmt.Errorf("repair provider instructions: %w", err)
	}
	return &validation, nil
}

func validationFailed(validation *config.EmissionValidation) bool {
	return validation != nil && validation.Status == config.EmissionFail
}

func (e *execution) verifyCandidate(ctx context.Context) error {
	a, cfg := e.admission, e.admission.settings.config
	before, err := snapshotCandidate(ctx, e.candidate)
	if err != nil {
		return err
	}
	a.report.ChangedFiles, err = changedFiles(e.baseline, before, cfg.AllowedFiles)
	if err != nil || len(a.report.ChangedFiles) == 0 {
		return errors.New("candidate does not contain an admissible source change")
	}
	result, err := e.check(ctx, "candidate")
	a.report.Candidate = result
	if err != nil {
		a.report.Status, a.report.ErrorCategory = "verification_failed", "verification_process"
		return err
	}
	after, err := snapshotCandidate(ctx, e.candidate)
	if err != nil {
		return err
	}
	if _, err := changedFiles(before, after, nil); err != nil {
		return err
	}
	if !result.Passed || !sameTestsPassed(a.report.Baseline.Tests, result.Tests) {
		a.report.Status, a.report.ErrorCategory = "verification_failed", "existing_tests_failed"
		return errors.New("candidate did not pass existing scoped tests")
	}
	a.report.Status, a.report.CandidateVerified = "scoped_test_verified", true
	return writeJSON(e.root, "changed-files.json", a.report.ChangedFiles)
}
