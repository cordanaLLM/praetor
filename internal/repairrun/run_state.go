package repairrun

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/util"
)

var executionSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

type started struct {
	Version      int       `json:"version"`
	ExecutionKey string    `json:"execution_key"`
	SourceSHA    string    `json:"source_sha"`
	ConfigSHA256 string    `json:"config_sha256"`
	StartedAt    time.Time `json:"started_at"`
}

type terminalValidationInputs struct {
	job, prompt, request string
	summary              *string
}

func openState(path string, create bool) (*os.Root, error) {
	parent, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if create {
		err := parent.Mkdir(filepath.Base(path), 0o700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, errors.Join(err, parent.Close())
		}
	}
	root, openErr := openChild(parent, filepath.Base(path))
	if err := errors.Join(openErr, parent.Close()); err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	statePrivate, stateUnverifiable := util.ArtefactPrivacy(info)
	util.NotePrivacyLimitation(stateUnverifiable)
	if err != nil || !statePrivate {
		return nil, errors.Join(errors.New("repair state must be a private directory"), err, root.Close())
	}
	return root, nil
}

func (a *admission) selectJob(ctx context.Context, root *os.Root) error {
	eligible := false
	for index := 0; index < len(a.plan.Jobs) && index < 8; index++ {
		job := &a.plan.Jobs[index]
		key := bytesSHA([]byte(a.settings.config.SourceSHA + ":" + a.settings.digest + ":" + job.UntrustedEvidence.Kind + ":" + job.UntrustedEvidence.CaseID + ":" + job.UntrustedEvidence.InputSHA256))
		state := JobStatus{CaseID: job.UntrustedEvidence.CaseID, ExecutionKey: key, Status: "blocked"}
		if job.Status == "review_required" && job.Route != nil && job.Route.Model.ID == a.settings.config.Provider.Model {
			eligible = true
			status, err := consumedStatus(ctx, root, key, *job)
			if err != nil {
				return err
			}
			state.Status = status
			if status == "ready" && a.selected == nil {
				a.choose(job, key)
			}
		}
		a.report.Jobs = append(a.report.Jobs, state)
	}
	if a.selected == nil && eligible {
		a.report.Status, a.report.Consumed = "consumed", true
	}
	return nil
}

func (a *admission) choose(job *dogfood.RepairJob, key string) {
	a.selected = job
	a.report.ExecutionKey, a.report.CaseID, a.report.JobID = key, job.UntrustedEvidence.CaseID, job.ID
	a.report.AttemptDir = filepath.Join(a.settings.config.StateDir, key)
	a.report.Register, a.report.RegisterSource = job.Register, job.RegisterSource
	a.report.MaxOutputTokens = job.MaxOutputTokens
	a.report.PromptRegister, a.report.PromptRegisterSource = job.PromptRegister, job.PromptRegisterSource
	a.report.RegisterManifestSHA256 = job.RegisterManifestSHA256
	a.report.JobInstructionsValidation = job.InstructionsValidation
	a.report.Status = "ready"
}

func consumedStatus(ctx context.Context, root *os.Root, key string, expected dogfood.RepairJob) (_ string, err error) {
	if root == nil {
		return "ready", nil
	}
	attempt, err := openChild(root, key)
	if errors.Is(err, os.ErrNotExist) {
		return "ready", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, attempt.Close()) }()
	data, err := readFile(ctx, attempt, "started.json", maxConfigBytes, true)
	if err != nil {
		return "", errors.New("repair attempt lacks valid durable start state")
	}
	var begin started
	if err := decodeConfigJSON(data, &begin); err != nil {
		return "", err
	}
	if !validStart(begin, key) {
		return "", errors.New("invalid repair start identity")
	}
	return readTerminal(ctx, attempt, begin, expected)
}

func validStart(begin started, key string) bool {
	return begin.Version == 1 && begin.ExecutionKey == key && sourceSHA.MatchString(begin.SourceSHA) && executionSHA.MatchString(begin.ConfigSHA256) && !begin.StartedAt.IsZero()
}

func readTerminal(ctx context.Context, attempt *os.Root, begin started, expected dogfood.RepairJob) (string, error) {
	data, err := readFile(ctx, attempt, "result.json", 1<<20, true)
	if errors.Is(err, os.ErrNotExist) {
		return "interrupted", nil
	}
	if err != nil {
		return "", err
	}
	var result Report
	if err := decodeExactJSON(data, &result, true); err != nil {
		return "", err
	}
	inputs, err := readTerminalValidationInputs(ctx, attempt, expected, result)
	if err != nil {
		return "", err
	}
	if !validTerminal(result, begin, expected, inputs) {
		return "", errors.New("invalid repair terminal outcome")
	}
	return result.Status, nil
}

func readTerminalValidationInputs(ctx context.Context, attempt *os.Root, job dogfood.RepairJob, result Report) (terminalValidationInputs, error) {
	inputs := terminalInputsForJob(job)
	data, err := readFile(ctx, attempt, "proposal.json", providerResponseLimit, true)
	if errors.Is(err, os.ErrNotExist) && result.SummaryValidation == nil {
		return inputs, nil
	}
	if err != nil {
		return inputs, errors.Join(errors.New("repair terminal validation lacks its proposal input"), err)
	}
	var proposal Proposal
	if err := decodeExactJSON(data, &proposal, true); err != nil {
		return inputs, err
	}
	inputs.summary = &proposal.Summary
	return inputs, nil
}

func terminalInputsForJob(job dogfood.RepairJob) terminalValidationInputs {
	return terminalValidationInputs{job: job.Instructions, prompt: repairPromptPrefix(job),
		request: providerInstructions(config.TextRegister(job.PromptRegister))}
}

func validTerminal(result Report, begin started, expected dogfood.RepairJob, inputs terminalValidationInputs) bool {
	if !sameAttempt(result, begin) || !validRegisterProof(result, expected, inputs) {
		return false
	}
	switch result.Status {
	case "not_reproduced", "agent_failed", "change_rejected", "verification_failed", "failed", "timeout":
		return !result.CandidateVerified
	case "scoped_test_verified":
		return validVerifiedResult(result)
	default:
		return false
	}
}

func validRegisterProof(result Report, expected dogfood.RepairJob, inputs terminalValidationInputs) bool {
	if !sameRegisterResolution(result, expected) {
		return false
	}
	task := config.Resolution{Register: config.TextRegister(result.Register), MaxTokens: result.MaxOutputTokens,
		Source: result.RegisterSource, ManifestSHA256: result.RegisterManifestSHA256}
	prompt := config.Resolution{Register: config.TextRegister(result.PromptRegister), Source: result.PromptRegisterSource,
		ManifestSHA256: result.RegisterManifestSHA256}
	if !validationMatches(result.JobInstructionsValidation, task, config.SurfaceAgent, caveman.KindBrief, inputs.job, false) {
		return false
	}
	return validStageProof(result, task, prompt, inputs)
}

func sameRegisterResolution(result Report, expected dogfood.RepairJob) bool {
	return result.Register == expected.Register && result.RegisterSource == expected.RegisterSource &&
		result.MaxOutputTokens == expected.MaxOutputTokens && result.PromptRegister == expected.PromptRegister &&
		result.PromptRegisterSource == expected.PromptRegisterSource &&
		result.RegisterManifestSHA256 == expected.RegisterManifestSHA256
}

func validStageProof(result Report, task, prompt config.Resolution, inputs terminalValidationInputs) bool {
	switch result.Status {
	case "not_reproduced":
		return inputs.summary == nil && noProviderValidation(result)
	case "agent_failed":
		return inputs.summary == nil && validAgentFailureProof(result, prompt, inputs)
	case "change_rejected":
		return validChangeRejectedProof(result, task, prompt, inputs)
	case "verification_failed", "scoped_test_verified":
		return validCompleteProof(result, task, prompt, inputs)
	case "failed", "timeout":
		return validProofPrefix(result, task, prompt, inputs)
	default:
		return false
	}
}

func noProviderValidation(result Report) bool {
	return result.PromptValidation == nil && result.RequestInstructionsValidation == nil && result.SummaryValidation == nil
}

func validAgentFailureProof(result Report, prompt config.Resolution, inputs terminalValidationInputs) bool {
	return validPromptProof(result, prompt, inputs) && result.SummaryValidation == nil
}

func validCompleteProof(result Report, task, prompt config.Resolution, inputs terminalValidationInputs) bool {
	return validPromptProof(result, prompt, inputs) && validSummaryProof(result, task, inputs, false)
}

func validPromptProof(result Report, prompt config.Resolution, inputs terminalValidationInputs) bool {
	return validationMatches(result.PromptValidation, prompt, config.SurfacePrompts, caveman.KindBrief, inputs.prompt, false) &&
		validationMatches(result.RequestInstructionsValidation, prompt, config.SurfacePrompts, caveman.KindMessage, inputs.request, false)
}

func validChangeRejectedProof(result Report, task, prompt config.Resolution, inputs terminalValidationInputs) bool {
	if !validPromptProof(result, prompt, inputs) {
		return false
	}
	allowFail := result.ErrorCategory == "summary_register_contract"
	if result.ErrorCategory != "candidate_contract" && !allowFail {
		return false
	}
	return validSummaryProof(result, task, inputs, allowFail) &&
		(result.SummaryValidation.Status == config.EmissionFail) == allowFail
}

func validSummaryProof(result Report, task config.Resolution, inputs terminalValidationInputs, allowFail bool) bool {
	return inputs.summary != nil &&
		validationMatches(result.SummaryValidation, task, config.SurfaceAgent, caveman.KindReturn, *inputs.summary, allowFail)
}

func validProofPrefix(result Report, task, prompt config.Resolution, inputs terminalValidationInputs) bool {
	if inputs.summary != nil && result.SummaryValidation == nil {
		return false
	}
	records := []struct {
		validation *config.EmissionValidation
		resolution config.Resolution
		surface    config.RegisterSurface
		kind       caveman.MessageKind
		text       string
		available  bool
	}{{result.PromptValidation, prompt, config.SurfacePrompts, caveman.KindBrief, inputs.prompt, true},
		{result.RequestInstructionsValidation, prompt, config.SurfacePrompts, caveman.KindMessage, inputs.request, true},
		{result.SummaryValidation, task, config.SurfaceAgent, caveman.KindReturn, summaryText(inputs), inputs.summary != nil}}
	missing, failed := false, false
	for index := 0; index < len(records) && index < 3; index++ {
		record := records[index]
		if record.validation == nil {
			missing = true
			continue
		}
		if missing || failed || !record.available ||
			!validationMatches(record.validation, record.resolution, record.surface, record.kind, record.text, true) {
			return false
		}
		failed = record.validation.Status == config.EmissionFail
	}
	return true
}

func summaryText(inputs terminalValidationInputs) string {
	if inputs.summary == nil {
		return ""
	}
	return *inputs.summary
}

func validationMatches(record *config.EmissionValidation, resolution config.Resolution, surface config.RegisterSurface, kind caveman.MessageKind, text string, allowFail bool) bool {
	if record == nil {
		return false
	}
	expected, err := config.ValidateEmission(resolution, surface, kind, text)
	if err != nil && expected.Status != config.EmissionFail {
		return false
	}
	if *record != expected {
		return false
	}
	return expected.Status != config.EmissionFail || allowFail
}

func sameAttempt(r Report, s started) bool {
	return r.Version == 1 && r.Consumed && r.ExecutionKey == s.ExecutionKey && r.SourceSHA == s.SourceSHA && r.ConfigSHA256 == s.ConfigSHA256
}

func validVerifiedResult(r Report) bool {
	return r.CandidateVerified && r.Candidate != nil && r.Candidate.Passed && r.Baseline != nil && !r.Baseline.Passed && len(r.ChangedFiles) > 0 && verifiedTestTransition(r.Baseline.Tests, r.Candidate.Tests)
}

func verifiedTestTransition(before, after []TestOutcome) bool {
	return containsFailedTest(before) && sameTestsPassed(before, after)
}

func checkStateCapacity(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(34)
	if err := errors.Join(readErr, directory.Close()); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 32 {
		return errors.New("repair state reached its 32 retained-attempt admission limit")
	}
	for _, entry := range entries {
		if entry.Name() == "execution.lock" {
			continue
		}
		if !entry.IsDir() || !executionSHA.MatchString(entry.Name()) {
			return errors.New("unknown repair state entry requires review")
		}
	}
	return nil
}
