package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCycloneDX_Positive_And_Negative(t *testing.T) {
	ctx := context.Background()
	repoRoot := filepath.Join("..", "..")

	bom, err := GenerateCycloneDX(ctx, repoRoot)
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
	if bom.Metadata.Component.Version == "" {
		t.Error("expected metadata component version to be populated from build info")
	}

	// Negative: non-existent repo directory
	_, negErr := GenerateCycloneDX(ctx, filepath.Join(repoRoot, "nonexistent-dir"))
	if negErr == nil {
		t.Error("expected error for non-existent directory, got nil")
	}

	// Boundary: nil context
	var absentContext context.Context
	_, nilErr := GenerateCycloneDX(absentContext, repoRoot)
	if nilErr == nil {
		t.Error("expected error for nil context, got nil")
	}
}

func TestGenerateSLSAProvenance_Positive_And_Boundary(t *testing.T) {
	ctx := context.Background()
	digest := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	stmt, err := GenerateSLSAProvenance(ctx, "praetorctl", "ghcr.io/cordanallm/builder", digest)
	if err != nil {
		t.Fatalf("expected SLSA statement generation to succeed: %v", err)
	}

	if len(stmt.Subject) == 0 || stmt.Subject[0].Digest["sha256"] != digest {
		t.Errorf("mismatched digest in subject: %+v", stmt.Subject)
	}

	// Negative: empty digest
	_, emptyErr := GenerateSLSAProvenance(ctx, "praetorctl", "builder", "")
	if emptyErr == nil {
		t.Error("expected error on empty digest, got nil")
	}

	// Boundary: cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, cancErr := GenerateSLSAProvenance(cancCtx, "praetorctl", "builder", digest)
	if cancErr == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}

func TestGenerateSLSAProvenance_Negative_MalformedDigests(t *testing.T) {
	ctx := context.Background()
	valid := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	cases := map[string]string{
		"63 characters (one short)": valid[:len(valid)-1],
		"uppercase hex":             strings.ToUpper(valid),
		"non-hex characters":        strings.Repeat("g", 64),
	}
	for name, digest := range cases {
		if _, err := GenerateSLSAProvenance(ctx, "praetorctl", "builder", digest); err == nil {
			t.Errorf("%s: expected digest %q to be rejected", name, digest)
		}
	}
}

func TestGenerateCycloneDX_Positive_ReplaceDirectiveAppliesToComponent(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := filepath.Join(tmpDir, "go.mod")
	content := "module example.com/test\n\ngo 1.24\n\nrequire old.example.com/dep v1.0.0\n\nreplace old.example.com/dep => new.example.com/dep v2.3.4\n"
	if err := os.WriteFile(goMod, []byte(content), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	bom, err := GenerateCycloneDX(context.Background(), tmpDir)
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

	bom, err := GenerateCycloneDX(context.Background(), tmpDir)
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
	if _, err := GenerateCycloneDX(t.Context(), root); err == nil {
		t.Fatal("linked manifest accepted")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateCycloneDX(t.Context(), root); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
