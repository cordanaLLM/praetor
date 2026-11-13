// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// Replay bounds enforcing HISS-02 scalar loop and I/O limits.
const (
	// MaxCommitScan bounds PR ranges, candidate windows and merged repair ranges.
	MaxCommitScan = 256
	// MaxDiffLines bounds added plus removed lines in a pull-request or candidate fix diff.
	MaxDiffLines = 2000
	// MaxArms bounds review arms evaluated in one replay run.
	MaxArms = 16
	// MaxDrafts bounds review draft files passed to one sample.
	MaxDrafts = 32
	// MaxFindings bounds findings decoded from one arm verdict payload.
	MaxFindings = 256
	// MinCommitPrefix is the shortest hexadecimal commit prefix matched in fix messages.
	MinCommitPrefix = 7
	// DefaultArmTimeout bounds one isolated arm run, including snapshot creation.
	DefaultArmTimeout = 30 * time.Second
	// DefaultPreparationTimeout bounds each sample preparation including history and snapshots.
	DefaultPreparationTimeout = time.Minute
	// DefaultSuite names the replay suite when the caller omits one.
	DefaultSuite = "replay"
	// TreePlaceholder expands to the cutoff worktree path inside an arm command template.
	TreePlaceholder = "{tree}"
	// DraftsPlaceholder expands to the ordered draft file paths inside an arm command template.
	DraftsPlaceholder = "{drafts}"
	// VerdictChangesRequested is the canonical verdict reporting at least one defect.
	VerdictChangesRequested = "changes_requested"
	// VerdictApprove is the canonical verdict reporting no blocking defects.
	VerdictApprove = "approve"

	maxGitProbeBytes  = 4 << 20
	maxArmOutputBytes = 1 << 20
	maxCommandArgs    = 64
	maxCommandBytes   = 4096
	maxScanLines      = 16384
)

// Sentinel errors classifying refused replays (exit status 1 in the CLI).
var (
	// ErrRefused marks any replay refused by cutoff, leak, ground-truth, or bound checks.
	ErrRefused = errors.New("replay refused")
	// ErrLeak marks a cutoff tree whose HEAD or ancestor history postdates the cutoff.
	ErrLeak = errors.New("commit after cutoff")
	// ErrNoGroundTruth marks a sample with no post-merge commits or no matched repair commits.
	ErrNoGroundTruth = errors.New("no ground truth")
	// ErrLimitExceeded marks a history or diff exceeding MaxCommitScan or MaxDiffLines.
	ErrLimitExceeded = errors.New("replay bound exceeded")
)

// refusedf builds an error matching both ErrRefused and kind.
func refusedf(kind error, format string, args ...any) error {
	prefix := []any{ErrRefused, kind}
	return fmt.Errorf("%w (%w): "+format, append(prefix, args...)...)
}

// PRSpec identifies a merged pull request on a base branch by number and merge commit or range.
type PRSpec struct {
	Number int    `json:"number"`
	Merge  string `json:"merge"`
	Base   string `json:"base,omitempty"`
}

// FixHunk is one contiguous line span touched by a ground-truth repair commit.
type FixHunk struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// FixCommit records a later commit on base matched as repairing the replayed pull request.
type FixCommit struct {
	Commit string    `json:"commit"`
	Reason string    `json:"reason"`
	Hunks  []FixHunk `json:"hunks,omitempty"`
}

// GroundTruthResult summarizes post-merge repair commits and fix hunks for one pull request.
type GroundTruthResult struct {
	MergeCommit  string      `json:"merge_commit"`
	PRCommits    []string    `json:"pr_commits"`
	LaterCommits int         `json:"later_commits"`
	Fixes        []FixCommit `json:"fixes"`
	Hunks        []FixHunk   `json:"hunks"`
}

// Finding is one defect pointer emitted by a review arm in its JSON output.
type Finding struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	EndLine  int    `json:"end_line,omitempty"`
	Location string `json:"location,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

// ArmVerdict is the minimal JSON payload a review arm writes to standard output.
type ArmVerdict struct {
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings,omitempty"`
}

// Arm configures one named review or combiner command template.
type Arm struct {
	Name    string        `json:"name"`
	Command []string      `json:"command"`
	Timeout time.Duration `json:"-"`
}

// Sample describes one historical pull request replay at a cutoff timestamp.
type Sample struct {
	PR      PRSpec    `json:"pr"`
	Cutoff  time.Time `json:"cutoff"`
	Until   time.Time `json:"until"`
	Drafts  []string  `json:"drafts,omitempty"`
	TreeDir string    `json:"-"`
	// AddedVerified annotates additional true findings independently verified by the dataset author.
	// Nil means unmeasured; an explicit empty array measures zero. No absence implies fabrication.
	AddedVerified []Finding `json:"added_verified"`
	// Fabrications annotates independently disproven finding locations; nil means unmeasured.
	Fabrications []Finding `json:"fabrications"`
}

// ArmScore holds aggregated replay metrics for one arm across all replayed samples.
type ArmScore struct {
	Arm                   string        `json:"arm"`
	Suite                 string        `json:"suite"`
	Cutoff                time.Time     `json:"cutoff,omitzero"`
	RetainedDefects       int           `json:"retained_defects"`
	TotalDefects          int           `json:"total_defects"`
	VerdictMatch          bool          `json:"verdict_match"`
	OrderSensitive        bool          `json:"order_sensitive"`
	ForwardVerdict        string        `json:"forward_verdict,omitempty"`
	ReverseVerdict        string        `json:"reverse_verdict,omitempty"`
	SampleCount           int           `json:"sample_count"`
	DurationMS            int64         `json:"duration_ms"`
	Samples               []SampleScore `json:"samples"`
	AddedVerifiedFindings *int          `json:"added_verified_findings"`
	Fabrication           *int          `json:"fabrication"`
}

// SampleScore preserves cutoff, verdict and annotated metric metadata for one sample.
type SampleScore struct {
	Cutoff                time.Time `json:"cutoff"`
	CutoffCommit          string    `json:"cutoff_commit"`
	MergeCommit           string    `json:"merge_commit"`
	ForwardVerdict        string    `json:"forward_verdict"`
	ReverseVerdict        string    `json:"reverse_verdict"`
	RetainedDefects       int       `json:"retained_defects"`
	TotalDefects          int       `json:"total_defects"`
	AddedVerifiedFindings *int      `json:"added_verified_findings"`
	Fabrication           *int      `json:"fabrication"`
}

// Options configures a Replay run across one or more samples and arms.
type Options struct {
	RepoDir    string
	Base       string
	Suite      string
	OutcomeLog string
	Samples    []Sample
	Arms       []Arm
}

// Report contains the ground-truth derivations, arm scores, and written outcome rows of Replay.
type Report struct {
	Suite        string              `json:"suite"`
	Cutoff       time.Time           `json:"cutoff,omitzero"`
	CutoffCommit string              `json:"cutoff_commit,omitempty"`
	SampleCount  int                 `json:"sample_count"`
	GroundTruth  []GroundTruthResult `json:"ground_truth"`
	Arms         []ArmScore          `json:"arms"`
	Outcomes     []router.Outcome    `json:"outcomes,omitempty"`
}
