package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

// RawModelInfo represents metadata from upstream LiteLLM/OpenRouter catalogs.
type RawModelInfo struct {
	MaxTokens        int     `json:"max_tokens"`
	MaxInputTokens   int     `json:"max_input_tokens"`
	InputCostPerM    float64 `json:"input_cost_per_token"`
	OutputCostPerM   float64 `json:"output_cost_per_token"`
	Mode             string  `json:"mode"`
	SupportsVision   bool    `json:"supports_vision"`
	SupportsToolCall bool    `json:"supports_function_calling"`
}

// SyncOptions configures catalog synchronization behavior.
type SyncOptions struct {
	IncludeOpenWeights bool
	DiscoverLocal      bool
	LocalEndpoints     []string
	MinContextWindow   int
}

// SyncResult details models updated during catalog synchronization.
type SyncResult struct {
	TotalModels   int
	HeavyFrontier int
	MidWeight     int
	LightWeight   int
	Nano          int
	LocalModels   int
}

// matchParamTag safely matches model parameter tags with boundary assertions (e.g. 2b won't match 32b).
func matchParamTag(s string, tags ...string) bool {
	for _, tag := range tags {
		idx := strings.Index(s, tag)
		for idx != -1 {
			prefixOk := idx == 0 || !asciiDigit(s[idx-1])
			endIdx := idx + len(tag)
			suffixOk := endIdx == len(s) || !asciiAlphanumeric(s[endIdx])
			if prefixOk && suffixOk {
				return true
			}
			next := strings.Index(s[idx+1:], tag)
			if next == -1 {
				break
			}
			idx += 1 + next
		}
	}
	return false
}

func asciiDigit(b byte) bool        { return b >= '0' && b <= '9' }
func asciiAlphanumeric(b byte) bool { return asciiDigit(b) || (b >= 'a' && b <= 'z') }

func containsModelTag(model string, tags ...string) bool {
	for _, tag := range tags {
		if strings.Contains(model, tag) {
			return true
		}
	}
	return false
}

// ClassifyTier preserves the legacy name heuristic; it is not measured capability evidence.
func ClassifyTier(modelID string, family ModelFamily, benchmarkELO float64) string {
	id := strings.ToLower(modelID)
	if matchParamTag(id, "0.5b", "1b", "1.5b", "1.7b", "2b", "3b", "3.8b", "4b") || containsModelTag(id, "smollm", "tiny", "nano", "micro", "phi-3-mini", "phi-3.5-mini") {
		return "nano"
	}
	if matchParamTag(id, "20b", "22b", "27b", "30b", "32b", "35b") || containsModelTag(id, "qwen3.8", "qwen3", "codestral", "gpt-oss:20b", "gpt-oss-small") {
		return "midweight"
	}
	if matchParamTag(id, "5b", "6b", "7b", "8b", "9b", "14b", "16b") || containsModelTag(id, "qwythos", "gemma-2-9b", "phi-4", "haiku") {
		return "lightweight"
	}
	return "heavy-frontier"
}

// DetectFamily preserves legacy catalog naming; dispatch requires an explicit binding.
func DetectFamily(modelID string) ModelFamily {
	lower := strings.ToLower(modelID)
	families := []struct {
		family ModelFamily
		tags   []string
	}{
		{FamilyAnthropic, []string{"claude"}}, {FamilyGoogle, []string{"gemini"}},
		{FamilyOpenAI, []string{"gpt", "o1", "o3"}}, {"xai", []string{"grok"}},
		{"deepseek", []string{"deepseek"}}, {"mistral", []string{"mistral", "codestral"}},
		{"qwen", []string{"qwen"}}, {"cohere", []string{"command"}},
	}
	for _, entry := range families {
		if containsModelTag(lower, entry.tags...) {
			return entry.family
		}
	}
	return FamilyOpenWeights
}

// queryOllamaEndpoint queries a single Ollama API endpoint for installed models.
func queryOllamaEndpoint(ctx context.Context, client *http.Client, ep string) (models []ModelDescriptor, resultErr error) {
	req, err := http.NewRequestWithContext(ctx, "GET", ep+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, resp.Body.Close()) }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local catalog returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxRoutingFileBytes+1))
	if err != nil {
		return nil, err
	}

	if len(body) > MaxRoutingFileBytes {
		return nil, errors.New("local catalog exceeds byte limit")
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	if len(payload.Models) > MaxRoutingModels {
		return nil, errors.New("local catalog exceeds model limit")
	}
	res := make([]ModelDescriptor, 0, len(payload.Models))
	for _, m := range payload.Models {
		res = append(res, ModelDescriptor{
			ID:          m.Name,
			Family:      FamilyOpenWeights,
			RPMLimit:    50000,
			TPMLimit:    20000000,
			CostPerMIn:  0.0,
			CostPerMOut: 0.0,
		})
	}
	return res, nil
}

// DiscoverLocalModels queries local Ollama/vLLM daemon endpoints.
func DiscoverLocalModels(ctx context.Context, endpoints []string) ([]ModelDescriptor, error) {
	discovered := make([]ModelDescriptor, 0)
	client := &http.Client{Timeout: 3 * time.Second}

	for _, ep := range endpoints {
		if strings.Contains(ep, "11434") {
			models, err := queryOllamaEndpoint(ctx, client, ep)
			if err != nil {
				return nil, fmt.Errorf("discover local models: %w", err)
			}
			discovered = append(discovered, models...)
		}
	}

	return discovered, nil
}

type catalogEntry struct {
	id      string
	elo     float64
	rpm     int
	tpm     int
	costIn  float64
	costOut float64
}

var legacySeedCatalog = []catalogEntry{
	// Nano / Micro (<= 4B)
	{"smollm2:1.7b", 1120, 50000, 20000000, 0.0, 0.0},
	{"qwen2.5-coder:1.5b", 1150, 50000, 20000000, 0.0, 0.0},
	{"qwen2.5:3b", 1160, 50000, 20000000, 0.0, 0.0},
	{"phi-3.5-mini:3.8b", 1180, 50000, 20000000, 0.0, 0.0},
	{"llama-3.2:1b", 1100, 50000, 20000000, 0.0, 0.0},
	{"llama-3.2:3b", 1165, 50000, 20000000, 0.0, 0.0},
	{"gemma-2-2b", 1155, 50000, 20000000, 0.0, 0.0},

	// Lightweight (5B - 16B: 7B, 8B, 9B, 14B)
	{"hf.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF:Q8_0", 1240, 50000, 20000000, 0.0, 0.0},
	{"gemma-2-9b", 1235, 5000, 2000000, 0.20, 0.20},
	{"qwen-2.5-coder-7b-instruct", 1225, 50000, 20000000, 0.0, 0.0},
	{"qwen-2.5-coder-14b-instruct", 1245, 50000, 20000000, 0.0, 0.0},
	{"meta-llama/llama-3.1-8b-instruct", 1210, 5000, 5000000, 0.15, 0.15},
	{"phi-4:14b", 1250, 50000, 20000000, 0.0, 0.0},
	{"claude-3-5-haiku-20241022", 1230, 2000, 100000, 0.80, 4.0},

	// Mid-Weight Workhorses (20B - 35B: 20B, 22B, 27B, 30B, 32B)
	{"hf.co/unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", 1280, 50000, 20000000, 0.0, 0.0},
	{"qwen-2.5-coder-32b-instruct", 1260, 5000, 5000000, 0.20, 0.60},
	{"codestral-2501", 1270, 1000, 500000, 0.30, 0.90},
	{"gpt-oss-small", 1200, 50000, 20000000, 0.0, 0.0},

	// Heavy & Frontier Reasoning (70B+ & Cloud APIs)
	{"claude-3-7-sonnet-20250219", 1340, 1000, 80000, 3.0, 15.0},
	{"claude-3-5-sonnet-20241022", 1315, 1000, 80000, 3.0, 15.0},
	{"claude-3-opus-20240229", 1305, 50, 40000, 15.0, 75.0},
	{"gemini-2.5-pro-preview-03-25", 1360, 300, 2000000, 1.25, 5.0},
	{"gemini-2.5-flash-preview-03-25", 1320, 2000, 4000000, 0.075, 0.30},
	{"gemini-2.0-flash", 1290, 2000, 4000000, 0.10, 0.40},
	{"gpt-4.5-preview-2025-02-27", 1355, 200, 100000, 75.0, 150.0},
	{"o3-mini", 1345, 500, 1000000, 1.10, 4.40},
	{"o1", 1335, 500, 100000, 15.0, 60.0},
	{"gpt-4o-2024-11-20", 1295, 2000, 450000, 2.50, 10.0},
	{"grok-3", 1350, 100, 200000, 5.0, 15.0},
	{"grok-3-mini", 1280, 500, 500000, 0.50, 2.0},
	{"deepseek-reasoner", 1340, 5000, 5000000, 0.55, 2.19},
	{"deepseek-chat", 1285, 10000, 10000000, 0.14, 0.28},
	{"mistral-large-2411", 1290, 500, 250000, 2.0, 6.0},
	{"qwen-2.5-max", 1325, 2000, 1000000, 1.60, 6.40},
	{"meta-llama/llama-3.3-70b-instruct", 1275, 5000, 5000000, 0.35, 0.40},
}

func defaultRoutingTiers() map[string]Tier {
	return map[string]Tier{
		"heavy-frontier": {
			Description:  "Tier 3 Heavy & Frontier Reasoning (70B+ & Frontier APIs: Claude, Gemini, GPT, O3, R1)",
			TargetTasks:  []string{"architecture_synthesis", "hiss_proof_verification", "ast_semantic_collision", "waiver_signoff"},
			FallbackTier: "midweight",
		},
		"midweight": {
			Description:  "Tier 2 Mid-Weight Workhorses (20B-35B: 27B Qwen3.8, 30B Qwen3, 32B Coder, Codestral)",
			TargetTasks:  []string{"feature_implementation", "multi_file_refactors", "unit_test_suites", "ci_debugging"},
			FallbackTier: "lightweight",
		},
		"lightweight": {
			Description:  "Tier 1 Lightweight Models (5B-16B: 9B Qwythos/Gemma, 7B/14B Qwen, Phi-4, Haiku)",
			TargetTasks:  []string{"function_docstrings", "single_file_audits", "test_case_stubbing", "fast_cli_tools"},
			FallbackTier: "nano",
		},
		"nano": {
			Description:  "Tier 0 Micro & Nano Models (<= 4B: SmolLM2, 1.5B/3B Qwen, Phi-3.5-mini, Llama 3.2)",
			TargetTasks:  []string{"pre_commit_hooks", "commit_message_synthesis", "secret_entropy_scan", "ast_skeleton_filter"},
			FallbackTier: "",
		},
	}
}

func populateLocalModels(ctx context.Context, endpoints []string, tiers map[string]Tier, result *SyncResult) error {
	localModels, err := DiscoverLocalModels(ctx, endpoints)
	if err != nil {
		return err
	}
	for _, lm := range localModels {
		tierName := ClassifyTier(lm.ID, lm.Family, 0.0)
		currentTier := tiers[tierName]
		currentTier.Models = append(currentTier.Models, lm)
		tiers[tierName] = currentTier

		result.LocalModels++
		result.TotalModels++
		recordTierCount(result, tierName)
	}
	return nil
}

func recordTierCount(result *SyncResult, tierName string) {
	switch tierName {
	case "heavy-frontier":
		result.HeavyFrontier++
	case "midweight":
		result.MidWeight++
	case "lightweight":
		result.LightWeight++
	case "nano":
		result.Nano++
	}
}

// SyncCatalog writes legacy seed metadata and optional local model inventory.
// Seed prices, quota values and naming heuristics are not live provider observations.
func SyncCatalog(ctx context.Context, targetPath string, opts SyncOptions) (*SyncResult, error) {
	tiers := defaultRoutingTiers()
	result := &SyncResult{}

	for _, m := range legacySeedCatalog {
		family := DetectFamily(m.id)
		tierName := ClassifyTier(m.id, family, m.elo)

		desc := ModelDescriptor{
			ID:          m.id,
			Family:      family,
			RPMLimit:    m.rpm,
			TPMLimit:    m.tpm,
			CostPerMIn:  m.costIn,
			CostPerMOut: m.costOut,
		}

		currentTier := tiers[tierName]
		currentTier.Models = append(currentTier.Models, desc)
		tiers[tierName] = currentTier

		result.TotalModels++
		recordTierCount(result, tierName)
	}

	if opts.DiscoverLocal && len(opts.LocalEndpoints) > 0 {
		if err := populateLocalModels(ctx, opts.LocalEndpoints, tiers, result); err != nil {
			return nil, err
		}
	}

	cfg := RoutingConfig{
		Version: 1,
		Tiers:   tiers,
		Governance: GovernancePolicy{
			MaxConcurrentSameModel:     2,
			ExhaustionThresholdPercent: 80.0,
			OrthogonalAuditRequired:    true,
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal updated routing config: %w", err)
	}

	before, exists, err := contextopt.ObserveSnapshot(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	if err := contextopt.ReplaceSnapshot(ctx, targetPath, data, contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0644}); err != nil {
		return nil, fmt.Errorf("failed to write routing config to %s: %w", targetPath, err)
	}

	return result, nil
}
