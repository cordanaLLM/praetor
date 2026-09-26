package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateCycloneDX_Positive_And_Negative(t *testing.T) {
	ctx := context.Background()
	repoRoot := filepath.Join("..", "..")

	bom, err := GenerateCycloneDX(ctx, repoRoot, SBOMOptions{})
	if err != nil {
		t.Fatalf("expected CycloneDX generation to succeed on repo root: %v", err)
	}

	if bom.BOMFormat != "CycloneDX" || bom.SpecVersion != "1.5" {
		t.Errorf("unexpected BOM metadata: %+v", bom)
	}

	// A generator that silently parsed zero components would still pass every check
	// above; assert a component was actually parsed, and that it is a real dependency
	// of this repository rather than a placeholder.
	if len(bom.Components) == 0 {
		t.Fatal("expected at least one component parsed from this repository's go.mod, got 0")
	}
	found := false
	for _, c := range bom.Components {
		if c.Name == "gopkg.in/yaml.v3" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gopkg.in/yaml.v3 among parsed components, got %+v", bom.Components)
	}

	// The metadata component must describe the repository actually scanned, never a
	// value fixed at compile time.
	if bom.Metadata.Component.Name != "github.com/cordanaLLM/praetor" {
		t.Errorf("expected metadata component name to be the module path, got %q", bom.Metadata.Component.Name)
	}
	// The version belongs to the scanned checkout, never to the test binary's build info.
	if bom.Metadata.Component.Version == "(devel)" {
		t.Error("metadata component version was taken from the generating binary's build info")
	}

	// Negative: non-existent repo directory
	_, negErr := GenerateCycloneDX(ctx, filepath.Join(repoRoot, "nonexistent-dir"), SBOMOptions{})
	if negErr == nil {
		t.Error("expected error for non-existent directory, got nil")
	}

	// Boundary: nil context
	var absentContext context.Context
	_, nilErr := GenerateCycloneDX(absentContext, repoRoot, SBOMOptions{})
	if nilErr == nil {
		t.Error("expected error for nil context, got nil")
	}
}

func TestGenerateCycloneDX_Positive_ReplaceDirectiveAppliesToComponent(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := filepath.Join(tmpDir, "go.mod")
	content := "module example.com/test\n\ngo 1.24\n\nrequire old.example.com/dep v1.0.0\n\nreplace old.example.com/dep => new.example.com/dep v2.3.4\n"
	if err := os.WriteFile(goMod, []byte(content), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	bom, err := GenerateCycloneDX(context.Background(), tmpDir, SBOMOptions{})
	if err != nil {
		t.Fatalf("expected generation to succeed: %v", err)
	}
	if len(bom.Components) == 0 {
		t.Fatal("expected a non-zero component count")
	}
	comp := bom.Components[0]
	if comp.Name != "new.example.com/dep" {
		t.Errorf("expected replaced path, got %q", comp.Name)
	}
	if comp.Version != "v2.3.4" {
		t.Errorf("expected replacement version, got %q", comp.Version)
	}
	if comp.PURL != "pkg:golang/new.example.com/dep@v2.3.4" {
		t.Errorf("expected PURL to reflect the replacement, got %q", comp.PURL)
	}
}

func TestParseGoModComponents_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	emptyGoMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(emptyGoMod, []byte("module example.com/test\n\ngo 1.24\n"), 0644); err != nil {
		t.Fatalf("write empty go.mod: %v", err)
	}

	bom, err := GenerateCycloneDX(context.Background(), tmpDir, SBOMOptions{})
	if err != nil {
		t.Fatalf("expected generation on empty go.mod to succeed: %v", err)
	}
	if len(bom.Components) != 0 {
		t.Errorf("expected 0 components, got %d", len(bom.Components))
	}
}

func TestSBOMRejectsLinkedOrOversizedManifest(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	source := filepath.Join(outside, "go.mod")
	if err := os.WriteFile(source, []byte("module example.com/outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "go.mod")
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateCycloneDX(t.Context(), root, SBOMOptions{}); err == nil {
		t.Fatal("linked manifest accepted")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateCycloneDX(t.Context(), root, SBOMOptions{}); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
