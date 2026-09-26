package router

import (
	"context"
	"path/filepath"
	"strings"
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

	if res.TotalModels < 20 {
		t.Fatalf("expected at least 20 verified models across spectrum, got %d", res.TotalModels)
	}
	if res.HeavyFrontier == 0 {
		t.Fatalf("expected heavy-frontier models to be classified")
	}
	if res.MidWeight == 0 {
		t.Fatalf("expected midweight models to be classified")
	}
	if res.LightWeight == 0 {
		t.Fatalf("expected lightweight models to be classified")
	}
	if res.Nano == 0 {
		t.Fatalf("expected nano models to be classified")
	}

	// Verify file loads cleanly
	cfg, err := LoadRoutingConfig(targetPath)
	if err != nil {
		t.Fatalf("failed to load generated config: %v", err)
	}
	if len(cfg.Tiers) != 4 {
		t.Fatalf("expected 4 tiers in continuum, got %d", len(cfg.Tiers))
	}
}

func TestClassifyTier_Continuum(t *testing.T) {
	// 1. Nano & Micro (<= 4B)
	if tier := ClassifyTier("smollm2:1.7b"); tier != "nano" {
		t.Fatalf("expected nano for smollm2, got %s", tier)
	}
	if tier := ClassifyTier("qwen2.5-coder:1.5b"); tier != "nano" {
		t.Fatalf("expected nano for qwen 1.5b, got %s", tier)
	}
	if tier := ClassifyTier("phi-3.5-mini"); tier != "nano" {
		t.Fatalf("expected nano for phi-3.5-mini, got %s", tier)
	}
	if tier := ClassifyTier("llama-3.2:3b"); tier != "nano" {
		t.Fatalf("expected nano for llama 3.2 3b, got %s", tier)
	}

	// 2. Lightweight (5B - 16B: 7B, 8B, 9B, 14B)
	if tier := ClassifyTier("qwythos:9b"); tier != "lightweight" {
		t.Fatalf("expected lightweight for qwythos 9b, got %s", tier)
	}
	if tier := ClassifyTier("gemma-2-9b"); tier != "lightweight" {
		t.Fatalf("expected lightweight for gemma 9b, got %s", tier)
	}
	if tier := ClassifyTier("qwen2.5-coder:7b"); tier != "lightweight" {
		t.Fatalf("expected lightweight for qwen 7b, got %s", tier)
	}
	if tier := ClassifyTier("phi-4:14b"); tier != "lightweight" {
		t.Fatalf("expected lightweight for phi-4 14b, got %s", tier)
	}

	// 3. Mid-Weight Workhorse (20B - 35B: 20B, 22B, 27B, 30B, 32B)
	if tier := ClassifyTier("qwen3.8:27b"); tier != "midweight" {
		t.Fatalf("expected midweight for qwen3.8 27b, got %s", tier)
	}
	if tier := ClassifyTier("qwen3:30b"); tier != "midweight" {
		t.Fatalf("expected midweight for qwen3 30b, got %s", tier)
	}
	if tier := ClassifyTier("qwen-2.5-coder-32b"); tier != "midweight" {
		t.Fatalf("expected midweight for qwen 32b, got %s", tier)
	}
	if tier := ClassifyTier("codestral-2501"); tier != "midweight" {
		t.Fatalf("expected midweight for codestral, got %s", tier)
	}

	// 4. Heavy & Frontier Reasoning
	if tier := ClassifyTier("claude-3-opus-20240229"); tier != "heavy-frontier" {
		t.Fatalf("expected heavy-frontier for opus, got %s", tier)
	}
	if tier := ClassifyTier("gemini-2.5-pro"); tier != "heavy-frontier" {
		t.Fatalf("expected heavy-frontier for gemini pro, got %s", tier)
	}
	if tier := ClassifyTier("deepseek-reasoner"); tier != "heavy-frontier" {
		t.Fatalf("expected heavy-frontier for deepseek reasoner, got %s", tier)
	}
}

// Boundary: the parameter-count tag decides before any family name tag, it matches whole
// tokens only (2b never inside 32b, a22b never as a size), and the band edges hold.
func TestClassifyTierSizeBoundaries(t *testing.T) {
	cases := map[string]string{
		"model:2b": "nano", "model:32b": "midweight", "model-2b-instruct": "nano",
		"qwen3:4b": "nano", "model:4.9b": "nano", "model:5b": "lightweight",
		"model:19b": "lightweight", "model:20b": "midweight", "model:35b": "midweight",
		"model:36b": "heavy-frontier", "qwen3:235b": "heavy-frontier",
		"Qwen/Qwen3-235B-A22B": "heavy-frontier", "qwen3-30b-a3b": "midweight",
		"mixtral:8x7b": "heavy-frontier", "gpt-oss:20b": "midweight",
		"qwen3:latest": "midweight", "model-7bit": "heavy-frontier", "qwen2.5": "heavy-frontier",
		"hf.co/org/model-7B-GGUF:Q8_0": "lightweight",
	}
	for id, want := range cases {
		if got := ClassifyTier(id); got != want {
			t.Errorf("ClassifyTier(%q) = %s, want %s", id, got, want)
		}
	}
	for id, want := range map[string]float64{"a:32b": 32, "a-1.5b": 1.5, "x-7b-8b": 8, "m:8x22b": 176} {
		if got, ok := parameterBillions(id); !ok || got != want {
			t.Errorf("parameterBillions(%q) = %v, %v; want %v", id, got, ok, want)
		}
	}
	for _, id := range []string{"", "qwen3", "a22b", "claude-3-5-sonnet-20241022", "b", "7bit"} {
		if got, ok := parameterBillions(id); ok {
			t.Errorf("parameterBillions(%q) = %v, want no size", id, got)
		}
	}
}

// Positive, negative and boundary for the vendor-prefix family rule.
func TestDetectFamilyMatchesVendorPrefixOnly(t *testing.T) {
	cases := map[string]ModelFamily{
		"hf.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF:Q8_0": FamilyOpenWeights,
		"claude-sonnet-4-5":            FamilyAnthropic,
		"hf.co/someone/gpt-claude-mix": FamilyOpenAI,
		"hf.co/someone/mix-gpt-claude": FamilyOpenWeights,
		"hf.co/unsloth/Qwen3.8-27B":    "qwen",
		"gemma-2-9b":                   FamilyGoogle,
		"gpt-oss-small":                FamilyOpenAI,
		"meta-llama/llama-3.1-8b":      FamilyOpenWeights,
		"o3-mini":                      FamilyOpenAI,
		"my-o3-finetune":               FamilyOpenWeights,
		"":                             FamilyOpenWeights,
		"anthropic/claude-3-5-haiku":   FamilyAnthropic,
		"codestral-2501":               "mistral",
		"deepseek-ai/DeepSeek-R1-0528": "deepseek",
	}
	for id, want := range cases {
		if got := DetectFamily(id); got != want {
			t.Errorf("DetectFamily(%q) = %s, want %s", id, got, want)
		}
	}
	// The seed table records lineage its names do not show: Qwythos is a Qwen3.5 finetune.
	for _, tier := range seedCatalog().Tiers {
		for _, model := range tier.Models {
			if strings.Contains(model.ID, "Qwythos") && model.Family != "qwen" {
				t.Fatalf("Qwythos seed family = %s, want qwen", model.Family)
			}
		}
	}
}

func TestModelCapacityArbiter_FallbackCascade(t *testing.T) {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers: map[string]Tier{
			"heavy-frontier": {
				Models: []ModelDescriptor{
					{ID: "primary-frontier", Family: FamilyAnthropic, RPMLimit: 10, TPMLimit: 1000},
				},
				FallbackTier: "midweight",
			},
			"midweight": {
				Models: []ModelDescriptor{
					{ID: "backup-midweight", Family: FamilyGoogle, RPMLimit: 100, TPMLimit: 10000},
				},
				FallbackTier: "lightweight",
			},
		},
		Governance: GovernancePolicy{
			ExhaustionThresholdPercent: 80.0,
		},
	}

	tracker := NewLimitTracker()
	arbiter := NewModelCapacityArbiter(cfg, tracker)

	// Step 1: When primary model has available capacity, it is selected
	m1, err := arbiter.SelectModel("heavy-frontier")
	if err != nil || m1.ID != "primary-frontier" {
		t.Fatalf("expected primary-frontier, got %v (err: %v)", m1, err)
	}

	// Step 2: Simulate primary model rate limit (429)
	tracker.Record429("primary-frontier")

	// Step 3: Arbiter cascades seamlessly to midweight tier
	m2, err := arbiter.SelectModel("heavy-frontier")
	if err != nil || m2.ID != "backup-midweight" {
		t.Fatalf("expected cascade to backup-midweight, got %v (err: %v)", m2, err)
	}
}

func TestModelCapacityArbiter_OrthogonalAuditor(t *testing.T) {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers: map[string]Tier{
			"heavy-frontier": {
				Models: []ModelDescriptor{
					{ID: "claude-opus", Family: FamilyAnthropic, RPMLimit: 100},
					{ID: "gemini-pro", Family: FamilyGoogle, RPMLimit: 100},
				},
			},
		},
		Governance: GovernancePolicy{
			OrthogonalAuditRequired: true,
		},
	}

	arbiter := NewModelCapacityArbiter(cfg, nil)

	// Author is Anthropic -> Auditor must be Google
	auditor, err := arbiter.SelectOrthogonalAuditor(FamilyAnthropic, "heavy-frontier")
	if err != nil {
		t.Fatalf("failed to select orthogonal auditor: %v", err)
	}
	if auditor.Family == FamilyAnthropic {
		t.Fatalf("invariant violation: auditor family (%s) matches author family", auditor.Family)
	}
	if auditor.Family != FamilyGoogle {
		t.Fatalf("expected google auditor, got %s", auditor.Family)
	}
}
