package flavors

import (
	"testing"
)

func TestLoadConfig_Dogfood(t *testing.T) {
	cfg, err := LoadConfig("../../.config/flavors.yaml")
	if err != nil {
		t.Fatalf("failed to load .config/flavors.yaml: %v", err)
	}

	if cfg.Version != 1 {
		t.Fatalf("expected version 1, got %d", cfg.Version)
	}

	expectedFlavors := []string{"bleeding", "edge", "latest", "lts"}
	for _, ef := range expectedFlavors {
		if _, ok := cfg.Flavors[ef]; !ok {
			t.Fatalf("missing expected flavor: %s", ef)
		}
	}
}

func TestPlanTransitions(t *testing.T) {
	cfg := &Config{
		Version: 1,
		Flavors: map[string]Flavor{
			"bleeding": {Description: "Bleeding edge"},
			"latest":   {Description: "Latest stable"},
		},
	}

	current := map[string]string{
		"latest": "v1.0.0",
	}

	transitions := PlanTransitions(cfg, current, "a1b2c3d", "1.0.1")
	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(transitions))
	}

	for _, tr := range transitions {
		if tr.FlavorName == "bleeding" {
			if tr.Action != "create" {
				t.Fatalf("expected create for bleeding, got %s", tr.Action)
			}
			if tr.TargetRef != "v1.0.1-bleeding.a1b2c3d" {
				t.Fatalf("unexpected target ref: %s", tr.TargetRef)
			}
		}
		if tr.FlavorName == "latest" {
			if tr.Action != "update" {
				t.Fatalf("expected update for latest, got %s", tr.Action)
			}
			if tr.TargetRef != "1.0.1" {
				t.Fatalf("unexpected target ref: %s", tr.TargetRef)
			}
		}
	}
}
