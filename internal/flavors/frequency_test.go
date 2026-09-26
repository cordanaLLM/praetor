package flavors

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// frequencyConfig declares one flavor per update frequency, all following one source.
func frequencyConfig() *Config {
	return &Config{Version: 1, Flavors: map[string]Flavor{
		"bleeding": {SourceRef: "refs/heads/main", UpdateFrequency: FrequencyOnPush, Stability: "experimental"},
		"edge":     {SourceRef: "refs/heads/main", UpdateFrequency: FrequencyManual, Stability: "pre-release"},
		"latest":   {SourceRef: "refs/heads/main", UpdateFrequency: FrequencyOnRelease, Stability: "stable"},
		"lts":      {SourceRef: "refs/heads/main", UpdateFrequency: FrequencyOnPatch},
		"plain":    {SourceRef: "refs/heads/main"},
	}}
}

func resolveMain(string) (string, bool) { return "c0ffee", true }

func actions(transitions []TagTransition) map[string]string {
	got := make(map[string]string, len(transitions))
	for _, tr := range transitions {
		got[tr.FlavorName] = tr.Action
	}
	return got
}

func TestPlanSelected_Positive_ManualMovesOnlyWhenNamed(t *testing.T) {
	cfg := frequencyConfig()
	all := actions(PlanTransitions(cfg, nil, resolveMain))
	want := map[string]string{"bleeding": ActionCreate, "edge": ActionHeld, "latest": ActionCreate, "lts": ActionCreate, "plain": ActionCreate}
	for name, action := range want {
		if all[name] != action {
			t.Fatalf("%s = %q, want %q (plan %v)", name, all[name], action, all)
		}
	}
	named := PlanSelected(cfg, map[string]string{"edge": "old"}, resolveMain, []string{"edge"})
	if len(named) != 1 || named[0].FlavorName != "edge" || named[0].Action != ActionUpdate {
		t.Fatalf("naming edge must plan exactly edge as an update, got %+v", named)
	}
	if named[0].UpdateFrequency != FrequencyManual || named[0].Stability != "pre-release" {
		t.Fatalf("declared values not carried into the plan: %+v", named[0])
	}
}

func TestPlanSelected_Negative_UnknownFrequencyAndFlavorAreRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flavors.yaml")
	for _, frequency := range []string{"weekly", "Manual", "on-push"} {
		body := "version: 1\nflavors:\n  edge: {source_ref: refs/heads/main, update_frequency: " + frequency + "}\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "unknown update_frequency") {
			t.Fatalf("update_frequency %q must be refused at load, got %v", frequency, err)
		}
	}
	if got := UnknownFlavors(frequencyConfig(), []string{"edge", "egde", "nightly"}); !slices.Equal(got, []string{"egde", "nightly"}) {
		t.Fatalf("unknown = %q", got)
	}
	if got := UnknownFlavors(nil, []string{"edge"}); !slices.Equal(got, []string{"edge"}) {
		t.Fatalf("a missing config declares nothing, got %q", got)
	}
}

func TestPlanSelected_Boundary_HeldBeatsUnresolvedAndBlankIsAutomatic(t *testing.T) {
	cfg := frequencyConfig()
	unresolved := actions(PlanTransitions(cfg, nil, func(string) (string, bool) { return "", false }))
	if unresolved["edge"] != ActionHeld || unresolved["bleeding"] != ActionUnresolved {
		t.Fatalf("a held flavor is held whatever its source does: %v", unresolved)
	}
	for _, empty := range [][]string{nil, {}} {
		if got := PlanSelected(cfg, nil, resolveMain, empty); len(got) != len(cfg.Flavors) {
			t.Fatalf("an empty selection plans every flavor, got %d", len(got))
		}
	}
	path := filepath.Join(t.TempDir(), "flavors.yaml")
	body := "version: 1\nflavors:\n  a: {update_frequency: \" manual \"}\n  b: {update_frequency: \"\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("padded manual and blank frequencies must load: %v", err)
	}
	if got := actions(PlanTransitions(loaded, nil, resolveMain)); got["a"] != ActionHeld || got["b"] != ActionCreate {
		t.Fatalf("padded manual must hold and blank must move: %v", got)
	}
}

func TestLoadConfig_DogfoodFrequenciesAreConsumed(t *testing.T) {
	cfg, err := LoadConfig("../../.config/flavors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Flavors["edge"].IsManual() || cfg.Flavors["bleeding"].IsManual() {
		t.Fatalf("edge is declared manual and bleeding on_push: %+v", cfg.Flavors)
	}
}
