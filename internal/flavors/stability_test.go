// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package flavors

import (
	"os"
	"path/filepath"
	"testing"
)

// BUG-907: stability is informational by contract (docs/guides/releasing.md). These tests pin
// that contract: the label reaches the plan, never decides an action, and loads whatever its
// value, so a reader cannot mistake it for a gate that update_frequency manual provides.

// stabilityLabels are labels a flavor may declare: the four the dogfood config uses, a
// label no config uses, and the undeclared value.
var stabilityLabels = []string{"experimental", "pre-release", "stable", "enterprise-stable", "frozen", ""}

// stabilityConfig declares one flavor per label, all automatic and all following one source.
func stabilityConfig() *Config {
	cfg := &Config{Version: 1, Flavors: make(map[string]Flavor, len(stabilityLabels))}
	for _, label := range stabilityLabels {
		cfg.Flavors["f-"+label] = Flavor{SourceRef: "refs/heads/main", Stability: label}
	}
	return cfg
}

func TestPlanSelected_Positive_StabilityReachesThePlanTrimmed(t *testing.T) {
	cfg := &Config{Version: 1, Flavors: map[string]Flavor{
		"edge": {SourceRef: "refs/heads/main", Stability: "  pre-release\t"},
	}}
	plan := PlanTransitions(cfg, nil, resolveMain)
	if len(plan) != 1 || plan[0].Stability != "pre-release" {
		t.Fatalf("the declared label must reach the plan trimmed, got %+v", plan)
	}
}

func TestPlanSelected_Negative_StabilityNeverChangesTheAction(t *testing.T) {
	unresolved := func(string) (string, bool) { return "", false }
	scenarios := []struct {
		name    string
		tag     string // commit every flavor tag points at; empty means no tag yet
		resolve RefResolver
		want    string
	}{
		{"absent tag", "", resolveMain, ActionCreate},
		{"stale tag", "old", resolveMain, ActionUpdate},
		{"current tag", "c0ffee", resolveMain, ActionNoop},
		{"unresolved source", "old", unresolved, ActionUnresolved},
	}
	cfg := stabilityConfig()
	for _, sc := range scenarios {
		current := make(map[string]string, len(cfg.Flavors))
		for name := range cfg.Flavors {
			if sc.tag != "" {
				current[name] = sc.tag
			}
		}
		got := actions(PlanTransitions(cfg, current, sc.resolve))
		if len(got) != len(stabilityLabels) {
			t.Fatalf("%s: planned %d flavors, want %d", sc.name, len(got), len(stabilityLabels))
		}
		for name, action := range got {
			if action != sc.want {
				t.Fatalf("%s: flavor %s planned %q, want %q: stability must not decide the action",
					sc.name, name, action, sc.want)
			}
		}
	}
}

func TestLoadConfig_Boundary_AnyStabilityValueLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flavors.yaml")
	body := "version: 1\nflavors:\n" +
		"  a: {source_ref: refs/heads/main, stability: \"\"}\n" +
		"  b: {source_ref: refs/heads/main, stability: \"   \"}\n" +
		"  c: {source_ref: refs/heads/main, stability: \"Stable (LTS) 2026\"}\n" +
		"  d: {source_ref: refs/heads/main}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a free-form or blank stability must load, got %v", err)
	}
	want := map[string]string{"a": "", "b": "", "c": "Stable (LTS) 2026", "d": ""}
	for _, tr := range PlanTransitions(cfg, nil, resolveMain) {
		if tr.Stability != want[tr.FlavorName] || tr.Action != ActionCreate {
			t.Fatalf("flavor %s: stability %q action %q, want %q and %q",
				tr.FlavorName, tr.Stability, tr.Action, want[tr.FlavorName], ActionCreate)
		}
	}
}
