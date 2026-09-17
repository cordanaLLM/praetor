package router

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestDeclaredTaskLabelsPositive(t *testing.T) {
	cfg, err := LoadRoutingConfigContext(context.Background(), filepath.Join("..", "..", ".config", "models", "routing.yaml"))
	if err != nil {
		t.Fatalf("load shipped routing.yaml: %v", err)
	}
	labels := DeclaredTaskLabels(cfg)
	if len(labels) != 16 {
		t.Fatalf("shipped routing.yaml declares %d labels, want 16: %v", len(labels), labels)
	}
	if !sort.StringsAreSorted(labels) {
		t.Fatalf("labels are not sorted: %v", labels)
	}
	if defaults := DefaultTaskLabels(); !reflect.DeepEqual(labels, defaults) {
		t.Fatalf("shipped labels %v differ from the built-in defaults %v", labels, defaults)
	}
}

func TestDeclaredTaskLabelsNegative(t *testing.T) {
	if labels := DeclaredTaskLabels(nil); labels == nil || len(labels) != 0 {
		t.Fatalf("nil configuration = %v, want an empty non-nil slice", labels)
	}
	if labels := DeclaredTaskLabels(&RoutingConfig{}); len(labels) != 0 {
		t.Fatalf("configuration without tiers = %v, want none", labels)
	}
}

func TestDeclaredTaskLabelsBoundary(t *testing.T) {
	tiers := make(map[string]Tier, MaxRoutingTiers)
	for i := 0; i < MaxRoutingTiers; i++ {
		tasks := make([]string, 0, MaxRoutingTags)
		for j := 0; j < MaxRoutingTags-1; j++ {
			tasks = append(tasks, fmt.Sprintf("task_%02d_%02d", i, j))
		}
		tiers[fmt.Sprintf("tier-%02d", i)] = Tier{TargetTasks: append(tasks, "shared_label")}
	}
	labels := DeclaredTaskLabels(&RoutingConfig{Tiers: tiers})
	if want := MaxRoutingTiers*(MaxRoutingTags-1) + 1; len(labels) != want {
		t.Fatalf("label union = %d, want %d (duplicates across tiers collapse)", len(labels), want)
	}
	if len(labels) > MaxTaskLabels {
		t.Fatalf("label union %d exceeds MaxTaskLabels %d", len(labels), MaxTaskLabels)
	}
}

func TestDefaultTaskLabels(t *testing.T) {
	labels := DefaultTaskLabels()
	if len(labels) != 16 || !sort.StringsAreSorted(labels) {
		t.Fatalf("default labels = %v, want 16 sorted labels", labels)
	}
	labels[0] = "mutated"
	if DefaultTaskLabels()[0] == "mutated" {
		t.Fatal("DefaultTaskLabels must return a fresh slice")
	}
}

func TestValidTaskLabel(t *testing.T) {
	if !ValidTaskLabel("ci_debugging") {
		t.Fatal("a declared label shape must be valid")
	}
	for _, bad := range []string{"", " padded", "tab\tinside", "line\nbreak", strings.Repeat("x", maxRoutingNameBytes+1)} {
		if ValidTaskLabel(bad) {
			t.Fatalf("label %q must be rejected", bad)
		}
	}
	if !ValidTaskLabel(strings.Repeat("x", maxRoutingNameBytes)) {
		t.Fatalf("a label of exactly %d bytes must be valid", maxRoutingNameBytes)
	}
}
