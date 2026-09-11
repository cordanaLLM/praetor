package needs

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestEmitDemandRequests(t *testing.T) {
	tempDir := t.TempDir()

	requests := []FrameworkDemandRequest{
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

	if err := EmitDemandRequests(requests, tempDir); err != nil {
		t.Fatalf("failed to emit demand requests: %v", err)
	}

	manifestFile := filepath.Join(tempDir, "FRAMEWORK_DEMAND.yaml")
	data, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatalf("failed to read generated manifest: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty FRAMEWORK_DEMAND.yaml")
	}

	specFile := filepath.Join(tempDir, "REQ-CAP-STORAGE-S3.md")
	specData, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("failed to read generated spec file: %v", err)
	}
	if string(specData) != requests[0].SpecificationMarkdown {
		t.Errorf("spec content mismatch: got %s", string(specData))
	}
}
