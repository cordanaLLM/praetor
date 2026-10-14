package planning

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExperimentValidationPositive(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Draft)
	}{
		{
			name: "hypothesis full contract",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "hypothesis",
					Null:              "Conventional baseline implementation",
					KillCriterion:     "Latency regresses by more than 5 percent",
					Margin:            "15 percent throughput improvement",
					ProtectedOutcomes: []string{"Zero error rate regression", "Memory within budget"},
					Disposition:       "",
				}
			},
		},
		{
			name: "established minimal",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "established",
					Null:              "",
					KillCriterion:     "",
					Margin:            "",
					ProtectedOutcomes: []string{},
					Disposition:       "",
				}
			},
		},
		{
			name: "research and manual steps need no experiment block",
			mutate: func(d *Draft) {
				d.Steps[1].Experiment, d.Steps[2].Experiment = nil, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := fixtureDraft(t)
			test.mutate(&draft)
			if _, err := compileDraft(t, draft); err != nil {
				t.Fatalf("expected valid draft to compile: %v", err)
			}
		})
	}
}

func TestExperimentValidationNegative(t *testing.T) {
	tests := []struct {
		name        string
		expectedErr string
		mutate      func(*Draft)
	}{
		{
			name:        "schema_version 2 implementation step without experiment",
			expectedErr: "step step-render: schema_version 2 requires experiment.evidence_status on implementation steps",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = nil
			},
		},
		{
			name:        "unknown evidence_status",
			expectedErr: "invalid evidence_status",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "speculative_unknown",
					Null:              "baseline",
					KillCriterion:     "fails",
					Margin:            "margin",
					ProtectedOutcomes: []string{"invariant"},
				}
			},
		},
		{
			name:        "hypothesis without kill_criterion",
			expectedErr: "kill_criterion required for evidence_status hypothesis",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "hypothesis",
					Null:              "baseline",
					KillCriterion:     "",
					Margin:            "margin",
					ProtectedOutcomes: []string{"invariant"},
				}
			},
		},
		{
			name:        "speculative with zero protected outcomes",
			expectedErr: "protected_outcomes requires 1..8 entries",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "speculative",
					Null:              "baseline",
					KillCriterion:     "fails",
					Margin:            "margin",
					ProtectedOutcomes: []string{},
				}
			},
		},
		{
			name:        "disposition maybe",
			expectedErr: "invalid disposition",
			mutate: func(d *Draft) {
				d.Steps[0].Experiment = &Experiment{
					EvidenceStatus:    "hypothesis",
					Null:              "baseline",
					KillCriterion:     "fails",
					Margin:            "margin",
					ProtectedOutcomes: []string{"invariant"},
					Disposition:       "maybe",
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := fixtureDraft(t)
			test.mutate(&draft)
			_, err := compileDraft(t, draft)
			if err == nil {
				t.Fatal("expected compilation error, got nil")
			}
			if !strings.Contains(err.Error(), test.expectedErr) {
				t.Fatalf("expected error containing %q, got %v", test.expectedErr, err)
			}
		})
	}
}

func TestExperimentJSONShapeRefusals(t *testing.T) {
	t.Run("unknown field inside experiment", func(t *testing.T) {
		doc := fixtureJSONDocument(t)
		step := jsonObjectAt(t, doc, "steps", 0)
		step["experiment"] = map[string]any{
			"evidence_status":    "hypothesis",
			"null":               "baseline",
			"kill_criterion":     "fails",
			"margin":             "margin",
			"protected_outcomes": []any{"invariant"},
			"disposition":        "",
			"unexpected_field":   "refused",
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "contains unknown field unexpected_field") {
			t.Fatalf("expected unknown field refusal, got %v", err)
		}
	})

	t.Run("experiment missing margin key", func(t *testing.T) {
		doc := fixtureJSONDocument(t)
		step := jsonObjectAt(t, doc, "steps", 0)
		step["experiment"] = map[string]any{
			"evidence_status":    "hypothesis",
			"null":               "baseline",
			"kill_criterion":     "fails",
			"protected_outcomes": []any{"invariant"},
			"disposition":        "",
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "experiment.margin must be present") {
			t.Fatalf("expected missing margin refusal, got %v", err)
		}
	})
}

func TestExperimentBoundaries(t *testing.T) {
	t.Run("protected outcomes count boundary", func(t *testing.T) {
		draft := fixtureDraft(t)
		outcomes := textValues("protected outcome", MaxProtectedOutcomes)
		draft.Steps[0].Experiment = &Experiment{
			EvidenceStatus:    "hypothesis",
			Null:              "baseline",
			KillCriterion:     "fails",
			Margin:            "margin",
			ProtectedOutcomes: outcomes,
		}
		if _, err := compileDraft(t, draft); err != nil {
			t.Fatalf("exact boundary rejected: %v", err)
		}
		draft.Steps[0].Experiment.ProtectedOutcomes = append(draft.Steps[0].Experiment.ProtectedOutcomes, "over-limit")
		if _, err := compileDraft(t, draft); err == nil || !strings.Contains(err.Error(), "protected_outcomes requires 1..8 entries") {
			t.Fatalf("over boundary accepted: %v", err)
		}
	})

	t.Run("protected outcome text bytes boundary", func(t *testing.T) {
		draft := fixtureDraft(t)
		draft.Steps[0].Experiment = &Experiment{
			EvidenceStatus:    "hypothesis",
			Null:              "baseline",
			KillCriterion:     "fails",
			Margin:            "margin",
			ProtectedOutcomes: []string{strings.Repeat("p", MaxTextBytes)},
		}
		if _, err := compileDraft(t, draft); err != nil {
			t.Fatalf("exact text bytes boundary rejected: %v", err)
		}
		draft.Steps[0].Experiment.ProtectedOutcomes = []string{strings.Repeat("p", MaxTextBytes+1)}
		if _, err := compileDraft(t, draft); err == nil || !strings.Contains(err.Error(), "invalid or duplicate text") {
			t.Fatalf("over text bytes boundary accepted: %v", err)
		}
	})
}

func TestExperimentRenderAndRoundTrip(t *testing.T) {
	t.Run("roadmap contains kill criterion", func(t *testing.T) {
		draft := fixtureDraft(t)
		const killText = "Throughput drops below 500 rps under nominal load"
		draft.Steps[0].Experiment = &Experiment{
			EvidenceStatus:    "hypothesis",
			Null:              "Single-threaded processor",
			KillCriterion:     killText,
			Margin:            "Double throughput",
			ProtectedOutcomes: []string{"Memory bounded"},
			Disposition:       "",
		}
		result, err := compileDraft(t, draft)
		if err != nil {
			t.Fatalf("compile failed: %v", err)
		}
		roadmap := string(result.Files["ROADMAP.md"])
		if !strings.Contains(roadmap, killText) {
			t.Fatalf("ROADMAP.md does not contain kill criterion %q", killText)
		}
		if !strings.Contains(roadmap, "### Experiment") {
			t.Fatal("ROADMAP.md missing Experiment block")
		}
		if !strings.Contains(roadmap, "Disposition: pending") {
			t.Fatal("ROADMAP.md missing pending disposition")
		}
	})

	t.Run("round trip keeps digest", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/valid-draft-experiment.json")
		if err != nil {
			t.Fatal(err)
		}
		first, err := Compile(t.Context(), raw)
		if err != nil {
			t.Fatalf("first compile failed: %v", err)
		}
		canonical := first.Files["plan.json"]
		second, err := Compile(t.Context(), canonical)
		if err != nil {
			t.Fatalf("second compile failed: %v", err)
		}
		if first.Digest != second.Digest {
			t.Fatalf("round trip changed digest: first %s, second %s", first.Digest, second.Digest)
		}
		if !reflect.DeepEqual(first.Files, second.Files) {
			t.Fatal("round trip changed artifact files")
		}
	})
}

func TestSchemaV1Window(t *testing.T) {
	v1, err := os.ReadFile("testdata/valid-draft-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := os.ReadFile("testdata/valid-draft.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { now = time.Now })
	now = func() time.Time { return SchemaV1RefusedFrom.Add(-time.Nanosecond) }
	result, err := Compile(context.Background(), v1)
	if err != nil {
		t.Fatalf("schema_version 1 inside the window must compile: %v", err)
	}
	report := result.Report(false)
	if len(report.Deprecations) != 1 || !strings.Contains(report.Deprecations[0], "refused from 2026-10-25") {
		t.Fatalf("one deprecation line naming the date expected, got %q", report.Deprecations)
	}
	now = func() time.Time { return SchemaV1RefusedFrom }
	if _, err := Compile(context.Background(), v1); err == nil || !strings.Contains(err.Error(), "schema_version 1 was refused from 2026-10-25") {
		t.Fatalf("schema_version 1 at the window end must be refused, got %v", err)
	}
	result, err = Compile(context.Background(), v2)
	if err != nil || len(result.Report(false).Deprecations) != 0 {
		t.Fatalf("schema_version 2 compiles without deprecations: %v %v", err, result)
	}
	v3 := strings.Replace(string(v2), `"schema_version": 2`, `"schema_version": 3`, 1)
	if _, err := Compile(context.Background(), []byte(v3)); err == nil || !strings.Contains(err.Error(), "schema_version 3 unsupported") {
		t.Fatalf("an unknown schema version must be refused, got %v", err)
	}
}
