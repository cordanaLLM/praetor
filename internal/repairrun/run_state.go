package repairrun

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

var executionSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

type started struct {
	Version      int       `json:"version"`
	ExecutionKey string    `json:"execution_key"`
	SourceSHA    string    `json:"source_sha"`
	ConfigSHA256 string    `json:"config_sha256"`
	StartedAt    time.Time `json:"started_at"`
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
	if err != nil || info.Mode().Perm()&0o077 != 0 {
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
			status, err := consumedStatus(ctx, root, key)
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
	a.report.Status = "ready"
}

func consumedStatus(ctx context.Context, root *os.Root, key string) (_ string, err error) {
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
	return readTerminal(ctx, attempt, begin)
}

func validStart(begin started, key string) bool {
	return begin.Version == 1 && begin.ExecutionKey == key && sourceSHA.MatchString(begin.SourceSHA) && executionSHA.MatchString(begin.ConfigSHA256) && !begin.StartedAt.IsZero()
}

func readTerminal(ctx context.Context, attempt *os.Root, begin started) (string, error) {
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
	if !validTerminal(result, begin) {
		return "", errors.New("invalid repair terminal outcome")
	}
	return result.Status, nil
}

func validTerminal(result Report, begin started) bool {
	if !sameAttempt(result, begin) {
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
