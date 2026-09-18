package needs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func demandFixture() []FrameworkDemandRequest {
	return []FrameworkDemandRequest{
		{
			RequestID:             "REQ-CAP-STORAGE-S3",
			Title:                 "[FRAMEWORK-DEMAND] Universal S3 Storage Adapter",
			Capability:            "storage.s3",
			TargetOrg:             "golusoris",
			TargetBuilderKit:      "golusoris/storage",
			ConsumingRepos:        []string{"repo-a", "repo-b"},
			ConsumerCount:         2,
			ReplacedPackages:      []string{"boto3"},
			DeduplicationRatio:    2.0,
			MaintenanceROI:        "Medium (2:1 deduplication)",
			SpecificationMarkdown: "# S3 Storage Spec\nDetails...",
		},
	}
}

func TestSynthesizeDemands_Positive(t *testing.T) {
	report := &FleetDemandReport{
		TotalRepositories:   5,
		ScannedRepositories: 5,
		Gaps: []GapDetail{
			{
				Capability:    "storage.s3",
				ConsumerCount: 3,
				Consumers:     []string{"repo-a", "repo-b", "repo-c"},
				PackagesUsed:  []string{"github.com/aws/aws-sdk-go-v2", "boto3"},
			},
			{
				Capability:    "ui.components",
				ConsumerCount: 2,
				Consumers:     []string{"repo-frontend", "repo-admin"},
				PackagesUsed:  []string{"bits-ui", "shadcn-svelte"},
			},
		},
	}

	requests := SynthesizeDemands(report)
	if len(requests) != 2 {
		t.Fatalf("expected 2 demand requests, got %d", len(requests))
	}

	// First should be highest consumer count (storage.s3 with 3 consumers)
	first := requests[0]
	if first.Capability != "storage.s3" {
		t.Errorf("expected highest priority request to be storage.s3, got %s", first.Capability)
	}
	if first.DeduplicationRatio != 3.0 {
		t.Errorf("expected deduplication ratio 3.0, got %.1f", first.DeduplicationRatio)
	}
	if first.TargetOrg != "golusoris" {
		t.Errorf("expected target org golusoris, got %s", first.TargetOrg)
	}
	if first.TargetBuilderKit != "golusoris/golusoris" {
		t.Errorf("expected default kit golusoris/golusoris, got %s", first.TargetBuilderKit)
	}

	second := requests[1]
	if second.TargetBuilderKit != "golusoris/sveltesentio" {
		t.Errorf("expected UI kit golusoris/sveltesentio, got %s", second.TargetBuilderKit)
	}
}

func TestSynthesizeDemands_Empty(t *testing.T) {
	requests := SynthesizeDemands(nil)
	if len(requests) != 0 {
		t.Errorf("expected 0 requests for nil report, got %d", len(requests))
	}

	emptyReport := &FleetDemandReport{Gaps: []GapDetail{}}
	requests = SynthesizeDemands(emptyReport)
	if len(requests) != 0 {
		t.Errorf("expected 0 requests for empty gaps, got %d", len(requests))
	}
}

func TestEmitDemandRequests_Positive(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "demands")
	requests := demandFixture()

	if err := EmitDemandRequests(context.Background(), requests, tempDir); err != nil {
		t.Fatalf("failed to emit demand requests: %v", err)
	}

	manifestFile := filepath.Join(tempDir, "FRAMEWORK_DEMAND.yaml")
	data, err := os.ReadFile(manifestFile) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("failed to read generated manifest: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty FRAMEWORK_DEMAND.yaml")
	}

	specFile := filepath.Join(tempDir, "REQ-CAP-STORAGE-S3.md")
	specData, err := os.ReadFile(specFile) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("failed to read generated spec file: %v", err)
	}
	if string(specData) != requests[0].SpecificationMarkdown {
		t.Errorf("spec content mismatch: got %s", string(specData))
	}

	// The artifacts enumerate the whole fleet: they must not be world-readable.
	dirInfo, err := os.Stat(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); util.ModeIsProtection() && perm != 0o700 {
		t.Errorf("expected the output directory to be owner-only, got %#o", perm)
	}
	for _, f := range []string{manifestFile, specFile} {
		info, statErr := os.Stat(f)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if perm := info.Mode().Perm(); util.ModeIsProtection() && perm != 0o600 {
			t.Errorf("expected %s to be owner-only, got %#o", f, perm)
		}
	}
}

func TestEmitDemandRequests_Negative(t *testing.T) {
	ctx := context.Background()
	requests := demandFixture()

	if err := EmitDemandRequests(ctx, requests, "   "); err == nil {
		t.Fatal("expected an error for an empty output directory")
	}

	// A regular file where the output directory belongs makes every write fail.
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EmitDemandRequests(ctx, requests, filepath.Join(blocker, "out")); err == nil {
		t.Fatal("expected an error when the output directory cannot be created")
	}

	// A request id that is not a plain file name must not escape the output directory.
	escaping := demandFixture()
	escaping[0].RequestID = filepath.Join("..", "escaped")
	outDir := filepath.Join(t.TempDir(), "demands")
	if err := EmitDemandRequests(ctx, escaping, outDir); err == nil {
		t.Fatal("expected an error for a request id escaping the output directory")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := EmitDemandRequests(cancelled, requests, filepath.Join(t.TempDir(), "cancelled")); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestEmitDemandRequests_Boundary(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "empty-demands")

	// An empty request set still produces the manifest and nothing else.
	if err := EmitDemandRequests(context.Background(), nil, tempDir); err != nil {
		t.Fatalf("expected an empty request set to succeed: %v", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "FRAMEWORK_DEMAND.yaml" {
		t.Fatalf("expected only the manifest, got %d entries", len(entries))
	}
}
