package dedupe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const CadenceFileName = "cadence.json"

const (
	// DefaultCadenceCommits is the commit delta that makes a sweep due.
	DefaultCadenceCommits = 20
	// DefaultCadenceAddedLines and DefaultCadenceAddedFiles make a sweep due on source growth
	// even while the commit delta is below its threshold.
	DefaultCadenceAddedLines = 1000
	DefaultCadenceAddedFiles = 10
)

// CadenceState records the commit and timestamp of the last audit. Growth is measured from
// LastCommitSHA, so a state file written before growth existed needs no migration.
type CadenceState struct {
	LastCommitSHA   string    `json:"last_commit_sha"`
	LastCommitCount int       `json:"last_commit_count"`
	LastRunAt       time.Time `json:"last_run_at"`
	Threshold       int       `json:"threshold"`
}

// CadenceLimits sets when a sweep falls due. A zero or negative field takes its default.
type CadenceLimits struct {
	Commits    int
	AddedLines int
	AddedFiles int
}

// CadenceStatus reports how far the repository moved since the recorded sweep and whether a
// new one is due. Added lines and files count Go production sources, the scope the clone
// detector reads.
type CadenceStatus struct {
	Due          bool
	CommitsSince int
	AddedLines   int
	AddedFiles   int
	// Unmeasured is set when the recorded sweep commit does not resolve to a commit object
	// in this repository (pruned after a rewrite, missing from a shallow clone, or recorded
	// in another clone), so growth since it cannot be counted. The check reads the object
	// store, not reachability: a commit a rebase or amend left unreachable still resolves
	// until garbage collection and is measured as a tree diff against HEAD.
	Unmeasured bool
	// Reason names the trigger that made the sweep due; it is empty when none did.
	Reason string
}

func (l CadenceLimits) withDefaults() CadenceLimits {
	if l.Commits <= 0 {
		l.Commits = DefaultCadenceCommits
	}
	if l.AddedLines <= 0 {
		l.AddedLines = DefaultCadenceAddedLines
	}
	if l.AddedFiles <= 0 {
		l.AddedFiles = DefaultCadenceAddedFiles
	}
	return l
}

// CheckCadence reports whether a deduplication sweep is due.
//
// It used to count commits and nothing else, so a branch adding thousands of lines in a
// handful of commits never made a sweep due: measured on this repository, about 4,600 added
// lines across 66 production files sat below the 20-commit trigger. The Go production lines
// and files added since the recorded sweep are now a second and third trigger.
func CheckCadence(ctx context.Context, repoPath string, limits CadenceLimits) (CadenceStatus, error) {
	limits = limits.withDefaults()
	currCount, err := getCommitCount(ctx, repoPath)
	if err != nil {
		return CadenceStatus{}, fmt.Errorf("read cadence commit count: %w", err)
	}
	state, err := loadCadence(ctx, repoPath)
	if err != nil {
		return CadenceStatus{}, fmt.Errorf("read cadence state: %w", err)
	}
	if state == nil {
		// Never run before, should run if there are commits
		status := CadenceStatus{Due: currCount > 0, CommitsSince: currCount}
		if status.Due {
			status.Reason = "no sweep has been recorded"
		}
		return status, nil
	}
	status := CadenceStatus{CommitsSince: currCount - state.LastCommitCount}
	if status.CommitsSince < 0 {
		status.CommitsSince = currCount
	}
	if err := measureGrowth(ctx, repoPath, state.LastCommitSHA, &status); err != nil {
		return CadenceStatus{}, fmt.Errorf("read cadence source growth: %w", err)
	}
	status.decide(limits)
	return status, nil
}

// decide sets Due and names the first trigger that fired.
func (s *CadenceStatus) decide(limits CadenceLimits) {
	switch {
	case s.CommitsSince >= limits.Commits:
		s.Reason = fmt.Sprintf("%d commits since the recorded sweep (threshold %d)", s.CommitsSince, limits.Commits)
	case s.AddedLines >= limits.AddedLines:
		s.Reason = fmt.Sprintf("%d Go source lines added since the recorded sweep (threshold %d)", s.AddedLines, limits.AddedLines)
	case s.AddedFiles >= limits.AddedFiles:
		s.Reason = fmt.Sprintf("%d Go source files added since the recorded sweep (threshold %d)", s.AddedFiles, limits.AddedFiles)
	case s.Unmeasured:
		s.Reason = "the recorded sweep commit is not in this history, so growth since it cannot be measured"
	}
	s.Due = s.Reason != ""
}

// measureGrowth counts the Go production lines and files added between the recorded sweep
// commit and HEAD.
//
// The recorded commit is read from a local state file, so it is resolved with
// --end-of-options before any diff sees it: a value shaped like an option is refused as a
// revision instead of being parsed as one, and only the resolved object ID reaches the diff.
func measureGrowth(ctx context.Context, repoPath, sinceSHA string, status *CadenceStatus) error {
	since := ""
	if sinceSHA != "" {
		resolved, err := util.RunGit(ctx, repoPath, "rev-parse", "--verify", "--quiet", "--end-of-options", sinceSHA+"^{commit}")
		if err == nil {
			since = resolved
		}
	}
	if since == "" {
		status.Unmeasured = true
		return nil
	}
	numstat, err := util.RunGit(ctx, repoPath, "diff", "--numstat", "-z", "--no-renames", since, "HEAD", "--")
	if err != nil {
		return err
	}
	status.AddedLines = addedSourceLines(numstat)
	added, err := util.RunGit(ctx, repoPath, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=A", since, "HEAD", "--")
	if err != nil {
		return err
	}
	status.AddedFiles = countSourcePaths(added)
	return nil
}

// addedSourceLines sums the added-line column of `git diff --numstat -z` over Go production
// sources. A binary file reports "-" and adds nothing.
func addedSourceLines(numstat string) int {
	total := 0
	for _, record := range strings.Split(numstat, "\x00") {
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 || !util.IsGoNonTestSource(fields[2]) {
			continue
		}
		if added, err := strconv.Atoi(fields[0]); err == nil && added > 0 {
			total += added
		}
	}
	return total
}

// countSourcePaths counts the Go production sources in a NUL-separated path list.
func countSourcePaths(paths string) int {
	count := 0
	for _, path := range strings.Split(paths, "\x00") {
		if util.IsGoNonTestSource(path) {
			count++
		}
	}
	return count
}

// RecordCadence updates the cadence state with the current commit count and HEAD SHA.
func RecordCadence(ctx context.Context, repoPath string, threshold int) error {
	if threshold <= 0 {
		threshold = DefaultCadenceCommits
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
