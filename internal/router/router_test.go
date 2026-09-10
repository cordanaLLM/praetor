package router

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSyncCatalog_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "routing.yaml")

	opts := SyncOptions{
		IncludeOpenWeights: true,
		DiscoverLocal:      false,
	}

	res, err := SyncCatalog(ctx, targetPath, opts)
	if err != nil {
		t.Fatalf("unexpected sync error: %v", err)
	}

	if res.TotalModels < 15 {
		t.Fatalf("expected at least 15 verified models, got %d", res.TotalModels)
	}
	if res.FrontierTier == 0 {
		t.Fatalf("expected frontier models to be classified")
	}
	if res.Workhorse == 0 {
		t.Fatalf("expected workhorse models to be classified")
	}
	if res.OSSFast == 0 {
		t.Fatalf("expected oss-fast models to be classified")
	}

	// Verify file loads cleanly
	cfg, err := LoadRoutingConfig(targetPath)
	if err != nil {
		t.Fatalf("failed to load generated config: %v", err)
	}
	if len(cfg.Tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(cfg.Tiers))
	}
}

func TestClassifyTier_Thresholds(t *testing.T) {
	// Frontier cases
	if tier := ClassifyTier("claude-3-opus-20240229", FamilyAnthropic, 1305); tier != "frontier" {
		t.Fatalf("expected frontier for opus, got %s", tier)
	}
	if tier := ClassifyTier("gemini-2.5-pro", FamilyGoogle, 1360); tier != "frontier" {
		t.Fatalf("expected frontier for gemini pro, got %s", tier)
	}
	if tier := ClassifyTier("deepseek-reasoner", "deepseek", 1340); tier != "frontier" {
		t.Fatalf("expected frontier for deepseek reasoner, got %s", tier)
	}

	// Workhorse cases
	if tier := ClassifyTier("claude-3-7-sonnet", FamilyAnthropic, 1280); tier != "workhorse" {
		t.Fatalf("expected workhorse for sonnet, got %s", tier)
	}
	if tier := ClassifyTier("gpt-4o", FamilyOpenAI, 1280); tier != "workhorse" {
		t.Fatalf("expected workhorse for gpt-4o, got %s", tier)
	}

	// Fast OSS cases
	if tier := ClassifyTier("qwen-2.5-coder-32b", FamilyOpenWeights, 1150); tier != "oss-fast" {
		t.Fatalf("expected oss-fast for qwen coder, got %s", tier)
	}
}

func TestModelCapacityArbiter_FallbackCascade(t *testing.T) {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers: map[string]Tier{
			"frontier": {
				Models: []ModelDescriptor{
					{ID: "primary-frontier", Family: FamilyAnthropic, RPMLimit: 10, TPMLimit: 1000},
				},
				FallbackTier: "workhorse",
			},
			"workhorse": {
				Models: []ModelDescriptor{
					{ID: "backup-workhorse", Family: FamilyGoogle, RPMLimit: 100, TPMLimit: 10000},
				},
				FallbackTier: "oss-fast",
			},
		},
		Governance: GovernancePolicy{
			ExhaustionThresholdPercent: 80.0,
		},
	}

	tracker := NewLimitTracker()
	arbiter := NewModelCapacityArbiter(cfg, tracker)

	// Step 1: When primary model has available capacity, it is selected
	m1, err := arbiter.SelectModel("frontier")
	if err != nil || m1.ID != "primary-frontier" {
		t.Fatalf("expected primary-frontier, got %v (err: %v)", m1, err)
	}

	// Step 2: Simulate primary model rate limit (429)
	tracker.Record429("primary-frontier")

	// Step 3: Arbiter cascades seamlessly to workhorse tier
	m2, err := arbiter.SelectModel("frontier")
	if err != nil || m2.ID != "backup-workhorse" {
		t.Fatalf("expected cascade to backup-workhorse, got %v (err: %v)", m2, err)
	}
}

func TestModelCapacityArbiter_OrthogonalAuditor(t *testing.T) {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers: map[string]Tier{
			"frontier": {
				Models: []ModelDescriptor{
					{ID: "claude-opus", Family: FamilyAnthropic, RPMLimit: 100},
					{ID: "gemini-pro", Family: FamilyGoogle, RPMLimit: 100},
					{ID: "o3-mini", Family: FamilyOpenAI, RPMLimit: 100},
				},
			},
		},
		Governance: GovernancePolicy{
			OrthogonalAuditRequired: true,
		},
	}

	arbiter := NewModelCapacityArbiter(cfg, nil)

	// Invariant: If author is Anthropic, auditor must NOT be Anthropic
	auditor, err := arbiter.SelectOrthogonalAuditor(FamilyAnthropic, "frontier")
	if err != nil {
		t.Fatalf("unexpected error finding auditor: %v", err)
	}
	if auditor.Family == FamilyAnthropic {
		t.Fatalf("expected orthogonal family, got Anthropic")
	}
	if auditor.Family != FamilyGoogle && auditor.Family != FamilyOpenAI {
		t.Fatalf("unexpected auditor family: %s", auditor.Family)
	}
}
