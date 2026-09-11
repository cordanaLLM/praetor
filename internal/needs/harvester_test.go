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
		{"Name": "sveltesentio", "Type": "Git", "Path": "C:\\dev\\sveltesentio", "DirtyCount": 0, "HasPatches": false}
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

	results, err := CodifyHarvestedInventory(ctx, tempHarvest)
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
		t.Errorf("expected sveltesentio typescript, got %s", results[1].Language)
	}
}

func TestCodifyHarvestedInventory_Negative(t *testing.T) {
	ctx := context.Background()
	if _, err := CodifyHarvestedInventory(ctx, "/nonexistent/path"); err == nil {
		t.Fatal("expected error for nonexistent harvest path")
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CodifyHarvestedInventory(cancelCtx, t.TempDir()); err == nil {
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

	results, err := CodifyHarvestedInventory(ctx, tempHarvest)
	if err != nil {
		t.Fatalf("empty inventory error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 repos, got %d", len(results))
	}
}
