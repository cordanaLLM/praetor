package clientsetup

import (
	"context"
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
		if item.Client == Codex || item.Client == Claude || item.Client == Gemini {
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
