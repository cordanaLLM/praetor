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

	bom, err := GenerateCycloneDX(ctx, repoRoot)
	if err != nil {
		t.Fatalf("expected CycloneDX generation to succeed on repo root: %v", err)
	}

	if bom.BOMFormat != "CycloneDX" || bom.SpecVersion != "1.5" {
		t.Errorf("unexpected BOM metadata: %+v", bom)
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
