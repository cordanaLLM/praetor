package clientsetup

import (
	"context"
	"slices"
	"testing"
)

func TestCapabilitiesInventory(t *testing.T) {
	report, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.RuntimeVerified || len(report.Clients) != len(adapters) {
		t.Fatalf("unexpected report: %+v", report)
	}
	for i, item := range report.Clients {
		if i > 0 && report.Clients[i-1].Client >= item.Client {
			t.Fatalf("clients not sorted: %+v", report.Clients)
		}
		if item.Lifecycle.Activation != "unverified" {
			t.Fatalf("activation claimed: %+v", item)
		}
		if item.Client == AGY {
			if item.Mode != "merge" || item.RelativePath != ".agents/mcp_config.json" || item.Lifecycle.State != "adapter-defined" || !slices.Equal(item.Lifecycle.DefinitionPaths, []string{".agents/mcp_config.json"}) {
				t.Fatalf("agy merge adapter not declared: %+v", item)
			}
		} else if item.Client == Codex || item.Client == Claude || item.Client == Gemini {
			if item.Lifecycle.State != "adapter-defined" || len(item.Lifecycle.DefinitionPaths) != 1 {
				t.Fatalf("native lifecycle missing: %+v", item)
			}
		} else if item.Lifecycle.State != "unsupported" || len(item.Lifecycle.DefinitionPaths) != 0 {
			t.Fatalf("unsupported lifecycle claimed: %+v", item)
		}
	}
}

func TestCapabilitiesContext(t *testing.T) {
	var nilContext context.Context
	if _, err := Capabilities(nilContext); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Capabilities(ctx); err == nil {
		t.Fatal("canceled context accepted")
	}
}

func TestCapabilitiesDefinitionPathsAreCopies(t *testing.T) {
	first, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Clients {
		for j := range first.Clients[i].Lifecycle.DefinitionPaths {
			first.Clients[i].Lifecycle.DefinitionPaths[j] = "mutated"
		}
	}
	second, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range second.Clients {
		if slices.Contains(item.Lifecycle.DefinitionPaths, "mutated") {
			t.Fatalf("caller mutation reached the inventory: %+v", item)
		}
	}
}
