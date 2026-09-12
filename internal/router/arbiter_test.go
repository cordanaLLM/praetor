package router

import (
	"strings"
	"testing"
)

func TestFallbackCycleAndCooldownAtFullThreshold(t *testing.T) {
	cfg := &RoutingConfig{Tiers: map[string]Tier{"a": {FallbackTier: "b"}, "b": {FallbackTier: "a"}}}
	a := NewModelCapacityArbiter(cfg, nil)
	if _, err := a.SelectModel("a"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle was not rejected: %v", err)
	}
	cfg.Governance.ExhaustionThresholdPercent = 100
	cfg.Tiers = map[string]Tier{"a": {Models: []ModelDescriptor{{ID: "cooling"}}}}
	a.Tracker.Record429("cooling")
	if _, err := a.SelectModel("a"); err == nil {
		t.Fatal("100 percent threshold admitted cooling model")
	}
}

func TestOrthogonalFallbackIsDeterministic(t *testing.T) {
	cfg := &RoutingConfig{Tiers: map[string]Tier{
		"z": {Models: []ModelDescriptor{{ID: "last", Family: FamilyGoogle}}},
		"a": {Models: []ModelDescriptor{{ID: "first", Family: FamilyGoogle}}},
	}}
	for i := 0; i < 30; i++ {
		m, err := NewModelCapacityArbiter(cfg, nil).SelectOrthogonalAuditor(FamilyAnthropic, "missing")
		if err != nil || m.ID != "first" {
			t.Fatalf("unstable selection: %+v %v", m, err)
		}
	}
}
