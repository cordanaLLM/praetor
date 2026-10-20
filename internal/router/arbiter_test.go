package router

import (
	"strings"
	"testing"
)

func TestFallbackCycleAndCooldownAtMaximumThreshold(t *testing.T) {
	cfg := &RoutingConfig{Governance: GovernancePolicy{QuotaReadingMaxAge: DefaultQuotaReadingMaxAge}, Tiers: map[string]Tier{"a": {FallbackTier: "b"}, "b": {FallbackTier: "a"}}}
	a := NewModelCapacityArbiter(cfg, nil)
	if _, err := a.SelectModel("a"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle was not rejected: %v", err)
	}
	cfg.Governance.ExhaustionThresholdPercent = 85
	cfg.Tiers = map[string]Tier{"a": {Models: []ModelDescriptor{{ID: "cooling"}}}}
	a.Tracker.RecordUsage("cooling", 0, 0)
	a.Tracker.Record429("cooling")
	if _, err := a.SelectModel("a"); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatal("85 percent threshold admitted cooling model")
	}
}

func TestOrthogonalFallbackIsDeterministic(t *testing.T) {
	cfg := &RoutingConfig{Governance: GovernancePolicy{QuotaReadingMaxAge: DefaultQuotaReadingMaxAge, ExhaustionThresholdPercent: 80}, Tiers: map[string]Tier{
		"z": {Models: []ModelDescriptor{{ID: "last", Family: FamilyGoogle}}},
		"a": {Models: []ModelDescriptor{{ID: "first", Family: FamilyGoogle}}},
	}}
	for i := 0; i < 30; i++ {
		arbiter := NewModelCapacityArbiter(cfg, nil)
		arbiter.Tracker.RecordUsage("first", 0, 0)
		arbiter.Tracker.RecordUsage("last", 0, 0)
		m, err := arbiter.SelectOrthogonalAuditor(FamilyAnthropic, "missing")
		if err != nil || m.ID != "first" {
			t.Fatalf("unstable selection: %+v %v", m, err)
		}
	}
}
