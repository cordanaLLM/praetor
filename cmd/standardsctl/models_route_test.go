package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/router"
)

const cliRouteFixture = `version: 1
tiers:
  work:
    target_tasks: [implement]
    models:
      - {id: cheap, family: openai, rpm_limit: 10, tpm_limit: 1000, cost_per_m_in: 1, cost_per_m_out: 2, capabilities: [tools]}
      - {id: reserve, family: google, rpm_limit: 10, tpm_limit: 1000, cost_per_m_in: 2, cost_per_m_out: 2, capabilities: [tools]}
governance:
  exhaustion_threshold_percent: 80
`

func writeRouteCLIInput(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const cliLaneFixture = `version: 1
gateway:
  address: https://gateway.example.invalid/v1
lanes:
  gateway-coding:
    harness: coding-harness
    command: [coding-harness, run, --model, "{target}", --task, "{task}"]
tiers:
  work:
    target_tasks: [implement, review]
    lane: gateway-coding
    models:
      - {id: pinned-cheap, family: openai, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: gw-light, family: openai, provider: gw, alias: light, alias_status: answers, cost_per_m_in: 1, cost_per_m_out: 1}
`

type cliRouteLane struct {
	Lane struct {
		Name    string   `json:"name"`
		Harness string   `json:"harness"`
		Target  string   `json:"target"`
		Command []string `json:"command"`
	} `json:"lane"`
	Model   struct{ ID string } `json:"model"`
	Skipped []struct {
		Model, Reason string
	} `json:"skipped"`
}

func TestModelsRouteCLITierOnlyReturnsExecutableLane(t *testing.T) {
	path := writeRouteCLIInput(t, cliLaneFixture)
	for _, label := range []string{"implement", "review"} {
		out, err := captureStdout(t, func() error { return runModels([]string{"route", "--config=" + path, "--task=" + label}) })
		if err != nil {
			t.Fatalf("%s: tier-only route refused: %v", label, err)
		}
		var report cliRouteLane
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatal(err)
		}
		want := []string{"coding-harness", "run", "--model", "light", "--task", label}
		if report.Model.ID != "gw-light" || report.Lane.Harness != "coding-harness" || report.Lane.Target != "light" || strings.Join(report.Lane.Command, " ") != strings.Join(want, " ") {
			t.Fatalf("%s: lane wrong: %+v", label, report)
		}
		if len(report.Skipped) != 1 || report.Skipped[0].Model != "pinned-cheap" {
			t.Fatalf("%s: the cheaper pinned model must be reported as skipped: %+v", label, report.Skipped)
		}
	}
}

func TestModelsRouteCLIUnansweredAliasFailsClosedWithItsReason(t *testing.T) {
	body := strings.Replace(cliLaneFixture, "alias_status: answers", "alias_status: unanswered, alias_reason: 'HTTP 400: Invalid model name passed'", 1)
	path := writeRouteCLIInput(t, body)
	_, err := captureStdout(t, func() error { return runModels([]string{"route", "--config=" + path, "--task=implement"}) })
	if !errors.Is(err, router.ErrNoEligibleModel) {
		t.Fatalf("a dead gateway must fail closed, not fall back to a pinned model: %v", err)
	}
	for _, want := range []string{"gw-light: alias did not answer the gateway probe: HTTP 400: Invalid model name passed", "pinned-cheap: pinned model"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error lacks %q: %v", want, err)
		}
	}
}

func validOutcomeCLIArgs(log string) []string {
	return []string{
		"outcome",
		"--task=implement",
		"--lane=gateway-coding",
		"--target=light",
		"--physical-model=claude-3-5-sonnet",
		"--harness=praetor-agent",
		"--harness-version=1.0.0",
		"--prompt-digest=sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"--context-digest=sha256:2222222222222222222222222222222222222222222222222222222222222222",
		"--context-bytes=1024",
		"--tools=read,write",
		"--rounds=1",
		"--retries=0",
		"--cost-estimate=0.05",
		"--actual-cost=0.06",
		"--result=ok",
		"--duration-ms=900",
		"--outcome-log=" + log,
	}
}

func TestModelsOutcomeCLIRecordsAndReadsBack(t *testing.T) {
	log := filepath.Join(t.TempDir(), "routing", "outcomes.jsonl")
	args := validOutcomeCLIArgs(log)
	stdout, err := captureStdout(t, func() error { return runModels(args) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "estimate-error [gateway-coding]:") {
		t.Errorf("stdout missing lane estimate-error: %s", stdout)
	}
	recorded, err := router.ReadOutcomes(context.Background(), log)
	if err != nil || len(recorded) != 1 {
		t.Fatalf("readback failed: %+v %v", recorded, err)
	}
	assertRecordedOutcomeFields(t, recorded[0])
}

func assertRecordedOutcomeFields(t *testing.T, rec router.Outcome) {
	t.Helper()
	if rec.Task != "implement" || rec.Result != router.OutcomeOK || rec.DurationMS != 900 {
		t.Errorf("unexpected record basics: %+v", rec)
	}
	if rec.ResolvedModel != "claude-3-5-sonnet" || rec.Identity.Harness != "praetor-agent" {
		t.Errorf("unexpected record identity: %+v", rec.Identity)
	}
	if rec.IdentityKey == "" || rec.EstimateError == nil {
		t.Errorf("expected identity key and estimate error: %+v", rec)
	}
}

func TestModelsOutcomeCLIRejectsInvalidArguments(t *testing.T) {
	log := filepath.Join(t.TempDir(), "routing", "outcomes.jsonl")
	args := validOutcomeCLIArgs(log)
	if _, err := captureStdout(t, func() error { return runModels(args) }); err != nil {
		t.Fatal(err)
	}
	for name, bad := range outcomeCLIBadArgs(log) {
		if _, err := captureStdout(t, func() error { return runModels(bad) }); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if again, err := router.ReadOutcomes(context.Background(), log); err != nil || len(again) != 1 {
		t.Fatalf("a refused record changed the log: %+v %v", again, err)
	}
}

func outcomeCLIBadArgs(log string) map[string][]string {
	return map[string][]string{
		"missing identity":   {"outcome", "--task=implement", "--target=light", "--result=ok", "--outcome-log=" + log},
		"missing harness":    {"outcome", "--task=implement", "--target=light", "--result=ok", "--prompt-digest=sha256:abc", "--context-digest=sha256:def", "--outcome-log=" + log},
		"unknown result":     {"outcome", "--task=implement", "--target=light", "--result=great", "--outcome-log=" + log},
		"missing task":       {"outcome", "--target=light", "--result=ok", "--outcome-log=" + log},
		"route flag misuse":  {"route", "--task=implement", "--result=ok"},
		"outcome token flag": {"outcome", "--task=implement", "--target=light", "--result=ok", "--input-tokens=5", "--outcome-log=" + log},
		"sync flag misuse":   {"route", "--task=implement", "--probe-aliases=false"},
	}
}

func TestModelsOutcomeCLIReconcileFlag(t *testing.T) {
	log := filepath.Join(t.TempDir(), "routing", "outcomes.jsonl")
	args := validOutcomeCLIArgs(log)
	if _, err := captureStdout(t, func() error { return runModels(args) }); err != nil {
		t.Fatal(err)
	}
	recArgs := []string{"outcome", "--reconcile", "--outcome-log=" + log}
	stdout, err := captureStdout(t, func() error { return runModels(recArgs) })
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if !strings.Contains(stdout, "lane gateway-coding:") || !strings.Contains(stdout, "estimate-error") {
		t.Errorf("unexpected reconcile output: %s", stdout)
	}
}

func checkProjectedCapacityOutput(t *testing.T, arbiter *router.ModelCapacityArbiter, path, usagePath, output string, outTokens int64) {
	t.Helper()
	request := router.TaskRequest{Task: "implement", InputTokens: 60, OutputTokens: outTokens, RequireObservedCapacity: true}
	core, coreErr := arbiter.SelectForTask(context.Background(), request)
	args := []string{"route", "--config=" + path, "--usage=" + usagePath, "--task=implement", "--input-tokens=60", "--output-tokens=" + output}
	out, cliErr := captureStdout(t, func() error { return runModels(args) })
	if (cliErr != nil) != (coreErr != nil) {
		t.Fatalf("CLI/core disagreement: %v / %v", cliErr, coreErr)
	}
	if coreErr != nil {
		return
	}
	var result modelRouteReport
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Model.ID != core.Model.ID || !result.QuotaLimitsKnown || result.ProjectedHeadroom == nil || *result.ProjectedHeadroom != *core.ProjectedHeadroom {
		t.Fatalf("CLI lost projected capacity: %s", out)
	}
}

func TestModelsRouteCLIProjectedCapacityMatchesCore(t *testing.T) {
	path := writeRouteCLIInput(t, cliRouteFixture)
	usagePath := writeRouteCLIInput(t, `{"version":1,"captured_at":"2026-09-12T12:00:00Z","models":{"cheap":{"current_rpm":7,"current_tpm":700},"reserve":{"current_rpm":8,"current_tpm":0}}}`)
	ctx := context.Background()
	cfg, err := router.LoadRoutingConfigContext(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := router.LoadUsageSnapshot(ctx, usagePath)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := router.TrackerFromSnapshot(cfg, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	arbiter := router.NewModelCapacityArbiter(cfg, tracker)
	checkProjectedCapacityOutput(t, arbiter, path, usagePath, "40", 40)
	checkProjectedCapacityOutput(t, arbiter, path, usagePath, "41", 41)
}

func checkUnobservedRouteReport(t *testing.T, result modelRouteReport, out string) {
	t.Helper()
	if result.Model.ID != "cheap" || result.EstimatedCost != .002 {
		t.Fatalf("route outcome model/cost mismatch: %s", out)
	}
	if result.CapacitySource != "unobserved" || result.CapacityObserved || result.RecordedHeadroom != nil {
		t.Fatalf("route outcome capacity mismatch: %s", out)
	}
}

func TestModelsRouteCLIUsesConfiguredCostAndLabelsUnknownCapacity(t *testing.T) {
	fixture := strings.ReplaceAll(cliRouteFixture, "tpm_limit: 1000", "tpm_limit: 10000")
	path := writeRouteCLIInput(t, fixture)
	args := []string{"route", "--config=" + path, "--task=implement", "--capabilities=tools", "--input-tokens=1000", "--output-tokens=500"}
	out, err := captureStdout(t, func() error { return runModels(args) })
	if err != nil {
		t.Fatal(err)
	}
	var result modelRouteReport
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	checkUnobservedRouteReport(t, result, out)
	if len(result.ConfigSHA256) != 64 {
		t.Fatal("missing configuration provenance")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != fixture {
		t.Fatal("routing mutated its configuration")
	}
}

func TestModelsRouteCLIUsesSuppliedCapacityAndRejectsMissingObservations(t *testing.T) {
	path := writeRouteCLIInput(t, cliRouteFixture)
	usage := writeRouteCLIInput(t, `{"version":1,"captured_at":"2026-09-12T12:00:00Z","models":{"cheap":{"current_rpm":9,"current_tpm":0},"reserve":{"current_rpm":0,"current_tpm":0}}}`)
	args := []string{"route", "--config=" + path, "--usage=" + usage, "--task=implement", "--input-tokens=1"}
	out, err := captureStdout(t, func() error { return runModels(args) })
	if err != nil {
		t.Fatal(err)
	}
	var result modelRouteReport
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Model.ID != "reserve" || !result.CapacityObserved || result.CapturedAt == nil {
		t.Fatalf("supplied quota ignored: %s", out)
	}
	if err := os.WriteFile(usage, []byte(`{"version":1,"captured_at":"2026-09-12T12:00:00Z","models":{"cheap":{"current_rpm":9,"current_tpm":0}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return runModels(args) }); err == nil {
		t.Fatal("unobserved reserve incorrectly treated as free capacity")
	}
}

func TestModelsRouteCLIRejectsInvalidOrInertArguments(t *testing.T) {
	path := writeRouteCLIInput(t, cliRouteFixture)
	cases := [][]string{{"list", "--task=implement"}, {"route", "extra"}, {"route", "--discover-local=false"}, {"route", "--task=unknown", "--input-tokens=1"}, {"route", "--task=implement", "--output-tokens=-1"}, {"route", "--task=implement", "--input-tokens=1000000001"}, {"route", "--task=implement", "--input-tokens=1", "--capabilities=tools,,json"}, {"list", "--prune"}, {"route", "--prune", "--task=implement", "--input-tokens=1"}}
	for _, args := range cases {
		args = append(args, "--config="+path)
		if _, err := captureStdout(t, func() error { return runModels(args) }); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	broken := writeRouteCLIInput(t, strings.Replace(cliRouteFixture, "cost_per_m_out: 2, ", "", 1))
	if err := runModels([]string{"route", "--config=" + broken, "--task=implement", "--input-tokens=1"}); err == nil {
		t.Fatal("missing price silently became zero")
	}
}

// routeRegisterRepo builds a working directory whose manifest gives one routing label a
// register row, and enters it.
func routeRegisterRepo(t *testing.T, register string) string {
	t.Helper()
	dir := t.TempDir()
	fixture := strings.Replace(cliRouteFixture, "target_tasks: [implement]", "target_tasks: [implement, summarize]", 1)
	path := writeFixtureFile(t, dir, ".config/models/routing.yaml", fixture)
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\n"+register)
	t.Chdir(dir)
	return path
}

func routeReport(t *testing.T, args ...string) (modelRouteReport, error) {
	t.Helper()
	var report modelRouteReport
	out, err := captureStdout(t, func() error { return runModels(append([]string{"route"}, args...)) })
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode route report: %v\n%s", err, out)
	}
	return report, nil
}

func TestModelsRouteCLIReportsTheTaskRegister(t *testing.T) {
	path := routeRegisterRepo(t, "register:\n  tasks:\n    summarize: {register: social, max_tokens: 512}\n")
	report, err := routeReport(t, "--config="+path, "--task=summarize", "--input-tokens=100")
	if err != nil {
		t.Fatal(err)
	}
	if report.Register != "social" || report.RegisterSource != "tasks.summarize" || report.MaxTokens != 512 ||
		len(report.RegisterManifestSHA256) != 64 {
		t.Fatalf("register = %q from %q with %d tokens", report.Register, report.RegisterSource, report.MaxTokens)
	}
	if report.Request.OutputTokens != 512 || !strings.Contains(report.Limitations, "seeded from the 512-token budget of tasks.summarize") {
		t.Fatalf("budget must seed the output estimate and say so: %d / %s", report.Request.OutputTokens, report.Limitations)
	}
}

func TestModelsRouteCLIExplicitOutputEstimateWins(t *testing.T) {
	path := routeRegisterRepo(t, "register:\n  tasks:\n    summarize: {register: social, max_tokens: 512}\n")
	report, err := routeReport(t, "--config="+path, "--task=summarize", "--input-tokens=100")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := routeReport(t, "--config="+path, "--task=summarize", "--input-tokens=100", "--output-tokens=40")
	if err != nil || explicit.Request.OutputTokens != 40 || strings.Contains(explicit.Limitations, "seeded") {
		t.Fatalf("an explicit estimate must win: %+v, %v", explicit, err)
	}
	if explicit.Tier != report.Tier || explicit.Model.ID != report.Model.ID {
		t.Fatal("the register must never change the selected tier or model")
	}
	if explicit.RegisterManifestSHA256 != report.RegisterManifestSHA256 {
		t.Fatal("one manifest snapshot produced two register digests")
	}
}

func TestModelsRouteCLIRegisterFallbackToSurfacesAgent(t *testing.T) {
	path := routeRegisterRepo(t, "register:\n  tasks:\n    summarize: {register: social, max_tokens: 512}\n")
	report, err := routeReport(t, "--config="+path, "--task=summarize", "--input-tokens=100")
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := routeReport(t, "--config="+path, "--task=implement", "--input-tokens=100")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Register != "internal" || fallback.RegisterSource != "surfaces.agent" || fallback.MaxTokens != 0 ||
		fallback.RegisterManifestSHA256 != report.RegisterManifestSHA256 {
		t.Fatalf("fallback register = %q from %q with %d tokens", fallback.Register, fallback.RegisterSource, fallback.MaxTokens)
	}
}

func TestModelsRouteCLIRegisterNegative(t *testing.T) {
	path := routeRegisterRepo(t, "register:\n  tasks:\n    summarize: social\n")
	// An unknown task fails as before, whatever the register says.
	if _, err := routeReport(t, "--config="+path, "--task=unknown", "--input-tokens=1"); err == nil {
		t.Fatal("an undeclared task must not route")
	}
	// A manifest row for an undeclared label stops the route instead of being ignored.
	writeFixtureFile(t, ".", ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
	_, err := routeReport(t, "--config="+path, "--task=implement", "--input-tokens=1")
	mustErrContain(t, err, `register task "deploy_prod" is not a declared target_tasks label`)
}

const perTierExclusionFixture = `version: 1
gateway:
  address: https://gateway.example.invalid/v1
lanes:
  frontier-agent:
    harness: agent
    command: [agent, run, --model, "{target}"]
  gateway-coding:
    harness: coding-harness
    command: [coding-harness, run, --model, "{target}"]
tiers:
  light:
    target_tasks: [stubs]
    lane: gateway-coding
    models:
      - {id: pinned-light, family: openai, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: gw-light, family: openai, provider: gw, alias: light, alias_status: answers, cost_per_m_in: 1, cost_per_m_out: 1}
  heavy:
    target_tasks: [architecture_synthesis]
    lane: frontier-agent
    models:
      - {id: claude-3-opus, family: anthropic, cost_per_m_in: 15, cost_per_m_out: 75}
`

func TestModelsRouteCLIAliasExclusionPerTierReturnsHeavyLane(t *testing.T) {
	path := writeRouteCLIInput(t, perTierExclusionFixture)
	out, err := captureStdout(t, func() error {
		return runModels([]string{"route", "--config=" + path, "--task=architecture_synthesis"})
	})
	if err != nil {
		t.Fatalf("route architecture_synthesis failed: %v", err)
	}
	var report cliRouteLane
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Lane.Name != "frontier-agent" || report.Model.ID != "claude-3-opus" {
		t.Fatalf("expected heavy lane with claude-3-opus, got lane %q model %q", report.Lane.Name, report.Model.ID)
	}
}

func TestModelsRouteCLIAliasExclusionPerTierLightNeverReturnsPinned(t *testing.T) {
	path := writeRouteCLIInput(t, perTierExclusionFixture)
	stubsOut, err := captureStdout(t, func() error {
		return runModels([]string{"route", "--config=" + path, "--task=stubs"})
	})
	if err != nil {
		t.Fatalf("route stubs failed: %v", err)
	}
	var stubsReport cliRouteLane
	if err := json.Unmarshal([]byte(stubsOut), &stubsReport); err != nil {
		t.Fatal(err)
	}
	if stubsReport.Model.ID == "pinned-light" {
		t.Fatalf("light tier must never return its pinned model, got %s", stubsReport.Model.ID)
	}
}
