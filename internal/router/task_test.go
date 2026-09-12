package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

func taskModel(id string, input, output float64, capabilities ...string) ModelDescriptor {
	return ModelDescriptor{ID: id, Family: FamilyOpenAI, CostPerMIn: input, CostPerMOut: output,
		CostRatesDeclared: true, RPMLimit: 10, TPMLimit: 1000, Capabilities: capabilities}
}

func taskConfig(models ...ModelDescriptor) *RoutingConfig {
	return &RoutingConfig{Version: 1, Tiers: map[string]Tier{"work": {TargetTasks: []string{"implement"}, Models: models}},
		Governance: GovernancePolicy{ExhaustionThresholdPercent: 80}}
}

func routeTask(t *testing.T, arbiter *ModelCapacityArbiter, request TaskRequest) *TaskRoute {
	t.Helper()
	route, err := arbiter.SelectForTask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestTaskRoutingEligibilityAndCost(t *testing.T) {
	cfg := taskConfig(taskModel("expensive", 10, 10, "tools", "json"), taskModel("cheap-incapable", 0, 0, "tools"), taskModel("eligible", 1, 2, "tools", "json"))
	cfg.Tiers["wrong-task"] = Tier{TargetTasks: []string{"docstrings"}, Models: []ModelDescriptor{taskModel("free-unqualified", 0, 0, "tools", "json")}}
	request := TaskRequest{Task: "implement", Capabilities: []string{"tools", "json"}, InputTokens: 1000, OutputTokens: 500}
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), request)
	if route.Model.ID != "eligible" || route.EstimatedCost != .002 || route.Tier != "work" {
		t.Fatalf("eligibility/cost mismatch: %+v", route)
	}
	if route.CapacityObserved || route.RecordedHeadroom != nil {
		t.Fatal("absent usage claimed observed capacity")
	}
	route.Model.Capabilities[0] = "mutated"
	route.Request.Capabilities[0] = "mutated"
	if cfg.Tiers["work"].Models[2].Capabilities[0] != "tools" || request.Capabilities[0] != "tools" {
		t.Fatal("route aliases caller capability slices")
	}
}

func TestTaskRoutingWeightedCostAndDeterministicTies(t *testing.T) {
	cfg := taskConfig(taskModel("input-cheap", 1, 10), taskModel("output-cheap", 2, 1))
	arbiter := NewModelCapacityArbiter(cfg, nil)
	if got := routeTask(t, arbiter, TaskRequest{Task: "implement", InputTokens: 1_000_000}); got.Model.ID != "input-cheap" {
		t.Fatal("input estimate ignored")
	}
	if got := routeTask(t, arbiter, TaskRequest{Task: "implement", OutputTokens: 1_000_000}); got.Model.ID != "output-cheap" {
		t.Fatal("output estimate ignored")
	}
	cfg.Tiers["work"] = Tier{TargetTasks: []string{"implement"}, Models: []ModelDescriptor{taskModel("zeta", 0, 0), taskModel("beta", 0, 0)}}
	cfg.Tiers["other"] = Tier{TargetTasks: []string{"implement"}, Models: []ModelDescriptor{taskModel("alpha", 0, 0)}}
	for i := 0; i < 10; i++ {
		if got := routeTask(t, arbiter, TaskRequest{Task: "implement", InputTokens: 1}); got.Model.ID != "alpha" {
			t.Fatalf("nondeterministic zero-cost tie: %s", got.Model.ID)
		}
	}
}

func TestTaskRoutingCapacityThresholdAndCooldown(t *testing.T) {
	cfg := taskConfig(taskModel("cheap", 1, 1), taskModel("reserve", 2, 2))
	tracker := NewLimitTracker()
	for i := 0; i < 8; i++ {
		tracker.RecordUsage("cheap", 0, 0)
	}
	arbiter := NewModelCapacityArbiter(cfg, tracker)
	request := TaskRequest{Task: "implement", InputTokens: 1}
	if got := routeTask(t, arbiter, request); got.Model.ID != "cheap" || !got.CapacityObserved {
		t.Fatal("exact threshold should remain eligible")
	}
	tracker.RecordUsage("cheap", 0, 0)
	if got := routeTask(t, arbiter, request); got.Model.ID != "reserve" {
		t.Fatal("exhausted RPM selected")
	}
	tracker.Record429("reserve")
	if _, err := arbiter.SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("cooldown ignored: %v", err)
	}
	cfg.Governance.ExhaustionThresholdPercent = 100
	tracker.RecordUsage("cheap", 0, 0)
	if _, err := arbiter.SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("fully exhausted/cooling model selected at threshold100: %v", err)
	}
}

func TestTaskRoutingTPMAndNoIneligibleFallback(t *testing.T) {
	cfg := taskConfig(taskModel("primary", 1, 1))
	tier := cfg.Tiers["work"]
	tier.FallbackTier = "docs"
	cfg.Tiers["work"] = tier
	cfg.Tiers["docs"] = Tier{TargetTasks: []string{"docstrings"}, Models: []ModelDescriptor{taskModel("incapable-fallback", 0, 0)}}
	tracker := NewLimitTracker()
	tracker.RecordUsage("primary", 801, 0)
	arbiter := NewModelCapacityArbiter(cfg, tracker)
	if _, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1}); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("undeclared fallback task accepted: %v", err)
	}
	if _, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "unknown", InputTokens: 1}); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("unknown task accepted: %v", err)
	}
}

func TestTaskRoutingRequiresObservedCapacityWhenRequested(t *testing.T) {
	cfg := taskConfig(taskModel("known", 1, 1), taskModel("missing-cheap", 0, 0))
	request := TaskRequest{Task: "implement", InputTokens: 1, RequireObservedCapacity: true}
	if _, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("missing usage accepted: %v", err)
	}
	snapshot := &UsageSnapshot{Version: 1, CapturedAt: time.Now().UTC(), Models: map[string]ModelUsage{"known": {}}}
	tracker, err := TrackerFromSnapshot(cfg, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	route := routeTask(t, NewModelCapacityArbiter(cfg, tracker), request)
	if route.Model.ID != "known" || route.RecordedHeadroom == nil || *route.RecordedHeadroom != 1 {
		t.Fatal("explicit zero observation was lost")
	}
	snapshot.Models["known"] = ModelUsage{CurrentRPM: 100}
	if got := tracker.GetUsage("known"); got.CurrentRPM != 0 {
		t.Fatal("tracker aliases caller snapshot")
	}
}

func TestTaskRoutingInputBounds(t *testing.T) {
	arbiter := NewModelCapacityArbiter(taskConfig(taskModel("one", 1, 1)), nil)
	if got := routeTask(t, arbiter, TaskRequest{Task: "implement", InputTokens: MaxTaskTokens}); got.EstimatedCost != 1000 {
		t.Fatal("exact token bound rejected")
	}
	for _, request := range []TaskRequest{{Task: "implement"}, {Task: "", InputTokens: 1}, {Task: "implement", InputTokens: -1}, {Task: "implement", OutputTokens: MaxTaskTokens + 1}, {Task: "implement", InputTokens: 1, Capabilities: make([]string, MaxRoutingTags+1)}} {
		if _, err := arbiter.SelectForTask(context.Background(), request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := arbiter.SelectForTask(ctx, TaskRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	var nilContext context.Context
	if _, err := arbiter.SelectForTask(nilContext, TaskRequest{}); err == nil {
		t.Fatal("nil context accepted")
	}
	var absent *ModelCapacityArbiter
	if _, err := absent.SelectForTask(context.Background(), TaskRequest{}); err == nil {
		t.Fatal("nil arbiter accepted")
	}
}

func TestTaskRoutingConfigAndCostBounds(t *testing.T) {
	cfg := &RoutingConfig{Version: 1, Tiers: make(map[string]Tier)}
	for i := 0; i < MaxRoutingTiers; i++ {
		tier := Tier{TargetTasks: []string{"implement"}}
		for j := 0; j < MaxModelsPerTier; j++ {
			tier.Models = append(tier.Models, taskModel(fmt.Sprintf("model-%02d-%02d", i, j), 1, 1))
		}
		cfg.Tiers[fmt.Sprintf("tier-%02d", i)] = tier
	}
	if got := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "implement", InputTokens: 1}); got.Model.ID != "model-00-00" {
		t.Fatal("exact candidate bound failed")
	}
	cfg.Tiers["overflow"] = Tier{}
	if err := ValidateRoutingConfig(cfg); err == nil {
		t.Fatal("too many tiers accepted")
	}
	for _, cost := range []float64{-1, math.Inf(1), math.NaN()} {
		if err := ValidateRoutingConfig(taskConfig(taskModel("bad", cost, 1))); err == nil {
			t.Fatalf("invalid price accepted: %v", cost)
		}
	}
	missing := taskModel("undeclared", 0, 0)
	missing.CostRatesDeclared = false
	if err := ValidateRoutingConfig(taskConfig(missing)); err == nil {
		t.Fatal("missing prices interpreted as free")
	}
}
