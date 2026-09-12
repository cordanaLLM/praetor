package flavors

import (
	"os"
	"path/filepath"
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

func TestLoadConfig_Negative_MissingAndMalformed(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error for a missing flavors config")
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("flavors: [not-a-map\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := LoadConfig(bad); err == nil {
		t.Fatal("expected a parse error for malformed YAML")
	}
}

func TestSourceRefFor(t *testing.T) {
	if got := SourceRefFor(Flavor{SourceRef: "refs/heads/main"}); got != "refs/heads/main" {
		t.Fatalf("expected declared source ref, got %q", got)
	}
	if got := SourceRefFor(Flavor{SourceRef: "   "}); got != "HEAD" {
		t.Fatalf("expected HEAD fallback for a blank source ref, got %q", got)
	}
}

func TestFlavorNames_BoundaryNilAndOverflow(t *testing.T) {
	if names := FlavorNames(nil); names != nil {
		t.Fatalf("expected nil names for a nil config, got %v", names)
	}

	cfg := &Config{Flavors: map[string]Flavor{}}
	for i := 0; i < MaxFlavors+10; i++ {
		cfg.Flavors[string(rune('a'+i%26))+string(rune('a'+i/26))] = Flavor{}
	}
	if got := len(FlavorNames(cfg)); got != MaxFlavors {
		t.Fatalf("expected names truncated to %d, got %d", MaxFlavors, got)
	}
}

func TestPlanTransitions_Positive_CreateUpdateNoop(t *testing.T) {
	cfg := &Config{
		Version: 1,
		Flavors: map[string]Flavor{
			"bleeding": {SourceRef: "refs/heads/main"},
			"latest":   {SourceRef: "refs/tags/v1.0.0"},
			"lts":      {SourceRef: "refs/heads/lts-1.x"},
		},
	}
	current := map[string]string{
		"latest": "cccccccccccccccccccccccccccccccccccccccc",
		"lts":    "dddddddddddddddddddddddddddddddddddddddd",
	}
	resolve := func(ref string) (string, bool) {
		switch ref {
		case "refs/heads/main":
			return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true
		case "refs/tags/v1.0.0":
			return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true
		case "refs/heads/lts-1.x":
			return "dddddddddddddddddddddddddddddddddddddddd", true
		}
		return "", false
	}

	transitions := PlanTransitions(cfg, current, resolve)
	if len(transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d", len(transitions))
	}
	byName := map[string]TagTransition{}
	for _, tr := range transitions {
		byName[tr.FlavorName] = tr
	}

	if byName["bleeding"].Action != ActionCreate {
		t.Fatalf("expected create for an absent tag, got %s", byName["bleeding"].Action)
	}
	if byName["latest"].Action != ActionUpdate {
		t.Fatalf("expected update when the tag points elsewhere, got %s", byName["latest"].Action)
	}
	if byName["latest"].TargetCommit != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected target commit: %q", byName["latest"].TargetCommit)
	}
	if byName["lts"].Action != ActionNoop {
		t.Fatalf("expected noop when the tag already points at the target, got %s", byName["lts"].Action)
	}
}

func TestPlanTransitions_Negative_UnresolvedSourceRef(t *testing.T) {
	cfg := &Config{Flavors: map[string]Flavor{"latest": {SourceRef: "refs/tags/v9.9.9"}}}
	current := map[string]string{"latest": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}

	transitions := PlanTransitions(cfg, current, func(string) (string, bool) { return "", false })
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].Action != ActionUnresolved {
		t.Fatalf("an unresolvable source ref must not retarget the tag, got %s", transitions[0].Action)
	}
	if transitions[0].TargetCommit != "" {
		t.Fatalf("expected an empty target commit, got %q", transitions[0].TargetCommit)
	}
}

func TestPlanTransitions_Boundary_NilResolverAndEmptyConfig(t *testing.T) {
	cfg := &Config{Flavors: map[string]Flavor{"edge": {}}}
	transitions := PlanTransitions(cfg, nil, nil)
	if len(transitions) != 1 || transitions[0].Action != ActionUnresolved {
		t.Fatalf("a nil resolver must yield unresolved transitions, got %+v", transitions)
	}

	if got := PlanTransitions(&Config{}, nil, nil); len(got) != 0 {
		t.Fatalf("expected no transitions for an empty config, got %d", len(got))
	}
}
