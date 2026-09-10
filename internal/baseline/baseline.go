package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
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
	Version          int          `json:"version"`
	GeneratedAt      string       `json:"generated_at"`
	Repository       string       `json:"repository"`
	CommitSHA        string       `json:"commit_sha"`
	TotalInfractions int          `json:"total_infractions"`
	Infractions      []Infraction `json:"infractions"`
}

// RatchetResult details the evaluation of a commit/PR against the baseline.
type RatchetResult struct {
	PreviousCount          int
	CurrentCount           int
	NewViolations          []Infraction
	TouchedCleanViolations []Infraction
	Passed                 bool
}

// LoadBaseline reads and parses .standards-baseline.json.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Baseline{
				Version:          1,
				GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
				TotalInfractions: 0,
				Infractions:      []Infraction{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read baseline %s: %w", path, err)
	}

	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("failed to parse baseline %s: %w", path, err)
	}
	return &b, nil
}

// SaveBaseline writes a baseline to disk.
func SaveBaseline(path string, b *Baseline) error {
	b.TotalInfractions = len(b.Infractions)
	b.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal baseline: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write baseline to %s: %w", path, err)
	}
	return nil
}

// EvaluateRatchet enforces monotonic debt reduction and the touched-file clean rule.
// Invariant: V_total(t1) <= V_total(t0) AND touched files must have 0 violations.
func EvaluateRatchet(b *Baseline, currentViolations []Infraction, touchedFiles []string) *RatchetResult {
	touchedMap := make(map[string]struct{}, len(touchedFiles))
	for _, f := range touchedFiles {
		touchedMap[f] = struct{}{}
	}

	baselinedFingerprints := make(map[string]struct{}, len(b.Infractions))
	for _, inf := range b.Infractions {
		baselinedFingerprints[inf.Fingerprint] = struct{}{}
	}

	var newViolations []Infraction
	var touchedCleanViolations []Infraction

	for _, curr := range currentViolations {
		_, isTouched := touchedMap[curr.FilePath]
		_, isBaselined := baselinedFingerprints[curr.Fingerprint]

		if isTouched {
			// Touched-File Clean Rule: Any touched file revokes prior baseline exemptions!
			touchedCleanViolations = append(touchedCleanViolations, curr)
		} else if !isBaselined {
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
