package dedupe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const CadenceFileName = "cadence.json"

// CadenceState records the commit and timestamp of the last audit.
type CadenceState struct {
	LastCommitSHA   string    `json:"last_commit_sha"`
	LastCommitCount int       `json:"last_commit_count"`
	LastRunAt       time.Time `json:"last_run_at"`
	Threshold       int       `json:"threshold"`
}

// CheckCadence verifies if a deduplication sweep should be triggered based on commit delta.
func CheckCadence(ctx context.Context, repoPath string, threshold int) (shouldRun bool, commitsSince int, err error) {
	if threshold <= 0 {
		threshold = 20
	}

	currCount, err := getCommitCount(ctx, repoPath)
	if err != nil {
		// If git repo has no commits yet, don't run
		return false, 0, nil
	}

	state, err := loadCadence(repoPath)
	if err != nil || state == nil {
		// Never run before, should run if there are commits
		return currCount > 0, currCount, nil
	}

	delta := currCount - state.LastCommitCount
	if delta < 0 {
		delta = currCount
	}

	return delta >= threshold, delta, nil
}

// RecordCadence updates the cadence state with the current commit count and HEAD SHA.
func RecordCadence(ctx context.Context, repoPath string, threshold int) error {
	if threshold <= 0 {
		threshold = 20
	}

	sha, err := util.RunGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	count, err := getCommitCount(ctx, repoPath)
	if err != nil {
		return err
	}

	state := &CadenceState{
		LastCommitSHA:   sha,
		LastCommitCount: count,
		LastRunAt:       time.Now().UTC(),
		Threshold:       threshold,
	}

	return saveCadence(repoPath, state)
}

func getCommitCount(ctx context.Context, repoPath string) (int, error) {
	out, err := util.RunGit(ctx, repoPath, "rev-list", "--count", "HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func loadCadence(repoPath string) (*CadenceState, error) {
	cadencePath := filepath.Join(repoPath, ".workingdir", CadenceFileName)
	data, err := os.ReadFile(cadencePath)
	if err != nil {
		return nil, err
	}
	var state CadenceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveCadence(repoPath string, state *CadenceState) error {
	wDir := filepath.Join(repoPath, ".workingdir")
	if err := os.MkdirAll(wDir, 0755); err != nil {
		return err
	}
	cadencePath := filepath.Join(wDir, CadenceFileName)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cadencePath, data, 0644)
}
