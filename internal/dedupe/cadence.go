package dedupe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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
		return false, 0, fmt.Errorf("read cadence commit count: %w", err)
	}

	state, err := loadCadence(ctx, repoPath)
	if err != nil {
		return false, 0, fmt.Errorf("read cadence state: %w", err)
	}
	if state == nil {
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

	return saveCadence(ctx, repoPath, state)
}

func getCommitCount(ctx context.Context, repoPath string) (int, error) {
	out, err := util.RunGit(ctx, repoPath, "rev-list", "--count", "HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func loadCadence(ctx context.Context, repoPath string) (*CadenceState, error) {
	cadencePath := filepath.Join(repoPath, ".workingdir", CadenceFileName)
	data, err := contextopt.ReadSnapshot(ctx, cadencePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state CadenceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveCadence(ctx context.Context, repoPath string, state *CadenceState) error {
	wDir := filepath.Join(repoPath, ".workingdir")
	if err := contextopt.EnsureDirectory(ctx, wDir, 0o700); err != nil {
		return err
	}
	cadencePath := filepath.Join(wDir, CadenceFileName)
	before, err := contextopt.ReadSnapshot(ctx, cadencePath)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return contextopt.ReplaceSnapshot(ctx, cadencePath, data, contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o600})
}
