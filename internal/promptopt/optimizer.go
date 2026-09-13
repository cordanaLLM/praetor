// Package promptopt selects prompt candidates from comparable held-out results.
// Metrics are caller-supplied observations; selection is not provider execution.
package promptopt

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/notebook"
)

type Identity struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Revision       string `json:"revision"`
	AdapterSHA256  string `json:"adapter_sha256"`
	DatasetSHA256  string `json:"dataset_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
}

type Observation struct {
	Case           string  `json:"case"`
	Quality        float64 `json:"quality"`
	CostUSD        float64 `json:"cost_usd"`
	LatencyMS      float64 `json:"latency_ms"`
	ContractPassed bool    `json:"contract_passed"`
}

type Candidate struct {
	ID           string        `json:"id"`
	Prompt       string        `json:"prompt"`
	PromptSHA256 string        `json:"prompt_sha256"`
	Identity     Identity      `json:"identity"`
	HeldOut      bool          `json:"held_out"`
	ObservedAt   string        `json:"observed_at"`
	Results      []Observation `json:"results"`
}

type Experiment struct {
	Format      string      `json:"format"`
	Identity    Identity    `json:"identity"`
	BaselineID  string      `json:"baseline_id"`
	MinQuality  float64     `json:"min_quality"`
	MaxAgeHours int         `json:"max_age_hours"`
	Candidates  []Candidate `json:"candidates"`
}

type Selection struct {
	SelectedID       string  `json:"selected_id"`
	PromptSHA256     string  `json:"prompt_sha256"`
	ExperimentSHA256 string  `json:"experiment_sha256"`
	Changed          bool    `json:"changed"`
	MeanQuality      float64 `json:"mean_quality"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	MeanLatencyMS    float64 `json:"mean_latency_ms"`
	Basis            string  `json:"basis"`
}

type score struct {
	quality, cost, latency float64
	passed                 bool
}

// Select rejects stale or incompatible experiments. An eligible replacement
// preserves each baseline case's quality and contract outcome, then minimizes
// observed total cost, mean latency, and finally maximizes mean quality.
func Select(ctx context.Context, raw []byte, now time.Time) (*Selection, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context required")
	}
	var e Experiment
	if err := notebook.Decode(raw, &e); err != nil {
		return nil, err
	}
	baseline, err := validateExperiment(e, now)
	if err != nil {
		return nil, err
	}
	best, bestScore := baseline, summarize(baseline)
	if !bestScore.passed || bestScore.quality < e.MinQuality {
		return nil, fmt.Errorf("baseline does not meet quality and contract floor")
	}
	for _, candidate := range e.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s := summarize(candidate)
		if !eligible(candidate, baseline, s, e.MinQuality) {
			continue
		}
		if better(s, bestScore) {
			best, bestScore = candidate, s
		}
	}
	return &Selection{SelectedID: best.ID, PromptSHA256: best.PromptSHA256,
		ExperimentSHA256: notebook.Digest(raw), Changed: best.ID != baseline.ID,
		MeanQuality: bestScore.quality, TotalCostUSD: bestScore.cost, MeanLatencyMS: bestScore.latency,
		Basis: "reported held-out measurements for exact provider/model/revision/adapter/dataset/contract; per-case nonregression, then cost, latency, quality; no deployment or universal optimum claim"}, nil
}

func validateExperiment(e Experiment, now time.Time) (Candidate, error) {
	if err := validateExperimentHeader(e); err != nil {
		return Candidate{}, err
	}
	seen := make(map[string]bool)
	var baseline Candidate
	for _, c := range e.Candidates {
		if seen[c.ID] {
			return Candidate{}, fmt.Errorf("duplicate candidate ID")
		}
		seen[c.ID] = true
		if err := validateCandidate(c, e, now); err != nil {
			return Candidate{}, err
		}
		if c.ID == e.BaselineID {
			baseline = c
		}
	}
	if baseline.ID == "" {
		return Candidate{}, fmt.Errorf("baseline missing")
	}
	wanted := caseIDs(baseline)
	for _, c := range e.Candidates {
		if strings.Join(caseIDs(c), "\n") != strings.Join(wanted, "\n") {
			return Candidate{}, fmt.Errorf("candidate cases differ from baseline")
		}
	}
	return baseline, nil
}

func validateExperimentHeader(e Experiment) error {
	if e.Format != "praetor-prompt-experiment-v1" || len(e.Candidates) < 1 || len(e.Candidates) > 16 {
		return fmt.Errorf("versioned experiment with 1..16 candidates required")
	}
	if !finite(e.MinQuality) || e.MinQuality > 1 || e.MaxAgeHours < 1 || e.MaxAgeHours > 720 {
		return fmt.Errorf("invalid quality floor or freshness window")
	}
	return validateIdentity(e.Identity)
}

func validateIdentity(id Identity) error {
	for _, label := range []string{id.Provider, id.Model, id.Revision} {
		if strings.TrimSpace(label) == "" || len(label) > 256 || strings.ContainsAny(label, "\n\r\x00") {
			return fmt.Errorf("explicit provider, model and revision required")
		}
	}
	for _, hash := range []string{id.AdapterSHA256, id.DatasetSHA256, id.ContractSHA256} {
		if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return fmt.Errorf("exact adapter, dataset and contract SHA256 required")
		}
	}
	return nil
}

func validateCandidate(c Candidate, e Experiment, now time.Time) error {
	if err := validateCandidateIdentity(c, e.Identity); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339, c.ObservedAt)
	if err != nil || t.After(now) || now.Sub(t) > time.Duration(e.MaxAgeHours)*time.Hour {
		return fmt.Errorf("candidate evidence is stale or future-dated")
	}
	if len(c.Results) < 2 || len(c.Results) > 256 {
		return fmt.Errorf("2..256 held-out results required")
	}
	seen := make(map[string]bool)
	for _, r := range c.Results {
		if err := validateObservation(r, seen); err != nil {
			return err
		}
		seen[r.Case] = true
	}
	return nil
}

func validateCandidateIdentity(c Candidate, identity Identity) error {
	if c.ID == "" || len(c.ID) > 128 || !c.HeldOut || c.Identity != identity {
		return fmt.Errorf("candidate requires held-out evidence and matching identity")
	}
	if strings.TrimSpace(c.Prompt) == "" || len(c.Prompt) > 32768 || notebook.Digest([]byte(c.Prompt)) != c.PromptSHA256 {
		return fmt.Errorf("candidate prompt hash or size invalid")
	}
	return nil
}

func validateObservation(r Observation, seen map[string]bool) error {
	if r.Case == "" || len(r.Case) > 128 || strings.ContainsAny(r.Case, "\n\r\x00") || seen[r.Case] {
		return fmt.Errorf("invalid or duplicate case ID")
	}
	if !finite(r.Quality) || r.Quality > 1 || !finite(r.CostUSD) || !finite(r.LatencyMS) {
		return fmt.Errorf("invalid evaluation metric")
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e9 }

func caseIDs(c Candidate) []string {
	ids := make([]string, 0, len(c.Results))
	for _, r := range c.Results {
		ids = append(ids, r.Case)
	}
	sort.Strings(ids)
	return ids
}

func summarize(c Candidate) score {
	s := score{passed: true}
	for _, r := range c.Results {
		s.quality += r.Quality
		s.cost += r.CostUSD
		s.latency += r.LatencyMS
		s.passed = s.passed && r.ContractPassed
	}
	s.quality /= float64(len(c.Results))
	s.latency /= float64(len(c.Results))
	return s
}

func nonregression(candidate, baseline Candidate) bool {
	quality := make(map[string]float64)
	for _, r := range baseline.Results {
		quality[r.Case] = r.Quality
	}
	for _, r := range candidate.Results {
		if r.Quality < quality[r.Case] {
			return false
		}
	}
	return true
}

func eligible(candidate, baseline Candidate, s score, minQuality float64) bool {
	return s.passed && s.quality >= minQuality && nonregression(candidate, baseline)
}

func better(a, b score) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.latency != b.latency {
		return a.latency < b.latency
	}
	return a.quality > b.quality
}
