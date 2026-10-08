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

func TestModelsOutcomeCLIRecordsAndReadsBack(t *testing.T) {
	log := filepath.Join(t.TempDir(), "routing", "outcomes.jsonl")
	args := []string{"outcome", "--task=implement", "--lane=gateway-coding", "--target=light", "--result=ok", "--duration-ms=900", "--outcome-log=" + log}
	if _, err := captureStdout(t, func() error { return runModels(args) }); err != nil {
		t.Fatal(err)
	}
	recorded, err := router.ReadOutcomes(context.Background(), log)
	if err != nil || len(recorded) != 1 || recorded[0].Task != "implement" || recorded[0].Result != router.OutcomeOK || recorded[0].DurationMS != 900 {
		t.Fatalf("readback: %+v %v", recorded, err)
	}
	for name, bad := range map[string][]string{
		"unknown result":     {"outcome", "--task=implement", "--target=light", "--result=great", "--outcome-log=" + log},
		"missing task":       {"outcome", "--target=light", "--result=ok", "--outcome-log=" + log},
		"route flag misuse":  {"route", "--task=implement", "--result=ok"},
		"outcome token flag": {"outcome", "--task=implement", "--target=light", "--result=ok", "--input-tokens=5", "--outcome-log=" + log},
		"sync flag misuse":   {"route", "--task=implement", "--probe-aliases=false"},
	} {
		if _, err := captureStdout(t, func() error { return runModels(bad) }); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if again, err := router.ReadOutcomes(context.Background(), log); err != nil || len(again) != 1 {
		t.Fatalf("a refused record changed the log: %+v %v", again, err)
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
	for _, output := range []string{"40", "41"} {
		request := router.TaskRequest{Task: "implement", InputTokens: 60, OutputTokens: 40, RequireObservedCapacity: true}
		if output == "41" {
			request.OutputTokens = 41
		}
		core, coreErr := router.NewModelCapacityArbiter(cfg, tracker).SelectForTask(ctx, request)
		args := []string{"route", "--config=" + path, "--usage=" + usagePath, "--task=implement", "--input-tokens=60", "--output-tokens=" + output}
		out, cliErr := captureStdout(t, func() error { return runModels(args) })
		if (cliErr != nil) != (coreErr != nil) {
			t.Fatalf("CLI/core disagreement: %v / %v", cliErr, coreErr)
		}
		if coreErr != nil {
			continue
		}
		var result modelRouteReport
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result.Model.ID != core.Model.ID || !result.QuotaLimitsKnown || result.ProjectedHeadroom == nil || *result.ProjectedHeadroom != *core.ProjectedHeadroom {
			t.Fatalf("CLI lost projected capacity: %s", out)
		}
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
	if result.Model.ID != "cheap" || result.EstimatedCost != .002 || result.CapacitySource != "unobserved" || result.CapacityObserved || result.RecordedHeadroom != nil {
		t.Fatalf("route outcome: %s", out)
	}
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

	// Positive: the row decides the register, and its budget seeds a missing output estimate.
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

	// Boundary: a routing label without a row falls back to surfaces.agent, no budget.
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
