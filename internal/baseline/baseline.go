package baseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// FilePerm is the mode of the written baseline: a tracked artifact reviewers read.
	FilePerm os.FileMode = 0o644
)

var (
	// ErrDebtIncrease reports a snapshot that records more infractions than its
	// predecessor, which HISS-13 forbids unless the increase is recorded deliberately.
	ErrDebtIncrease = errors.New("baseline: total infractions increased")
	// ErrIncreaseRationaleRequired reports an allowed increase without a rationale.
	ErrIncreaseRationaleRequired = errors.New("baseline: an allowed increase requires a non-empty rationale")
	// ErrNilSnapshot reports a nil baseline handed to a comparison.
	ErrNilSnapshot = errors.New("baseline: snapshot must not be nil")
)

// Infraction represents a baselined legacy technical debt violation.
type Infraction struct {
	RuleID      string `json:"rule_id"`
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_number"`
	Symbol      string `json:"symbol,omitempty"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
}

// Baseline represents the recorded legacy debt snapshot (.standards-baseline.json).
type Baseline struct {
	Version          int    `json:"version"`
	GeneratedAt      string `json:"generated_at"`
	Repository       string `json:"repository"`
	CommitSHA        string `json:"commit_sha"`
	TotalInfractions int    `json:"total_infractions"`
	// IncreaseRationale is set only when a record deliberately raised the count with
	// --allow-increase; it is what reviewers and the growth guard see.
	IncreaseRationale string       `json:"increase_rationale,omitempty"`
	Infractions       []Infraction `json:"infractions"`
	// Absent is set by LoadBaseline when no baseline file exists. The empty snapshot it
	// returns then means "nothing recorded", not "zero debt recorded"; it is never
	// serialized, so a saved baseline is always a recorded one.
	Absent bool `json:"-"`
}

// RatchetResult details the evaluation of a commit/PR against the baseline.
type RatchetResult struct {
	PreviousCount          int
	CurrentCount           int
	NewViolations          []Infraction
	TouchedCleanViolations []Infraction
	Passed                 bool
}

// RecordOptions controls how Record treats a snapshot that would raise the count.
type RecordOptions struct {
	// AllowIncrease permits the count to rise; Rationale must then be non-empty.
	AllowIncrease bool
	// Rationale is stored in the baseline when an increase is allowed.
	Rationale string
}

// LoadBaseline reads and parses .standards-baseline.json. A missing file is an empty
// baseline marked Absent; any other read error is returned.
func LoadBaseline(path string) (*Baseline, error) {
	// #nosec G304 -- the baseline path is the operator's own repository file, passed
	// explicitly by the caller; there is no root to confine it to.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Baseline{
				Version:          1,
				GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
				TotalInfractions: 0,
				Infractions:      []Infraction{},
				Absent:           true,
			}, nil
		}
		return nil, fmt.Errorf("failed to read baseline %s: %w", path, err)
	}

	b, err := ParseBaseline(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse baseline %s: %w", path, err)
	}
	return b, nil
}

// ParseBaseline decodes a baseline document, for example one read from a git ref.
func ParseBaseline(data []byte) (*Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("decode baseline: %w", err)
	}
	if b.Infractions == nil {
		b.Infractions = []Infraction{}
	}
	return &b, nil
}

// SaveBaseline writes a baseline to disk. It is the raw writer: it does not enforce the
// HISS-13 ratchet, so callers recording a fresh scan must go through Record first.
func SaveBaseline(path string, b *Baseline) error {
	if b == nil {
		return ErrNilSnapshot
	}
	b.TotalInfractions = len(b.Infractions)
	b.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal baseline: %w", err)
	}

	if err := util.WriteFileSecure(path, append(data, '\n'), FilePerm); err != nil {
		return fmt.Errorf("failed to write baseline to %s: %w", path, err)
	}
	return nil
}

// Count returns the number of infractions a snapshot records, trusting the entries over
// a hand-edited total.
func (b *Baseline) Count() int {
	if b == nil {
		return 0
	}
	if n := len(b.Infractions); n > b.TotalInfractions {
		return n
	}
	return b.TotalInfractions
}

// CheckMonotonic enforces HISS-13 across snapshots: next may not record more
// infractions than previous. It returns ErrDebtIncrease with both counts otherwise.
func CheckMonotonic(previous, next *Baseline) error {
	if previous == nil || next == nil {
		return ErrNilSnapshot
	}
	if next.Count() > previous.Count() {
		return fmt.Errorf("%w: %d -> %d", ErrDebtIncrease, previous.Count(), next.Count())
	}
	return nil
}

// Record builds the snapshot that replaces previous from a fresh scan. It refuses a
// higher count unless opts.AllowIncrease is set together with a rationale, which is then
// stored in the snapshot; a snapshot that does not grow carries no rationale.
func Record(previous *Baseline, infractions []Infraction, opts RecordOptions) (*Baseline, error) {
	if previous == nil {
		previous = &Baseline{Version: 1, Infractions: []Infraction{}}
	}
	next := &Baseline{
		Version:          previous.Version,
		Repository:       previous.Repository,
		CommitSHA:        previous.CommitSHA,
		Infractions:      make([]Infraction, 0, len(infractions)),
		TotalInfractions: len(infractions),
	}
	if next.Version == 0 {
		next.Version = 1
	}
	next.Infractions = append(next.Infractions, infractions...)

	if err := CheckMonotonic(previous, next); err != nil {
		if !opts.AllowIncrease {
			return nil, err
		}
		rationale := strings.TrimSpace(opts.Rationale)
		if rationale == "" {
			return nil, fmt.Errorf("%w (%w)", ErrIncreaseRationaleRequired, err)
		}
		next.IncreaseRationale = rationale
	}
	return next, nil
}

// NormalizePath renders a repository-relative path with forward slashes.
//
// Baseline records are committed and compared across platforms. A scan on Windows writes
// "scripts\\tunnel_hindsight.py" while the same scan on Linux writes
// "scripts/tunnel_hindsight.py", so the same infraction produced two different fingerprints and
// the ratchet silently stopped recognising its own baseline. Measured in cordanaLLM/imago, whose
// committed baseline could not suppress its own recorded infraction on a Linux checkout.
//
// filepath.ToSlash is not enough: it rewrites nothing on Linux, so a baseline written on Windows
// would still fail to match there. The replacement is unconditional in both directions.
//
// The cost is that a path genuinely containing a backslash -- legal on Linux, vanishingly rare,
// and impossible in a Git-tracked path on Windows -- collapses onto the separator form. That
// trades a false match in one pathological filename for a gate that works on every platform.
func NormalizePath(path string) string {
	return util.NormalizeSlashes(path)
}

// EvaluateRatchet enforces monotonic debt reduction and the touched-file clean rule.
// Invariant: V_total(t1) <= V_total(t0) AND touched files must have 0 violations.
// RatchetOptions selects how the touched-file clean rule decides a touched file.
type RatchetOptions struct {
	// DebtDelta judges a touched file on whether its debt grew rather than on whether it was
	// touched. The default is off, deliberately: the touched-file rule is a boy-scout rule --
	// working in a file obliges cleaning it -- and turning that off for everyone would remove
	// the pressure that pays debt down. It exists for changes that are provably mechanical,
	// where that obligation makes the change impossible rather than cleaner: renaming a Go
	// module path edits one import line per file, introduces no infraction, and failed 75
	// files that merely already carried baselined debt (#150).
	DebtDelta bool
}

// EvaluateRatchet applies the HISS-13 ratchet with the default touched-file clean rule.
func EvaluateRatchet(b *Baseline, currentViolations []Infraction, touchedFiles []string) *RatchetResult {
	return EvaluateRatchetWithOptions(b, currentViolations, touchedFiles, RatchetOptions{})
}

// EvaluateRatchetWithOptions applies the HISS-13 ratchet under explicit options.
func EvaluateRatchetWithOptions(b *Baseline, currentViolations []Infraction, touchedFiles []string, opts RatchetOptions) *RatchetResult {
	touchedMap := make(map[string]struct{}, len(touchedFiles))
	for _, f := range touchedFiles {
		touchedMap[NormalizePath(f)] = struct{}{}
	}

	baselinedFingerprints := make(map[string]struct{}, len(b.Infractions))
	for _, inf := range b.Infractions {
		baselinedFingerprints[NormalizePath(inf.Fingerprint)] = struct{}{}
	}

	var newViolations []Infraction
	var touchedCleanViolations []Infraction

	// Under DebtDelta a touched file is judged on whether it got worse; otherwise any touch
	// revokes the file's exemptions, which is the default boy-scout rule.
	revokes := touchedRuleRevokes(b.Infractions, currentViolations, touchedMap, opts)

	for _, curr := range currentViolations {
		file := NormalizePath(curr.FilePath)
		_, isTouched := touchedMap[file]
		_, isBaselined := baselinedFingerprints[NormalizePath(curr.Fingerprint)]

		if isTouched && revokes(file) {
			// Touched-File Clean Rule: the file's baseline exemptions are revoked.
			touchedCleanViolations = append(touchedCleanViolations, curr)
		} else if !isTouched && !isBaselined {
			// Brand new violation in an untouched file
			newViolations = append(newViolations, curr)
		}
	}

	passed := len(newViolations) == 0 && len(touchedCleanViolations) == 0 && len(currentViolations) <= b.TotalInfractions

	return &RatchetResult{
		PreviousCount:          b.TotalInfractions,
		CurrentCount:           len(currentViolations),
		NewViolations:          newViolations,
		TouchedCleanViolations: touchedCleanViolations,
		Passed:                 passed,
	}
}

// worsenedFiles reports which touched files carry more infractions of some rule than the
// baseline recorded for them.
//
// It compares per-file, per-rule counts rather than fingerprints, on purpose. A fingerprint is
// path:line:rule, so adding or removing a line above a baselined infraction changes its
// fingerprint without changing the debt. Comparing fingerprints would read any line shift as new
// debt and reproduce the defect this function exists to fix. A count for one rule in one file
// is independent of where in the file the infraction sits.
//
// A file is worse when any single rule's count rose. Trading one rule's infraction for another's
// is still a new infraction of the second rule, and a lower total must not hide it.
func worsenedFiles(recorded, current []Infraction, touched map[string]struct{}) map[string]bool {
	before := countByFileRule(recorded, touched)
	after := countByFileRule(current, touched)
	worsened := make(map[string]bool, len(after))
	for key, count := range after {
		if count > before[key] {
			worsened[key.file] = true
		}
	}
	return worsened
}

// fileRule identifies one rule within one file.
type fileRule struct {
	file string
	rule string
}

// countByFileRule counts infractions per file and rule, restricted to the touched files.
func countByFileRule(infractions []Infraction, touched map[string]struct{}) map[fileRule]int {
	counts := make(map[fileRule]int)
	for i := 0; i < len(infractions); i++ {
		file := NormalizePath(infractions[i].FilePath)
		if _, ok := touched[file]; !ok {
			continue
		}
		counts[fileRule{file: file, rule: infractions[i].RuleID}]++
	}
	return counts
}

// touchedRuleRevokes decides, per touched file, whether the touched-file clean rule revokes its
// baseline exemptions. By default any touch revokes them. Under DebtDelta only a file whose debt
// grew does. Deciding it here keeps the ratchet's own loop within the complexity bound.
func touchedRuleRevokes(recorded, current []Infraction, touched map[string]struct{}, opts RatchetOptions) func(string) bool {
	if !opts.DebtDelta {
		return func(string) bool { return true }
	}
	worsened := worsenedFiles(recorded, current, touched)
	return func(file string) bool { return worsened[file] }
}
