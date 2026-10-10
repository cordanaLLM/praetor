package router

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readWriteToolSetDigest is SHA-256 over "read\nwrite\n", the digest of the sample tool set.
const readWriteToolSetDigest = "sha256:88b06efcea4b5946cebd4b0674b93744de328339de5d61b75db858119054ff93"

func sampleIdentity() RunIdentity {
	return RunIdentity{
		PhysicalModel:  "claude-3-5-sonnet",
		Harness:        "praetor-agent",
		HarnessVersion: "1.0.0",
		PromptDigest:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ContextDigest:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		ContextBytes:   1024,
		ToolSetDigest:  readWriteToolSetDigest,
		ToolCount:      2,
		ToolSet:        []string{"read", "write"},
		PriorRounds:    1,
		Retries:        0,
		CostEstimate:   ptrFloat64(0.05),
	}
}

// mustDigestToolSet returns the digest and count of a tool set the test declares valid.
func mustDigestToolSet(t *testing.T, tools []string) (string, int) {
	t.Helper()
	digest, count, err := DigestToolSet(tools)
	if err != nil {
		t.Fatal(err)
	}
	return digest, count
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
		"negative estimate": func(o *Outcome) { o.Identity.CostEstimate = ptrFloat64(-0.1) },
		"nan estimate":      func(o *Outcome) { o.Identity.CostEstimate = ptrFloat64(math.NaN()) },
		"infinite actual":   func(o *Outcome) { o.ActualCost = ptrFloat64(math.Inf(1)) },
		"invalid tool":      func(o *Outcome) { o.Identity.ToolSet = []string{"bad tool"} },
		"mismatch model":    func(o *Outcome) { o.ResolvedModel = "other-model" },
		"corrupt key":       func(o *Outcome) { o.IdentityKey = "sha256:corrupt" },
		"digest no count":   func(o *Outcome) { o.Identity.ToolSet, o.Identity.ToolCount = nil, 0 },
		"count no digest":   func(o *Outcome) { o.Identity.ToolSet, o.Identity.ToolSetDigest = nil, "" },
		"malformed digest":  func(o *Outcome) { o.Identity.ToolSet, o.Identity.ToolSetDigest = nil, "sha256:xyz" },
		"count over bound":  func(o *Outcome) { o.Identity.ToolSet, o.Identity.ToolCount = nil, MaxIdentityTools+1 },
		"list not digest":   func(o *Outcome) { o.Identity.ToolSet = []string{"read", "exec"} },
		"list not count":    func(o *Outcome) { o.Identity.ToolCount = 3 },
		"error no estimate": func(o *Outcome) { o.Identity.CostEstimate, o.EstimateError = nil, ptrFloat64(0.01) },
		"error no actual":   func(o *Outcome) { o.ActualCost, o.EstimateError = nil, ptrFloat64(0.01) },
		"error wrong value": func(o *Outcome) { o.EstimateError = ptrFloat64(0.5) },
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

// An outcome written through an alias names the resolved model: the writer leaves
// resolved_model unset, AppendOutcome derives it from the identity, never from the target, and
// a record whose resolved model is the alias while the identity names another model is refused.
func TestOutcomeWrittenThroughAliasNamesResolvedModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	ctx := context.Background()
	o := sampleOutcome("stubs", OutcomeOK)
	o.Target = "gateway-coding"
	o.ResolvedModel = ""
	o.Identity.PhysicalModel = "claude-3-7-sonnet-20250219"
	if err := AppendOutcome(ctx, path, o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	read, err := ReadOutcomes(ctx, path)
	if err != nil || len(read) != 1 {
		t.Fatalf("failed reading recorded outcome: %v", err)
	}
	if read[0].Target != "gateway-coding" || read[0].ResolvedModel != "claude-3-7-sonnet-20250219" {
		t.Fatalf("resolved_model must name the physical model behind target %q, got %q", read[0].Target, read[0].ResolvedModel)
	}
	aliasAsResolved := o
	aliasAsResolved.ResolvedModel = o.Target
	if err := AppendOutcome(ctx, path, aliasAsResolved); err == nil {
		t.Fatal("a record naming the alias as resolved model while the identity names another model was accepted")
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
				CostEstimate:   ptrFloat64(0.20),
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
	id1.CostEstimate = ptrFloat64(0.05)
	id1.ContextBytes = 1000

	id2 := sampleIdentity()
	id2.Retries = 2
	id2.PriorRounds = 3
	id2.CostEstimate = ptrFloat64(0.10)
	id2.ContextBytes = 2000

	if id1.Key() != id2.Key() {
		t.Fatalf("expected identical keys across retries, got: %s vs %s", id1.Key(), id2.Key())
	}
}

// mcpToolNames returns n distinct tool names at the length MCP servers produce.
func mcpToolNames(n int) []string {
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("mcp__plugin_cloudflare_cloudflare-observability__query_worker_observability_%04d", i)
	}
	return names
}

func TestDigestToolSet_Boundary(t *testing.T) {
	digest, count, err := DigestToolSet(mcpToolNames(MaxIdentityTools))
	if err != nil || count != MaxIdentityTools || !validToolSetDigest(digest) {
		t.Fatalf("a tool set at the bound must digest: %q %d %v", digest, count, err)
	}
	if _, _, err := DigestToolSet(mcpToolNames(MaxIdentityTools + 1)); err == nil {
		t.Fatal("a tool set past the bound was accepted")
	}
	if _, _, err := DigestToolSet([]string{"tool-a", "tool-b", "tool-a"}); err == nil {
		t.Fatal("duplicate tools were accepted")
	}
	if digest, count, err := DigestToolSet(nil); err != nil || digest != "" || count != 0 {
		t.Fatalf("an empty tool set has no digest: %q %d %v", digest, count, err)
	}
	forward, _ := mustDigestToolSet(t, []string{"read", "write"})
	backward, _ := mustDigestToolSet(t, []string{"write", "read"})
	other, _ := mustDigestToolSet(t, []string{"read", "exec"})
	if forward != readWriteToolSetDigest || forward != backward || forward == other {
		t.Fatal("the digest must ignore tool order and change with the tool set")
	}
}

// A run with more tools than any routing tag bound, at real MCP name length, fits one record:
// the record keeps the digest and count; the full list is refused only when it is kept too.
func TestOutcomeLargeToolSetFitsTheRecordBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	ctx := context.Background()
	o := sampleOutcome("stubs", OutcomeOK)
	o.Identity.ToolSet = nil
	o.Identity.ToolSetDigest, o.Identity.ToolCount = mustDigestToolSet(t, mcpToolNames(400))
	if err := AppendOutcome(ctx, path, o); err != nil {
		t.Fatalf("400 real-length tools identified by digest and count were refused: %v", err)
	}
	withList := o
	withList.Identity.ToolSet = mcpToolNames(400)
	if err := AppendOutcome(ctx, path, withList); err == nil || !strings.Contains(err.Error(), "digest and count") {
		t.Fatalf("a record over the byte bound must be refused naming the remedy, got %v", err)
	}
	got, err := ReadOutcomes(ctx, path)
	if err != nil || len(got) != 1 || got[0].Identity.ToolCount != 400 {
		t.Fatalf("readback: %+v %v", got, err)
	}
}

func TestOutcomeKeyCoversToolSet(t *testing.T) {
	listed := sampleIdentity()
	listed.ToolSetDigest, listed.ToolCount = "", 0
	digested := sampleIdentity()
	digested.ToolSet = nil
	other := sampleIdentity()
	other.ToolSet = nil
	other.ToolSetDigest, other.ToolCount = mustDigestToolSet(t, []string{"read"})
	if listed.Key() != digested.Key() || digested.Key() == other.Key() {
		t.Fatal("the key must follow the tool set, whether it is given as list or digest")
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
	o.Identity.CostEstimate = ptrFloat64(0.50)
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
	o.Identity.CostEstimate = ptrFloat64(0)
	o.ActualCost = ptrFloat64(0)
	if err := AppendOutcome(context.Background(), path, o); err != nil {
		t.Fatalf("zero cost and context bytes refused: %v", err)
	}
	got, err := ReadOutcomes(context.Background(), path)
	if err != nil || len(got) != 1 {
		t.Fatalf("readback failed: %v", err)
	}
	if got[0].Identity.ContextBytes != 0 || got[0].Identity.CostEstimate == nil || *got[0].Identity.CostEstimate != 0 {
		t.Errorf("zero values corrupted: %+v", got[0].Identity)
	}
	if got[0].ActualCost == nil || *got[0].ActualCost != 0 || got[0].EstimateError == nil || *got[0].EstimateError != 0 {
		t.Errorf("zero actual cost and error corrupted: %+v", got[0])
	}
}

// identityWithoutEstimate is a complete run identity that carries no pre-dispatch estimate.
func identityWithoutEstimate() RunIdentity {
	return RunIdentity{
		PhysicalModel:  "claude-3-5-sonnet",
		Harness:        "praetor-agent",
		HarnessVersion: "1.0.0",
		PromptDigest:   "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ContextDigest:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
	}
}

func TestOutcomeMissingEstimateIsNeverZero(t *testing.T) {
	o := sampleOutcome("stubs", OutcomeOK)
	o.Identity = identityWithoutEstimate()
	o.ActualCost = ptrFloat64(0.5)
	PrepareOutcome(&o)
	if o.EstimateError != nil {
		t.Fatalf("an outcome without an estimate must carry no estimate error, got %v", *o.EstimateError)
	}
	recs := ReconcileLanes([]Outcome{o})
	if len(recs) != 1 || recs[0].MeasuredRuns != 0 || recs[0].EstimatedCost != 0 || recs[0].ActualCost != 0 {
		t.Fatalf("a run without an estimate must not be measured: %+v", recs)
	}
	if rendered := RenderLaneReconciliation(recs); !strings.Contains(rendered, "not measured") {
		t.Fatalf("lane without estimates must render not measured: %q", rendered)
	}
}

func TestReadOutcomesToleratesOnlyRecordsWithoutIdentityFields(t *testing.T) {
	refused := map[string]string{
		"junk identity":     `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","identity":{"harness":"h","context_bytes":-5},"identity_key":"junk","actual_cost":1.5}`,
		"only actual cost":  `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","actual_cost":1.5}`,
		"only identity key": `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","identity_key":"sha256:00"}`,
		"only resolved":     `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","resolved_model":"m"}`,
		"invalid identity":  `{"time":"2026-10-08T09:00:00Z","task":"stubs","target":"light","result":"ok","identity":{"physical_model":"m","harness":"h","harness_version":"1","prompt_digest":"p","context_digest":"c","context_bytes":-5}}`,
	}
	for name, line := range refused {
		path := filepath.Join(t.TempDir(), "outcomes.jsonl")
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadOutcomes(context.Background(), path); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%s: a record carrying identity fields must be validated on read, got %v", name, err)
		}
	}
}

func TestReconcileLanes_Boundary_EmptyInput(t *testing.T) {
	recs := ReconcileLanes(nil)
	if recs == nil || len(recs) != 0 {
		t.Fatalf("no outcomes reconcile to an empty, non-nil table: %#v", recs)
	}
	if got := RenderLaneReconciliation(recs); got != "no outcomes recorded for reconciliation\n" {
		t.Fatalf("empty table must say so: %q", got)
	}
}

func TestReconcileLanes_LegacyMixCountsRunsOnly(t *testing.T) {
	legacy := Outcome{Time: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Task: "stubs", Lane: "gateway-coding", Target: "light", Result: OutcomeOK}
	legacyWithCost := legacy
	legacyWithCost.ActualCost = ptrFloat64(9)
	unidentifiedWithCosts := legacyWithCost
	unidentifiedWithCosts.Identity = RunIdentity{CostEstimate: ptrFloat64(1)}
	measured := sampleOutcome("stubs", OutcomeOK)
	recs := ReconcileLanes([]Outcome{legacy, legacyWithCost, unidentifiedWithCosts, measured, {Task: "stubs", Target: "light", Result: OutcomeOK}})
	if len(recs) != 2 || recs[0].Lane != "default" || recs[0].MeasuredRuns != 0 {
		t.Fatalf("an outcome without lane groups under default, unmeasured: %+v", recs)
	}
	coding := recs[1]
	if coding.Runs != 4 || coding.MeasuredRuns != 1 || coding.ActualCost != 0.048 || coding.EstimatedCost != 0.05 {
		t.Fatalf("unidentified records count as runs but never as measured costs: %+v", coding)
	}
	if coding.ErrorRatio == nil || math.Abs(*coding.ErrorRatio-(-0.04)) > 1e-9 {
		t.Fatalf("error ratio over the measured run: %v", coding.ErrorRatio)
	}
	rendered := RenderLaneReconciliation(recs)
	for _, want := range []string{"lane default: estimate-error not measured (runs: 1,", "lane gateway-coding: estimate-error -0.0020 (-4.0%)", "measured runs: 1 of 4"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered table lacks %q:\n%s", want, rendered)
		}
	}
}

func TestReconcileLanes_ZeroEstimateHasNoRatio(t *testing.T) {
	o := sampleOutcome("stubs", OutcomeOK)
	o.Identity.CostEstimate = ptrFloat64(0)
	o.ActualCost = ptrFloat64(0.25)
	recs := ReconcileLanes([]Outcome{o})
	if len(recs) != 1 || recs[0].MeasuredRuns != 1 || recs[0].EstimateError != 0.25 || recs[0].ErrorRatio != nil {
		t.Fatalf("a zero estimate is measured, with no ratio: %+v", recs)
	}
	if got := FormatEstimateError(0, 0.25); got != "+0.2500" {
		t.Fatalf("a zero estimate renders the signed error without a percentage: %q", got)
	}
	if got := FormatEstimateError(0.2, 0.25); got != "+0.0500 (+25.0%)" {
		t.Fatalf("a positive estimate renders the percentage: %q", got)
	}
}

func TestTallyCosts_MeasuresOnlyIdentifiedRunsWithBothCosts(t *testing.T) {
	noEstimate := sampleOutcome("stubs", OutcomeOK)
	noEstimate.Identity.CostEstimate = nil
	noActual := sampleOutcome("stubs", OutcomeOK)
	noActual.ActualCost = nil
	tally := TallyCosts([]Outcome{noEstimate, noActual, sampleOutcome("stubs", OutcomeOK)})
	diff, ok := tally.EstimateError()
	if tally.Runs != 3 || tally.MeasuredRuns != 1 || !ok || math.Abs(diff-(-0.002)) > 1e-9 {
		t.Fatalf("tally: %+v %v %v", tally, diff, ok)
	}
	if _, ok := TallyCosts(nil).EstimateError(); ok {
		t.Fatal("an empty tally must not report an estimate error")
	}
}

// Encoding refuses NaN and infinity on write, so the validators are checked directly: they also
// guard records built in memory and never encoded.
func TestValidateOutcome_NonFiniteCostsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*Outcome){
		"nan estimate":      func(o *Outcome) { o.Identity.CostEstimate = ptrFloat64(math.NaN()) },
		"infinite estimate": func(o *Outcome) { o.Identity.CostEstimate = ptrFloat64(math.Inf(1)) },
		"nan actual":        func(o *Outcome) { o.ActualCost = ptrFloat64(math.NaN()) },
		"infinite actual":   func(o *Outcome) { o.ActualCost = ptrFloat64(math.Inf(1)) },
	} {
		o := sampleOutcome("stubs", OutcomeOK)
		mutate(&o)
		if err := ValidateOutcome(o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := ValidateOutcome(sampleOutcome("stubs", OutcomeOK)); err != nil {
		t.Fatalf("finite costs refused: %v", err)
	}
}
