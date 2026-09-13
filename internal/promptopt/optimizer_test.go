package promptopt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/notebook"
)

func experiment(t *testing.T) Experiment {
	t.Helper()
	h := notebook.Digest([]byte("synthetic contract"))
	id := Identity{Provider: "fixture", Model: "fixture-model", Revision: "v1", AdapterSHA256: h, DatasetSHA256: h, ContractSHA256: h}
	c := Candidate{ID: "baseline", Prompt: "keep constraints", Identity: id, HeldOut: true, ObservedAt: "2026-09-12T00:00:00Z",
		Results: []Observation{{Case: "positive", Quality: .9, CostUSD: .1, LatencyMS: 100, ContractPassed: true}, {Case: "negative", Quality: 1, CostUSD: .1, LatencyMS: 100, ContractPassed: true}}}
	c.PromptSHA256 = notebook.Digest([]byte(c.Prompt))
	other := c
	other.ID = "candidate"
	other.Prompt = "preserve all constraints"
	other.PromptSHA256 = notebook.Digest([]byte(other.Prompt))
	other.Results = append([]Observation(nil), c.Results...)
	other.Results[0].CostUSD = .05
	return Experiment{Format: "praetor-prompt-experiment-v1", Identity: id, BaselineID: c.ID, MinQuality: .8, MaxAgeHours: 24, Candidates: []Candidate{c, other}}
}

func evaluate(t *testing.T, e Experiment) (*Selection, error) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return Select(context.Background(), raw, time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC))
}

func TestSelectMeasuredImprovementAndPreserveBaseline(t *testing.T) {
	e := experiment(t)
	s, err := evaluate(t, e)
	if err != nil || !s.Changed || s.SelectedID != "candidate" {
		t.Fatalf("selection %+v, %v", s, err)
	}
	e.Candidates[1].Results[0].Quality = .85
	s, err = evaluate(t, e)
	if err != nil || s.Changed {
		t.Fatalf("per-case quality regression selected: %+v, %v", s, err)
	}
	e.Candidates[1].Results[0].Quality = .9
	e.Candidates[1].Results[0].ContractPassed = false
	s, err = evaluate(t, e)
	if err != nil || s.Changed {
		t.Fatalf("contract regression selected: %+v, %v", s, err)
	}
}

func TestRejectUncomparableEvidence(t *testing.T) {
	for _, mutate := range []func(*Experiment){
		func(e *Experiment) { e.Candidates[1].HeldOut = false },
		func(e *Experiment) { e.Candidates[1].Identity.Revision = "v2" },
		func(e *Experiment) { e.Candidates[1].ObservedAt = "2020-01-01T00:00:00Z" },
		func(e *Experiment) { e.Candidates[1].PromptSHA256 = "wrong" },
		func(e *Experiment) { e.Candidates[1].Results[1].Case = "different" },
		func(e *Experiment) { e.Candidates[1].Results[1].Case = "positive" },
		func(e *Experiment) { e.Candidates[1].Results[0].CostUSD = -1 },
		func(e *Experiment) { e.Candidates[1].Results = e.Candidates[1].Results[:1] },
		func(e *Experiment) { e.Candidates[0].Results[0].ContractPassed = false },
	} {
		e := experiment(t)
		mutate(&e)
		if _, err := evaluate(t, e); err == nil {
			t.Fatal("invalid experiment accepted")
		}
	}
}

func TestSelectRequiresExplicitMeasurementsAndFloor(t *testing.T) {
	raw, err := json.Marshal(experiment(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"min_quality":0.8`, `"quality":0.9`, `"cost_usd":0.1`, `"latency_ms":100`, `"contract_passed":true`} {
		for _, replacement := range []string{"", strings.SplitN(field, ":", 2)[0] + ":null,"} {
			t.Run(field+"/"+replacement, func(t *testing.T) {
				input := strings.Replace(string(raw), field+",", replacement, 1)
				// contract_passed is the final observation field, without a comma.
				if strings.HasPrefix(field, `"contract_passed"`) {
					input = strings.Replace(string(raw), ","+field, strings.TrimSuffix(replacement, ","), 1)
					if replacement != "" {
						input = strings.Replace(string(raw), field, strings.TrimSuffix(replacement, ","), 1)
					}
				}
				if !json.Valid([]byte(input)) {
					t.Fatal("invalid test JSON")
				}
				if _, err := Select(context.Background(), []byte(input), time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)); err == nil {
					t.Fatal("absent evidence accepted")
				}
			})
		}
	}
}

func TestSelectAcceptsExplicitZeroAndFalse(t *testing.T) {
	e := experiment(t)
	e.MinQuality = 0
	for i := range e.Candidates {
		for j := range e.Candidates[i].Results {
			e.Candidates[i].Results[j].Quality = 0
			e.Candidates[i].Results[j].CostUSD = 0
			e.Candidates[i].Results[j].LatencyMS = 0
		}
	}
	e.Candidates[1].Results[0].ContractPassed = false
	s, err := evaluate(t, e)
	if err != nil || s.Changed || s.MeanQuality != 0 || s.TotalCostUSD != 0 || s.MeanLatencyMS != 0 {
		t.Fatalf("explicit zero or false rejected: %+v, %v", s, err)
	}
}

func TestSelectStillRejectsUnknownAndDuplicateFields(t *testing.T) {
	raw, err := json.Marshal(experiment(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range [][2]string{
		{`"min_quality":0.8`, `"min_quality":0.8,"surprise":true`},
		{`"min_quality":0.8`, `"min_quality":0.8,"min_quality":0`},
		{`"quality":0.9`, `"quality":0.9,"surprise":true`},
		{`"quality":0.9`, `"quality":0.9,"quality":1`},
	} {
		input := strings.Replace(string(raw), mutation[0], mutation[1], 1)
		if _, err := Select(context.Background(), []byte(input), time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)); err == nil {
			t.Fatalf("ambiguous evidence accepted: %s", mutation[1])
		}
	}
}
