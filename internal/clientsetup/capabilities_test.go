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
		if item.Lifecycle.Activation != "unverified" || item.BriefCapture.Activation != "unverified" ||
			item.ReturnCapture.Activation != "unverified" || item.RegisterGate.Activation != "unverified" {
			t.Fatalf("activation claimed: %+v", item)
		}
		if item.Client == AGY {
			if item.Mode != "merge" || item.RelativePath != ".agents/mcp_config.json" || item.Lifecycle.State != "adapter-defined" || !slices.Equal(item.Lifecycle.DefinitionPaths, []string{".agents/plugins/praetor/hooks.json"}) {
				t.Fatalf("agy merge adapter not declared: %+v", item)
			}
			if item.BriefCapture.State != "adapter-defined" || item.ReturnCapture.State != "unenforceable" || item.RegisterGate.State != "unenforceable" {
				t.Fatalf("agy text boundary overstated: %+v", item)
			}
		} else if item.Client == Codex {
			if item.Lifecycle.State != "adapter-defined" || len(item.Lifecycle.DefinitionPaths) != 1 {
				t.Fatalf("codex native lifecycle missing: %+v", item)
			}
			if item.BriefCapture.State != "adapter-defined" || item.ReturnCapture.State != "adapter-defined" || item.RegisterGate.State != "unenforceable" {
				t.Fatalf("codex correlation gap hidden: %+v", item)
			}
		} else if item.Client == Claude {
			if item.Lifecycle.State != "adapter-defined" || len(item.Lifecycle.DefinitionPaths) != 1 {
				t.Fatalf("native lifecycle missing: %+v", item)
			}
			if item.BriefCapture.State != "adapter-defined" || item.ReturnCapture.State != "adapter-defined" || item.RegisterGate.State != "adapter-defined" {
				t.Fatalf("native text adapters missing: %+v", item)
			}
		} else if item.Client == Gemini {
			if item.Lifecycle.State != "adapter-defined" || len(item.Lifecycle.DefinitionPaths) != 1 {
				t.Fatalf("gemini native lifecycle missing: %+v", item)
			}
			if item.BriefCapture.State != "adapter-defined" || item.ReturnCapture.State != "unenforceable" || item.RegisterGate.State != "unenforceable" {
				t.Fatalf("gemini unproved return shape overstated: %+v", item)
			}
		} else if item.Lifecycle.State != "unsupported" || len(item.Lifecycle.DefinitionPaths) != 0 {
			t.Fatalf("unsupported lifecycle claimed: %+v", item)
		} else if item.BriefCapture.State != "unenforceable" || item.ReturnCapture.State != "unenforceable" || item.RegisterGate.State != "unenforceable" {
			t.Fatalf("unsupported text boundary claimed: %+v", item)
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
		capabilities := []*[]string{&first.Clients[i].Lifecycle.DefinitionPaths, &first.Clients[i].BriefCapture.DefinitionPaths,
			&first.Clients[i].ReturnCapture.DefinitionPaths, &first.Clients[i].RegisterGate.DefinitionPaths}
		for _, paths := range capabilities {
			for j := range *paths {
				(*paths)[j] = "mutated"
			}
		}
	}
	second, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range second.Clients {
		if slices.Contains(item.Lifecycle.DefinitionPaths, "mutated") || slices.Contains(item.BriefCapture.DefinitionPaths, "mutated") ||
			slices.Contains(item.ReturnCapture.DefinitionPaths, "mutated") || slices.Contains(item.RegisterGate.DefinitionPaths, "mutated") {
			t.Fatalf("caller mutation reached the inventory: %+v", item)
		}
	}
}
