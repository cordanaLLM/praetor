package flavor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

func TestDetectFlavor_Positive(t *testing.T) {
	tmp := t.TempDir()

	// 1. Go service detection
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test/svc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "cmd", "svc"), 0755); err != nil {
		t.Fatal(err)
	}
	detected := flavor.DetectFlavor(tmp)
	if detected != "go-service" {
		t.Fatalf("expected go-service, got %s", detected)
	}

	// 2. Svelte detection
	tmpSvelte := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpSvelte, "package.json"), []byte("{\"name\": \"test\"}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpSvelte, "svelte.config.js"), []byte("// config"), 0644); err != nil {
		t.Fatal(err)
	}
	detectedSvelte := flavor.DetectFlavor(tmpSvelte)
	if detectedSvelte != "frontend-svelte" {
		t.Fatalf("expected frontend-svelte, got %s", detectedSvelte)
	}
}

func TestAuditFlavor_PositiveAndScoring(t *testing.T) {
	tmp := t.TempDir()
	// Scaffold minimal files for go-library
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test/lib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "internal"), 0755); err != nil {
		t.Fatal(err)
	}

	report, err := flavor.AuditFlavor(tmp, "go-library")
	if err != nil {
		t.Fatalf("audit flavor failed: %v", err)
	}
	if report.Flavor != "go-library" {
		t.Fatalf("expected flavor go-library, got %s", report.Flavor)
	}
	if report.TemplatesTotal == 0 {
		t.Fatal("expected non-zero required templates")
	}
	if report.Score < 0.0 || report.Score > 100.0 {
		t.Fatalf("invalid score: %f", report.Score)
	}
}

func TestAuditFlavor_Negative_UnknownFlavor(t *testing.T) {
	tmp := t.TempDir()
	_, err := flavor.AuditFlavor(tmp, "non-existent-flavor-xyz")
	if err == nil {
		t.Fatal("expected error for unknown flavor, got nil")
	}
}

func TestApplyFlavor_PositiveAndBoundary(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()

	// 1. Initial apply
	report, err := flavor.ApplyFlavor(ctx, tmp, "go-service", false)
	if err != nil {
		t.Fatalf("apply flavor failed: %v", err)
	}
	if len(report.CreatedTemplates) == 0 {
		t.Fatal("expected created templates")
	}
	if !report.WorkingDirCreated {
		t.Fatal("expected workingdir to be created")
	}

	// Verify .workingdir/STATE.md exists
	if _, err := os.Stat(filepath.Join(tmp, ".workingdir", "STATE.md")); err != nil {
		t.Fatalf("STATE.md not created: %v", err)
	}

	// 2. Second apply with force=false (boundary: should skip existing)
	report2, err := flavor.ApplyFlavor(ctx, tmp, "go-service", false)
	if err != nil {
		t.Fatalf("apply flavor second run failed: %v", err)
	}
	if len(report2.SkippedTemplates) == 0 {
		t.Fatal("expected skipped templates on non-force re-apply")
	}

	// 3. Third apply with force=true (boundary: should overwrite/re-create)
	report3, err := flavor.ApplyFlavor(ctx, tmp, "go-service", true)
	if err != nil {
		t.Fatalf("apply flavor force run failed: %v", err)
	}
	if len(report3.CreatedTemplates) == 0 {
		t.Fatal("expected recreated templates on force re-apply")
	}
}

func TestApplyFlavor_Negative_UnknownFlavor(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()
	_, err := flavor.ApplyFlavor(ctx, tmp, "non-existent-flavor-xyz", false)
	if err == nil {
		t.Fatal("expected error for unknown flavor, got nil")
	}
}

func TestAuditFlavor_Boundary_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	report, err := flavor.AuditFlavor(tmp, "auto")
	if err != nil {
		t.Fatalf("audit flavor on empty dir failed: %v", err)
	}
	if report.Score < 0 || report.Score > 100 {
		t.Fatalf("unexpected score on empty dir: %f", report.Score)
	}
}
