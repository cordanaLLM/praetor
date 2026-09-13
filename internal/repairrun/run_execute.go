package repairrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
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
	a, cfg := e.admission, e.admission.settings.config
	before, err := snapshotCandidate(ctx, e.candidate)
	if err != nil {
		return err
	}
	changes, err := changedFiles(e.baseline, before, nil)
	if err != nil || len(changes) != 0 {
		return errors.New("baseline verification changed source")
	}
	prompt, err := buildPrompt(ctx, cfg, e.candidate, *a.selected, a.report.Baseline.Tests)
	if err != nil {
		return err
	}
	if err := writeNew(e.root, "prompt.json.txt", []byte(prompt)); err != nil {
		return err
	}
	proposal, err := e.generate(ctx, cfg.Provider, prompt)
	if err != nil {
		a.report.Status, a.report.ErrorCategory = "agent_failed", "provider_request"
		return errors.New("provider did not produce a candidate")
	}
	if proposal == nil {
		a.report.Status = "agent_failed"
		return errors.New("provider returned no candidate")
	}
	a.report.Usage, a.report.ActualModel = &proposal.Usage, proposal.ActualModel
	if err := writeJSON(e.root, "proposal.json", proposal); err != nil {
		return err
	}
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
