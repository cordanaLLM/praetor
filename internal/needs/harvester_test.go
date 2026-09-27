package needs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupTestHarvest(t *testing.T) string {
	t.Helper()
	tempHarvest := t.TempDir()
	invDir := filepath.Join(tempHarvest, "dev-inventory")
	patchesDir := filepath.Join(tempHarvest, "dev-patches")
	if err := os.MkdirAll(invDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(patchesDir, 0755); err != nil {
		t.Fatal(err)
	}

	invJSON := `[
		{"Name": "jellyfin-utilities", "Type": "Git", "Path": "C:\\dev\\jellyfin-utilities", "DirtyCount": 1, "HasPatches": true},
		{"Name": "acme-svelte-app", "Type": "Git", "Path": "C:\\dev\\acme-svelte-app", "DirtyCount": 0, "HasPatches": false}
	]`
	if err := os.WriteFile(filepath.Join(invDir, "dev-inventory.json"), []byte(invJSON), 0644); err != nil {
		t.Fatal(err)
	}

	patchDiff := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,2 @@\n+import \"github.com/jackc/pgx/v5\"\n"
	if err := os.WriteFile(filepath.Join(patchesDir, "jellyfin-utilities.patch"), []byte(patchDiff), 0644); err != nil {
		t.Fatal(err)
	}
	return tempHarvest
}

func TestCodifyHarvestedInventory_Positive(t *testing.T) {
	ctx := context.Background()
	tempHarvest := setupTestHarvest(t)

	results, err := CodifyHarvestedInventory(ctx, tempHarvest, nil)
	if err != nil {
		t.Fatalf("harvest codification failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 codified repos, got %d", len(results))
	}

	if results[0].Language != "go" || len(results[0].Dependencies) < 1 {
		t.Errorf("expected jellyfin go deps, got %+v", results[0])
	}
	if results[1].Language != "typescript" {
		t.Errorf("expected acme-svelte-app typescript, got %s", results[1].Language)
	}
}

func TestCodifyHarvestedInventory_Negative(t *testing.T) {
	ctx := context.Background()
	if _, err := CodifyHarvestedInventory(ctx, "/nonexistent/path", nil); err == nil {
		t.Fatal("expected error for nonexistent harvest path")
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CodifyHarvestedInventory(cancelCtx, t.TempDir(), nil); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestCodifyHarvestedInventory_Empty(t *testing.T) {
	ctx := context.Background()
	tempHarvest := t.TempDir()
	invDir := filepath.Join(tempHarvest, "dev-inventory")
	if err := os.MkdirAll(invDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invDir, "dev-inventory.json"), []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := CodifyHarvestedInventory(ctx, tempHarvest, nil)
	if err != nil {
		t.Fatalf("empty inventory error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 repos, got %d", len(results))
	}
}

// Harvested repositories record the target of their inferred language: the configured one,
// or none for a language without a target; an unconfigured host records none at all and
// scores the rows not-configured.
func TestCodifyHarvestedInventoryUsesTargets_3D(t *testing.T) {
	harvest := setupTestHarvest(t)
	// Positive: a configured go target names the framework and routes the harvested demand.
	configured, err := CodifyHarvestedInventory(t.Context(), harvest, acmeTargets())
	if err != nil {
		t.Fatal(err)
	}
	goRepo := configured[0]
	if goRepo.Framework != "example.com/acme/kit" || goRepo.BuilderKits[0] != "acme/kit" || goRepo.Dependencies[0].TargetBuilderKit != "acme/kit" ||
		goRepo.Dependencies[0].FrameworkReplacement != "" {
		t.Fatalf("configured go target = %+v", goRepo)
	}
	// Negative: a typescript repository on a host configuring only go has no framework.
	goOnly, err := CodifyHarvestedInventory(t.Context(), harvest, Targets{"go": acmeTargets()["go"]})
	if err != nil {
		t.Fatal(err)
	}
	if ts := goOnly[1]; ts.Framework != "" || len(ts.BuilderKits) != 0 {
		t.Fatalf("unconfigured typescript target = %+v", ts)
	}
	// Boundary: no configured target names no framework and routes nothing.
	unconfigured, err := CodifyHarvestedInventory(t.Context(), harvest, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range unconfigured {
		if row.Framework != "" || len(row.BuilderKits) != 0 || row.Readiness.Basis != FrameworkNotConfigured {
			t.Fatalf("an unconfigured harvest row named a framework: %+v", row)
		}
	}
	if unconfigured[0].Dependencies[0].TargetBuilderKit != "" || unconfigured[0].Dependencies[0].Status != StatusGap {
		t.Fatalf("an unconfigured harvested demand was routed or scored: %+v", unconfigured[0].Dependencies[0])
	}
}
