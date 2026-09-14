package main

import (
	"context"
	"encoding/json"
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
	cases := [][]string{{"list", "--task=implement"}, {"route", "extra"}, {"route", "--discover-local=false"}, {"route", "--task=unknown", "--input-tokens=1"}, {"route", "--task=implement"}, {"route", "--task=implement", "--input-tokens=1000000001"}, {"route", "--task=implement", "--input-tokens=1", "--capabilities=tools,,json"}}
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
