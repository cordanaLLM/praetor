package needs

import (
	"path/filepath"
	"testing"
)

func TestInferLanguageFromItemBranches(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		// Names that merely contain the letters "arr" are not part of the *arr stack, and
		// with no other signal their language is unsupported, not a Go default (BUG-864).
		{"go-arrow", LanguageUnsupported},
		{"carrot-service", LanguageUnsupported},
		{"barrier", LanguageUnsupported},
		{"narrative-api", LanguageUnsupported},
		// Real *arr projects still resolve to python.
		{"sonarr", "python"},
		{"my-radarr-helper", "python"},
		{"python-tooling", "python"},
		// The remaining branches.
		{"sveltesentio", "typescript"},
		{"pelorus", "typescript"},
		{"ffmpeg-tools", "native"},
		{"vmafx", "native"},
		{"gpu-bench", "native"},
		{"rust-compute", "rust"},
		{"plain-service", LanguageUnsupported},
		{"", LanguageUnsupported},
	}
	for _, tc := range cases {
		got := inferLanguageFromItem(HarvestRepoItem{Name: tc.name}, nil)
		if got != tc.want {
			t.Errorf("inferLanguageFromItem(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInferLanguageFromItemPrefersPatchEvidence(t *testing.T) {
	// A Go module path harvested from the patch outranks the name heuristic.
	got := inferLanguageFromItem(HarvestRepoItem{Name: "sonarr"}, []string{"github.com/gin-gonic/gin"})
	if got != "go" {
		t.Errorf("expected patch evidence to win, got %q", got)
	}
}

// TestCodifyHarvestedInventoryMarksUnknownLanguageUnsupported: no patch evidence and no
// name signal yields an explicit unsupported entry without Go framework, kits or score.
func TestCodifyHarvestedInventoryMarksUnknownLanguageUnsupported(t *testing.T) {
	harvest := t.TempDir()
	writeFixture(t, harvest, filepath.Join("dev-inventory", "dev-inventory.json"),
		`[{"Name": "plain-service", "Type": "Git", "HasPatches": true}]`)
	writeFixture(t, harvest, filepath.Join("dev-patches", "plain-service.patch"),
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,1 +1,2 @@\n+todo\n")

	results, err := CodifyHarvestedInventory(t.Context(), harvest)
	if err != nil {
		t.Fatalf("CodifyHarvestedInventory() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 codified repo, got %d", len(results))
	}
	got := results[0]
	if got.Language != LanguageUnsupported || got.Framework != "" || len(got.BuilderKits) != 0 ||
		len(got.Dependencies) != 0 || got.Readiness.Score != 0 {
		t.Fatalf("unknown-language repo = %+v, want unsupported with no framework, kits, deps or score", got)
	}
}

func TestExtractPatchDependenciesFormBranches(t *testing.T) {
	patchesDir := t.TempDir()
	patch := "--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,20 @@\n" +
		"+import (\n" +
		"+\t\"github.com/gin-gonic/gin\"\n" +
		"+\t\"github.com/jackc/pgx/v5\"\n" +
		"+)\n" +
		"+require (\n" +
		"+\tgithub.com/redis/rueidis v1.0.51\n" +
		"+)\n" +
		"+const svelte = require(\"svelte\")\n" +
		"+import numpy\n" +
		"+from pydantic import BaseModel\n" +
		"+dep_cuda = dependency('cuda')\n" +
		"-import \"github.com/removed/pkg\"\n"
	writeFixture(t, patchesDir, "svc.patch", patch)

	deps, err := extractPatchDependencies("svc", patchesDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	index := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		index[d] = struct{}{}
	}
	for _, want := range []string{
		"github.com/gin-gonic/gin", "github.com/jackc/pgx/v5", "github.com/redis/rueidis",
		"svelte", "numpy", "pydantic", "cuda",
	} {
		if _, ok := index[want]; !ok {
			t.Errorf("expected %q to be harvested, got %v", want, deps)
		}
	}
	if _, ok := index["github.com/removed/pkg"]; ok {
		t.Errorf("removed lines must not be harvested, got %v", deps)
	}
}

func TestExtractPatchDependenciesBoundaryNoPatch(t *testing.T) {
	deps, err := extractPatchDependencies("absent", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected no dependencies without a patch file, got %v", deps)
	}
}

func TestExtractPatchDependenciesNegativeEscapingName(t *testing.T) {
	patchesDir := t.TempDir()
	outside := filepath.Dir(patchesDir)
	writeFixture(t, outside, "escape.patch", "+import \"github.com/gin-gonic/gin\"\n")

	deps, err := extractPatchDependencies(filepath.Join("..", "escape"), patchesDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("a patch name escaping the bundle must not be read, got %v", deps)
	}
}

func TestCodifyHarvestedInventoryHarvestsGroupedImports(t *testing.T) {
	harvest := t.TempDir()
	writeFixture(t, harvest, filepath.Join("dev-inventory", "dev-inventory.json"),
		`[{"Name": "svc", "Type": "Git", "HasPatches": true}]`)
	writeFixture(t, harvest, filepath.Join("dev-patches", "svc.patch"),
		"--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,4 @@\n+import (\n+\t\"github.com/gin-gonic/gin\"\n+)\n")

	results, err := CodifyHarvestedInventory(t.Context(), harvest)
	if err != nil {
		t.Fatalf("harvest codification failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 codified repo, got %d", len(results))
	}
	if len(results[0].Dependencies) != 1 {
		t.Fatalf("expected the grouped import to be harvested, got %+v", results[0].Dependencies)
	}
	if results[0].Readiness.Score != 100.0 {
		t.Errorf("gin is covered, expected 100%% readiness, got %.1f", results[0].Readiness.Score)
	}
}
