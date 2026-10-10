package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleIdentity() RunIdentity {
	return RunIdentity{
		PhysicalModel:  "claude-3-5-sonnet",
		Harness:        "praetor-agent",
		HarnessVersion: "1.0.0",
		PromptDigest:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ContextDigest:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		ContextBytes:   1024,
		ToolSet:        []string{"read", "write"},
		PriorRounds:    1,
		Retries:        0,
		CostEstimate:   0.05,
	}
}

func ptrFloat64(v float64) *float64 { return &v }

func sampleOutcome(task, result string) Outcome {
	return Outcome{
		Time:          time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
		Task:          task,
		Lane:          "gateway-coding",
		Target:        "light",
		ResolvedModel: "claude-3-5-sonnet",
		Result:        result,
		DurationMS:    1200,
		Identity:      sampleIdentity(),
		ActualCost:    ptrFloat64(0.048),
	}
}

func TestOutcomeLogAppendsAndReadsBackInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "outcomes.jsonl")
	ctx := context.Background()
	samples := []Outcome{sampleOutcome("stubs", OutcomeOK), sampleOutcome("synthesis", OutcomeFail), sampleOutcome("stubs", OutcomeTimeout)}
	for _, o := range samples {
		if err := AppendOutcome(ctx, path, o); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadOutcomes(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomeResults(t, got)
	assertOutcomeIdentity(t, got[0])
}

func assertOutcomeResults(t *testing.T, got []Outcome) {
	t.Helper()
	if len(got) != 3 {
		t.Fatalf("expected 3 outcomes, got: %d", len(got))
	}
	if got[0].Result != OutcomeOK || got[1].Task != "synthesis" {
		t.Fatalf("results mismatch: %+v", got)
	}
	if got[2].Result != OutcomeTimeout || got[0].DurationMS != 1200 {
		t.Fatalf("duration or result mismatch: %+v", got)
	}
}

func assertOutcomeIdentity(t *testing.T, o Outcome) {
	t.Helper()
	if o.ResolvedModel != "claude-3-5-sonnet" {
		t.Errorf("wrong resolved model: %s", o.ResolvedModel)
	}
	if o.Identity.Harness != "praetor-agent" {
		t.Errorf("wrong harness: %s", o.Identity.Harness)
	}
	if o.IdentityKey == "" || o.EstimateError == nil {
		t.Errorf("missing key or estimate error: %+v", o)
	}
}

func TestOutcomeLogPermissionsArePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "outcomes.jsonl")
	if err := AppendOutcome(context.Background(), path, sampleOutcome("stubs", OutcomeOK)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || (info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("log must be private: %v %v", info, err)
	}
}

func TestOutcomeLogMissingIsEmptyHistory(t *testing.T) {
	got, err := ReadOutcomes(context.Background(), filepath.Join(t.TempDir(), "absent.jsonl"))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing log: %v %v", got, err)
	}
}

func outcomeRefusalMutators() map[string]func(*Outcome) {
	return map[string]func(*Outcome){
		"no task":         func(o *Outcome) { o.Task = "" },
		"padded task":     func(o *Outcome) { o.Task = " stubs" },
		"no target":       func(o *Outcome) { o.Target = "" },
		"unknown result":  func(o *Outcome) { o.Result = "great" },
		"no time":         func(o *Outcome) { o.Time = time.Time{} },
		"negative time":   func(o *Outcome) { o.DurationMS = -1 },
		"oversized note":  func(o *Outcome) { o.Note = strings.Repeat("n", maxOutcomeNoteBytes+1) },
		"invalid lane":    func(o *Outcome) { o.Lane = "a\nb" },
		"empty lane name": func(o *Outcome) { o.Lane = " " },
		"negative actual": func(o *Outcome) { o.ActualCost = ptrFloat64(-0.5) },
	}
}

func TestOutcomeRefusals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcomes.jsonl")
	ctx := context.Background()
	for name, mutate := range outcomeRefusalMutators() {
		o := sampleOutcome("stubs", OutcomeOK)
		mutate(&o)
		if err := AppendOutcome(ctx, path, o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a refused record created the log")
	}
	atBound := sampleOutcome("stubs", OutcomeOK)
	atBound.Note = strings.Repeat("n", maxOutcomeNoteBytes)
	if err := AppendOutcome(ctx, path, atBound); err != nil {
		t.Fatalf("note at the bound refused: %v", err)
	}
	if err := AppendOutcome(ctx, dir, atBound); err == nil {
		t.Fatal("a directory accepted as the log")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := AppendOutcome(cancelled, path, atBound); err == nil {
		t.Fatal("canceled context ignored")
	}
}

func identityRefusalMutators() map[string]func(*Outcome) {
	return map[string]func(*Outcome){
		"no identity":       func(o *Outcome) { o.Identity = RunIdentity{} },
		"no phys model":     func(o *Outcome) { o.Identity.PhysicalModel = "" },
		"invalid phys":      func(o *Outcome) { o.Identity.PhysicalModel = "bad model!" },
		"no harness":        func(o *Outcome) { o.Identity.Harness = "" },
		"no harness ver":    func(o *Outcome) { o.Identity.HarnessVersion = "" },
		"no prompt digest":  func(o *Outcome) { o.Identity.PromptDigest = "" },
		"no context digest": func(o *Outcome) { o.Identity.ContextDigest = "" },
		"negative bytes":    func(o *Outcome) { o.Identity.ContextBytes = -1 },
		"negative rounds":   func(o *Outcome) { o.Identity.PriorRounds = -1 },
		"negative retries":  func(o *Outcome) { o.Identity.Retries = -1 },
		"negative estimate": func(o *Outcome) { o.Identity.CostEstimate = -0.1 },
		"invalid tool":      func(o *Outcome) { o.Identity.ToolSet = []string{"bad tool"} },
		"mismatch model":    func(o *Outcome) { o.ResolvedModel = "other-model" },
		"corrupt key":       func(o *Outcome) { o.IdentityKey = "sha256:corrupt" },
	}
}

func TestOutcomeIdentityRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	ctx := context.Background()
	for name, mutate := range identityRefusalMutators() {
		o := sampleOutcome("stubs", OutcomeOK)
		mutate(&o)
		if err := AppendOutcome(ctx, path, o); err == nil {
			t.Errorf("refusal expected for %s, but accepted", name)
		}
	}
}

func TestOutcomeIdentityKeyDiffersOnPromptTemplate(t *testing.T) {
	run1 := sampleOutcome("stubs", OutcomeOK)
	run1.Identity.PromptDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	run2 := sampleOutcome("stubs", OutcomeOK)
	run2.Identity.PromptDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	run3 := sampleOutcome("stubs", OutcomeOK)
	run3.Identity.PromptDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	key1 := run1.Key()
	key2 := run2.Key()
	key3 := run3.Key()

	if key1 == key2 {
		t.Fatalf("expected different identity keys for differing prompt digests, got identical: %s", key1)
	}
	if key1 != key3 {
		t.Fatalf("expected identical keys for identical prompt digests, got: %s vs %s", key1, key3)
	}
}

func TestOutcomeWrittenThroughAliasNamesResolvedModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	ctx := context.Background()

	o := sampleOutcome("stubs", OutcomeOK)
	o.Target = "gateway-coding"
	o.ResolvedModel = "claude-3-7-sonnet-20250219"
	o.Identity.PhysicalModel = "claude-3-7-sonnet-20250219"

	if err := AppendOutcome(ctx, path, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	read, err := ReadOutcomes(ctx, path)
	if err != nil || len(read) != 1 {
		t.Fatalf("failed reading recorded outcome: %v", err)
	}
	if read[0].Target != "gateway-coding" {
		t.Errorf("expected target gateway-coding, got: %s", read[0].Target)
	}
	if read[0].ResolvedModel != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected resolved_model to name physical model, got: %s", read[0].ResolvedModel)
	}
}

func TestEstimateErrorMetricPerLane(t *testing.T) {
	outcomes := []Outcome{
		{
			Task:          "stubs",
			Lane:          "gateway-coding",
			Target:        "light",
			ResolvedModel: "claude-3-5-sonnet",
			Result:        OutcomeOK,
			Identity:      sampleIdentity(),
			ActualCost:    ptrFloat64(0.06),
		},
		{
			Task:          "implement",
			Lane:          "gateway-coding",
			Target:        "light",
			ResolvedModel: "claude-3-5-sonnet",
			Result:        OutcomeOK,
			Identity:      sampleIdentity(),
			ActualCost:    ptrFloat64(0.04),
		},
		{
			Task:          "synthesis",
			Lane:          "frontier-agent",
			Target:        "heavy",
			ResolvedModel: "claude-3-opus",
			Result:        OutcomeOK,
			Identity: RunIdentity{
				PhysicalModel:  "claude-3-opus",
				Harness:        "praetor-agent",
				HarnessVersion: "1.0.0",
				PromptDigest:   "sha256:3333333333333333333333333333333333333333333333333333333333333333",
				ContextDigest:  "sha256:4444444444444444444444444444444444444444444444444444444444444444",
				CostEstimate:   0.20,
			},
			ActualCost: ptrFloat64(0.25),
		},
	}

	recs := ReconcileLanes(outcomes)
	if len(recs) != 2 {
		t.Fatalf("expected 2 lanes, got: %d", len(recs))
	}

	coding := recs[1]
	if coding.Lane != "gateway-coding" || coding.Runs != 2 {
		t.Errorf("unexpected coding lane: %+v", coding)
	}
	if coding.EstimatedCost != 0.10 || coding.ActualCost != 0.10 || coding.EstimateError != 0 {
		t.Errorf("unexpected cost reconciliation: %+v", coding)
	}

	rendered := RenderLaneReconciliation(recs)
	if !strings.Contains(rendered, "lane gateway-coding:") || !strings.Contains(rendered, "lane frontier-agent:") {
		t.Errorf("rendered reconciliation missing lanes: %s", rendered)
	}
}

func TestOutcomeLogCorruptLineIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	if err := AppendOutcome(context.Background(), path, sampleOutcome("stubs", OutcomeOK)); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOutcomes(context.Background(), path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("corrupt record must fail the read, naming its line: %v", err)
	}
}

func TestOutcomeIdentityKeyStableAcrossRetries(t *testing.T) {
	id1 := sampleIdentity()
	id1.Retries = 0
	id1.PriorRounds = 1
	id1.CostEstimate = 0.05
	id1.ContextBytes = 1000

	id2 := sampleIdentity()
	id2.Retries = 2
	id2.PriorRounds = 3
	id2.CostEstimate = 0.10
	id2.ContextBytes = 2000

	if id1.Key() != id2.Key() {
		t.Fatalf("expected identical keys across retries, got: %s vs %s", id1.Key(), id2.Key())
	}
}

func TestValidateRunIdentity_ToolsBoundary(t *testing.T) {
	id := sampleIdentity()
	tools64 := make([]string, MaxRoutingTags)
	for i := 0; i < MaxRoutingTags; i++ {
		tools64[i] = fmt.Sprintf("tool-%d", i)
	}
	id.ToolSet = tools64
	if err := ValidateRunIdentity(id); err != nil {
		t.Fatalf("expected 64 tools to be accepted, got error: %v", err)
	}

	idOver := sampleIdentity()
	tools65 := make([]string, MaxRoutingTags+1)
	for i := 0; i < MaxRoutingTags+1; i++ {
		tools65[i] = fmt.Sprintf("tool-%d", i)
	}
	idOver.ToolSet = tools65
	if err := ValidateRunIdentity(idOver); err == nil {
		t.Fatal("expected 65 tools to be refused, but was accepted")
	}

	idDup := sampleIdentity()
	idDup.ToolSet = []string{"tool-a", "tool-b", "tool-a"}
	if err := ValidateRunIdentity(idDup); err == nil {
		t.Fatal("expected duplicate tools to be refused, but was accepted")
	}
}

func TestOutcomeLegacyRecordReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcomes.jsonl")
	legacy := `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","duration_ms":100}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOutcomes(context.Background(), path)
	if err != nil {
		t.Fatalf("legacy record must be readable: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(got))
	}
	if got[0].Identity.PhysicalModel != "" {
		t.Errorf("legacy record should be unidentified, got: %+v", got[0].Identity)
	}
}

func TestOutcomeEstimateWithoutActualCost(t *testing.T) {
	o := sampleOutcome("stubs", OutcomeOK)
	o.Identity.CostEstimate = 0.50
	o.ActualCost = nil
	o.EstimateError = nil
	PrepareOutcome(&o)
	if o.EstimateError != nil {
		t.Fatalf("expected EstimateError to be nil when ActualCost is nil, got: %v", *o.EstimateError)
	}
}

func TestOutcomeZeroCostAndContextBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	o := sampleOutcome("stubs", OutcomeOK)
	o.Identity.ContextBytes = 0
	o.Identity.CostEstimate = 0
	o.ActualCost = ptrFloat64(0)
	if err := AppendOutcome(context.Background(), path, o); err != nil {
		t.Fatalf("zero cost and context bytes refused: %v", err)
	}
	got, err := ReadOutcomes(context.Background(), path)
	if err != nil || len(got) != 1 {
		t.Fatalf("readback failed: %v", err)
	}
	if got[0].Identity.ContextBytes != 0 || got[0].Identity.CostEstimate != 0 {
		t.Errorf("zero values corrupted: %+v", got[0].Identity)
	}
	if got[0].ActualCost == nil || *got[0].ActualCost != 0 || got[0].EstimateError == nil || *got[0].EstimateError != 0 {
		t.Errorf("zero actual cost and error corrupted: %+v", got[0])
	}
}
