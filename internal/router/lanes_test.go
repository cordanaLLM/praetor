package router

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const laneCatalog = `version: 1
gateway:
  address: http://gateway.example.invalid/v1
  key_env: GATEWAY_KEY
lanes:
  gateway-coding:
    harness: coding-harness
    command: [coding-harness, run, --model, "{target}", --task, "{task}"]
  free-cli:
    harness: free-cli
    command: [free-cli, --prompt-file, "{task}.md"]
tiers:
  heavy:
    target_tasks: [synthesis, signoff]
    lane: gateway-coding
    models:
      - {id: heavy-alias, family: openai, provider: gw, alias: reasoning, alias_status: answers, cost_per_m_in: 1, cost_per_m_out: 1}
  light:
    target_tasks: [stubs, audits]
    lane: gateway-coding
    models:
      - {id: light-alias, family: openai, provider: gw, alias: light, alias_status: answers, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: free-model, family: google, lane: free-cli, source: local, cost_per_m_in: 0, cost_per_m_out: 0}
governance:
  exhaustion_threshold_percent: 80
`

func loadLaneCatalog(t *testing.T) *RoutingConfig {
	t.Helper()
	cfg, err := ParseRoutingConfig([]byte(laneCatalog), "lanes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEveryDeclaredLabelRoutesToAnExecutableLane(t *testing.T) {
	cfg := loadLaneCatalog(t)
	arbiter := NewModelCapacityArbiter(cfg, nil)
	labels := DeclaredTaskLabels(cfg)
	if len(labels) != 4 {
		t.Fatalf("fixture labels: %v", labels)
	}
	for _, label := range labels {
		route := routeTask(t, arbiter, TaskRequest{Task: label})
		if route.Lane == nil || route.Lane.Harness == "" || len(route.Lane.Command) == 0 || route.LaneNote != "" {
			t.Fatalf("%s has no executable lane: %+v note %q", label, route.Lane, route.LaneNote)
		}
		for _, arg := range route.Lane.Command {
			if strings.Contains(arg, "{") {
				t.Fatalf("%s left a placeholder in %q", label, arg)
			}
		}
	}
}

func TestLaneCommandCarriesAliasAndTaskAsSingleArguments(t *testing.T) {
	cfg := loadLaneCatalog(t)
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "synthesis"})
	want := []string{"coding-harness", "run", "--model", "reasoning", "--task", "synthesis"}
	if route.Lane.Name != "gateway-coding" || route.Lane.Target != "reasoning" || !reflect.DeepEqual(route.Lane.Command, want) {
		t.Fatalf("lane wrong: %+v", route.Lane)
	}
	lane, ok := ResolveLane(cfg, "heavy", cfg.Tiers["heavy"].Models[0], "two words; rm -rf")
	if !ok || lane.Command[5] != "two words; rm -rf" || len(lane.Command) != len(want) {
		t.Fatalf("a task label must stay one argument: %+v", lane)
	}
}

func TestModelLaneOverridesTierLane(t *testing.T) {
	cfg := loadLaneCatalog(t)
	model := cfg.Tiers["light"].Models[1]
	lane, ok := ResolveLane(cfg, "light", model, "stubs")
	if !ok || lane.Name != "free-cli" || lane.Target != "free-model" || lane.Command[2] != "stubs.md" {
		t.Fatalf("model lane ignored: %+v", lane)
	}
}

func TestRouteWithoutLaneSaysSo(t *testing.T) {
	route := routeTask(t, NewModelCapacityArbiter(taskConfig(costTaskModel("only", 1, 1)), nil), TaskRequest{Task: "implement", InputTokens: 1})
	if route.Lane != nil || !strings.Contains(route.LaneNote, "no lane declared") {
		t.Fatalf("missing lane hidden: %+v", route)
	}
}

func TestLaneValidation(t *testing.T) {
	bad := map[string]string{
		"undeclared tier lane":  strings.Replace(laneCatalog, "lane: gateway-coding\n    models:\n      - {id: heavy", "lane: nowhere\n    models:\n      - {id: heavy", 1),
		"undeclared model lane": strings.Replace(laneCatalog, "lane: free-cli", "lane: nowhere", 1),
		"unknown placeholder":   strings.Replace(laneCatalog, `"{task}.md"`, `"{taskname}.md"`, 1),
		"empty command":         strings.Replace(laneCatalog, "command: [free-cli, --prompt-file, \"{task}.md\"]", "command: []", 1),
		"empty argument":        strings.Replace(laneCatalog, "--prompt-file, ", `"", `, 1),
		"unknown lane field":    strings.Replace(laneCatalog, "harness: free-cli", "harness: free-cli\n    shell: true", 1),
	}
	for name, body := range bad {
		if _, err := ParseRoutingConfig([]byte(body), name); !errors.Is(err, ErrInvalidRoutingConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	long := make([]string, MaxLaneArgs+1)
	for i := range long {
		long[i] = "x"
	}
	cfg := loadLaneCatalog(t)
	cfg.Lanes["free-cli"] = Lane{Harness: "free-cli", Command: long}
	if err := ValidateRoutingConfig(cfg); !errors.Is(err, ErrInvalidRoutingConfig) {
		t.Fatal("oversized command accepted")
	}
	cfg.Lanes["free-cli"] = Lane{Harness: "free-cli", Command: long[:MaxLaneArgs]}
	if err := ValidateRoutingConfig(cfg); err != nil {
		t.Fatalf("command at the bound refused: %v", err)
	}
}

func TestLoadedAliasCatalogRoutesOnlyAnsweringAliases(t *testing.T) {
	cfg := loadLaneCatalog(t)
	cfg.Tiers["light"].Models[0].AliasStatus = AliasUnanswered
	cfg.Tiers["light"].Models[0].AliasReason = "HTTP 400: Invalid model name passed"
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "stubs"})
	if route.Model.ID != "free-model" || len(route.Skipped) != 1 || route.Skipped[0].Model != "light-alias" {
		t.Fatalf("dead alias not skipped with its reason: %+v", route)
	}
	if !strings.Contains(route.Skipped[0].Reason, "Invalid model name passed") {
		t.Fatalf("reason lost: %+v", route.Skipped)
	}
	if _, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), TaskRequest{Task: "audits", Capabilities: []string{"none-declared"}}); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("capability filter lost: %v", err)
	}
}
