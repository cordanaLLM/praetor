package router

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	// Prune rebuilds the catalog from the seed list and this run's local discovery,
	// removing every other entry. Without it a sync never removes an entry and
	// refuses to write when one would be lost.
	Prune bool
}

// SyncResult details the catalog a synchronization wrote.
type SyncResult struct {
	TotalModels   int
	HeavyFrontier int
	MidWeight     int
	LightWeight   int
	Nano          int
	// LocalModels counts written entries whose source is local discovery.
	LocalModels int
	// Preserved counts existing entries the seed list does not own that were kept.
	Preserved int
	// Removed lists, sorted, the model IDs a pruning sync dropped.
	Removed []string
	// DiscoveryFailures lists, one per endpoint, the local endpoints that did not answer
	// a non-pruning sync; the sync kept their catalogued entries.
	DiscoveryFailures []string
}

// ErrSyncWouldRemove means a sync without Prune would drop catalog entries.
var ErrSyncWouldRemove = errors.New("model catalog sync would remove entries")

// localDiscovery lists the models installed on local runtime endpoints.
type localDiscovery func(ctx context.Context, endpoints []string) ([]ModelDescriptor, error)

// parameterSize matches a parameter-count token such as 7b, 1.5b, 235b or 8x7b that
// starts after a separator, so the active-parameter tag of a mixture-of-experts name
// (a22b in qwen3-235b-a22b) and a version number (qwen2.5) never read as the size.
var parameterSize = regexp.MustCompile(`(?:^|[^a-z0-9.])(?:(\d+)x)?(\d+(?:\.\d+)?)b`)

// parameterBillions returns the largest parameter-count token in a lowercase model ID;
// NxMb counts N experts of M billion. A token must end at a separator, so 32b is 32,
// never 2, and 7bit is no size.
func parameterBillions(id string) (float64, bool) {
	largest, found := 0.0, false
	for _, match := range parameterSize.FindAllStringSubmatchIndex(id, MaxRoutingModels) {
		if match[1] < len(id) && asciiAlphanumeric(id[match[1]]) {
			continue
		}
		size, err := strconv.ParseFloat(id[match[4]:match[5]], 64)
		if err != nil {
			continue
		}
		if match[2] >= 0 {
			experts, err := strconv.ParseFloat(id[match[2]:match[3]], 64)
			if err != nil {
				continue
			}
			size *= experts
		}
		if !found || size > largest {
			largest, found = size, true
		}
	}
	return largest, found
}

// tierForSize maps a declared parameter count onto the tier bands: below 5 billion is
// nano, below 20 lightweight, up to 35 midweight, and anything larger heavy-frontier.
func tierForSize(billions float64) string {
	switch {
	case billions < 5:
		return "nano"
	case billions < 20:
		return "lightweight"
	case billions <= 35:
		return "midweight"
	default:
		return "heavy-frontier"
	}
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

// ClassifyTier preserves the legacy name heuristic; it is not measured capability
// evidence. A parameter-count tag in the ID decides first, so a 235B Qwen3 model is
// heavy-frontier whatever its family name; the name tags only place IDs without one.
func ClassifyTier(modelID string) string {
	id := strings.ToLower(modelID)
	if billions, ok := parameterBillions(id); ok {
		return tierForSize(billions)
	}
	switch {
	case containsModelTag(id, "smollm", "tiny", "nano", "micro", "phi-3-mini", "phi-3.5-mini"):
		return "nano"
	case containsModelTag(id, "qwen3.8", "qwen3", "codestral", "gpt-oss-small"):
		return "midweight"
	case containsModelTag(id, "qwythos", "phi-4", "haiku"):
		return "lightweight"
	default:
		return "heavy-frontier"
	}
}

// vendorFamilies maps the vendor prefix of a model name onto its training lineage.
// Open weights of a vendor with a hosted API in the catalog (gemma, gpt-oss, qwen) join
// that vendor's family; lineages without one share FamilyOpenWeights.
var vendorFamilies = []struct {
	family ModelFamily
	tags   []string
}{
	{FamilyAnthropic, []string{"claude"}}, {FamilyGoogle, []string{"gemini", "gemma"}},
	{FamilyOpenAI, []string{"gpt", "o1", "o3"}}, {"xai", []string{"grok"}},
	{"deepseek", []string{"deepseek"}}, {"mistral", []string{"mistral", "codestral"}},
	{"qwen", []string{"qwen"}}, {"cohere", []string{"command"}},
}

// DetectFamily preserves legacy catalog naming; dispatch requires an explicit binding.
// Only the start of the model name counts, taken after the last '/' of a hosted path
// such as hf.co/<org>/<name>: a finetune that names another vendor inside its name
// (Qwythos-9B-Claude-Mythos) does not inherit that vendor's family.
func DetectFamily(modelID string) ModelFamily {
	name := strings.ToLower(modelID)
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	for _, entry := range vendorFamilies {
		for _, tag := range entry.tags {
			if strings.HasPrefix(name, tag) {
				return entry.family
			}
		}
	}
	return FamilyOpenWeights
}

// seedLineage names the family of a seed entry whose name does not start with its
// lineage's vendor tag. A family is training lineage, not hosting: the orthogonal
// auditor exists to avoid correlated errors, which a local copy of a vendor's weights
// shares with that vendor's API. gpt-oss-small therefore stays openai by its name.
var seedLineage = map[string]ModelFamily{
	// The GGUF architecture is qwen35 per the Hugging Face model card, a Qwen3.5 finetune.
	"hf.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF:Q8_0": "qwen",
}

type catalogEntry struct {
	id      string
	rpm     int
	tpm     int
	costIn  float64
	costOut float64
}

var legacySeedCatalog = []catalogEntry{
	// Nano / Micro (<= 4B)
	{"smollm2:1.7b", 50000, 20000000, 0.0, 0.0},
	{"qwen2.5-coder:1.5b", 50000, 20000000, 0.0, 0.0},
	{"qwen2.5:3b", 50000, 20000000, 0.0, 0.0},
	{"phi-3.5-mini:3.8b", 50000, 20000000, 0.0, 0.0},
	{"llama-3.2:1b", 50000, 20000000, 0.0, 0.0},
	{"llama-3.2:3b", 50000, 20000000, 0.0, 0.0},
	{"gemma-2-2b", 50000, 20000000, 0.0, 0.0},

	// Lightweight (5B - 16B: 7B, 8B, 9B, 14B)
	{"hf.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF:Q8_0", 50000, 20000000, 0.0, 0.0},
	{"gemma-2-9b", 5000, 2000000, 0.20, 0.20},
	{"qwen-2.5-coder-7b-instruct", 50000, 20000000, 0.0, 0.0},
	{"qwen-2.5-coder-14b-instruct", 50000, 20000000, 0.0, 0.0},
	{"meta-llama/llama-3.1-8b-instruct", 5000, 5000000, 0.15, 0.15},
	{"phi-4:14b", 50000, 20000000, 0.0, 0.0},
	{"claude-3-5-haiku-20241022", 2000, 100000, 0.80, 4.0},

	// Mid-Weight Workhorses (20B - 35B: 20B, 22B, 27B, 30B, 32B)
	{"hf.co/unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M", 50000, 20000000, 0.0, 0.0},
	{"qwen-2.5-coder-32b-instruct", 5000, 5000000, 0.20, 0.60},
	{"codestral-2501", 1000, 500000, 0.30, 0.90},
	{"gpt-oss-small", 50000, 20000000, 0.0, 0.0},

	// Heavy & Frontier Reasoning (70B+ & Cloud APIs)
	{"claude-3-7-sonnet-20250219", 1000, 80000, 3.0, 15.0},
	{"claude-3-5-sonnet-20241022", 1000, 80000, 3.0, 15.0},
	{"claude-3-opus-20240229", 50, 40000, 15.0, 75.0},
	{"gemini-2.5-pro-preview-03-25", 300, 2000000, 1.25, 5.0},
	{"gemini-2.5-flash-preview-03-25", 2000, 4000000, 0.075, 0.30},
	{"gemini-2.0-flash", 2000, 4000000, 0.10, 0.40},
	{"gpt-4.5-preview-2025-02-27", 200, 100000, 75.0, 150.0},
	{"o3-mini", 500, 1000000, 1.10, 4.40},
	{"o1", 500, 100000, 15.0, 60.0},
	{"gpt-4o-2024-11-20", 2000, 450000, 2.50, 10.0},
	{"grok-3", 100, 200000, 5.0, 15.0},
	{"grok-3-mini", 500, 500000, 0.50, 2.0},
	{"deepseek-reasoner", 5000, 5000000, 0.55, 2.19},
	{"deepseek-chat", 10000, 10000000, 0.14, 0.28},
	{"mistral-large-2411", 500, 250000, 2.0, 6.0},
	{"qwen-2.5-max", 2000, 1000000, 1.60, 6.40},
	{"meta-llama/llama-3.3-70b-instruct", 5000, 5000000, 0.35, 0.40},
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

// SyncCatalog merges legacy seed metadata and optional local model inventory into
// the catalog at targetPath. Seed-owned entries are rewritten in place; every other
// existing entry is kept unless opts.Prune is set, and a sync that would lose an
// entry without it writes nothing and returns ErrSyncWouldRemove. Governance keys and
// default-tier descriptions, task labels and fallbacks the catalog declares are kept;
// only undeclared ones take the built-in defaults. A local endpoint that does not answer
// is listed in SyncResult.DiscoveryFailures, and refuses a pruning sync.
// Seed prices, quota values and naming heuristics are not live provider observations.
func SyncCatalog(ctx context.Context, targetPath string, opts SyncOptions) (*SyncResult, error) {
	return syncCatalog(ctx, targetPath, opts, DiscoverLocalModels)
}

func syncCatalog(ctx context.Context, targetPath string, opts SyncOptions, discover localDiscovery) (*SyncResult, error) {
	before, exists, err := contextopt.ObserveSnapshot(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	existing, err := existingCatalog(before, exists, targetPath)
	if err != nil {
		return nil, err
	}
	declared, err := declaredSettings(before, exists)
	if err != nil {
		return nil, err
	}
	discovered, failures, err := runDiscovery(ctx, opts, discover)
	if err != nil {
		return nil, err
	}
	cfg, result, err := planCatalog(existing, declared, discovered, opts.Prune)
	if err != nil {
		return nil, err
	}
	result.DiscoveryFailures = failures
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal updated routing config: %w", err)
	}
	// The write is bound to the observed bytes, so a catalog edited after it was
	// merged is refused instead of overwritten.
	if err := contextopt.ReplaceSnapshot(ctx, targetPath, data, contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0644}); err != nil {
		return nil, fmt.Errorf("failed to write routing config to %s: %w", targetPath, err)
	}
	return result, nil
}

// existingCatalog decodes the catalog observed before the sync. An absent file is
// an empty catalog; an unreadable one is refused, because merging needs its entries.
func existingCatalog(data []byte, exists bool, path string) (*RoutingConfig, error) {
	if !exists {
		return &RoutingConfig{}, nil
	}
	cfg, err := decodeRoutingConfig(data, path)
	if err != nil {
		return nil, fmt.Errorf("refusing to sync over a catalog that does not load: %w", err)
	}
	return cfg, nil
}

// runDiscovery lists local models when asked. An endpoint that did not answer is
// reported, not fatal: its models, if catalogued, stay through the merge. A pruning
// sync refuses instead, because it would drop that endpoint's entries, and a canceled
// context stops the sync whichever mode it runs in.
func runDiscovery(ctx context.Context, opts SyncOptions, discover localDiscovery) ([]ModelDescriptor, []string, error) {
	if !opts.DiscoverLocal || len(opts.LocalEndpoints) == 0 {
		return nil, nil, nil
	}
	discovered, err := discover(ctx, opts.LocalEndpoints)
	if err == nil {
		return discovered, nil, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	if opts.Prune {
		return nil, nil, fmt.Errorf("refusing to prune from a partial local inventory: %w", err)
	}
	return discovered, discoveryFailures(err), nil
}

// discoveryFailures lists one message per endpoint failure a discovery joined.
func discoveryFailures(err error) []string {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []string{err.Error()}
	}
	failures := make([]string, 0, len(joined.Unwrap()))
	for _, failure := range joined.Unwrap() {
		failures = append(failures, failure.Error())
	}
	return failures
}

// planCatalog merges the seed list, the existing catalog and discovered local
// models, then refuses any removal the caller did not ask to prune.
func planCatalog(existing *RoutingConfig, declared settingsPresence, discovered []ModelDescriptor, prune bool) (*RoutingConfig, *SyncResult, error) {
	cfg := seedCatalog()
	keepDeclaredSettings(cfg, existing, declared)
	if !prune {
		preserveUnowned(cfg, existing, modelIDs(cfg))
	}
	addDiscovered(cfg, discovered)
	removed := missingIDs(existing, cfg)
	if len(removed) > 0 && !prune {
		return nil, nil, fmt.Errorf("%w: %s", ErrSyncWouldRemove, strings.Join(removed, ", "))
	}
	if err := ValidateRoutingConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("merged catalog: %w", err)
	}
	result := summarizeCatalog(cfg, existing)
	result.Removed = removed
	return cfg, result, nil
}

// seedCatalog builds the default tiers holding only the seed-owned entries.
func seedCatalog() *RoutingConfig {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers:   defaultRoutingTiers(),
		Governance: GovernancePolicy{
			MaxConcurrentSameModel:     2,
			ExhaustionThresholdPercent: 80.0,
			OrthogonalAuditRequired:    true,
		},
	}
	for _, m := range legacySeedCatalog {
		family, ok := seedLineage[m.id]
		if !ok {
			family = DetectFamily(m.id)
		}
		appendModel(cfg.Tiers, ClassifyTier(m.id), ModelDescriptor{
			ID: m.id, Family: family, Source: SourceSeed,
			RPMLimit: m.rpm, TPMLimit: m.tpm,
			CostPerMIn: m.costIn, CostPerMOut: m.costOut, CostRatesDeclared: true,
		})
	}
	return cfg
}

// preserveUnowned keeps every existing entry the seed list does not own, in its
// tier and order, and keeps tiers the defaults do not define. An entry marked as
// seed that the seed list no longer carries is retired, not kept, so the removal
// check reports it and only a pruning sync drops it.
func preserveUnowned(cfg, existing *RoutingConfig, owned map[string]bool) {
	for name, tier := range existing.Tiers {
		if _, ok := cfg.Tiers[name]; !ok {
			cfg.Tiers[name] = Tier{Description: tier.Description, TargetTasks: tier.TargetTasks, FallbackTier: tier.FallbackTier}
		}
		for _, model := range tier.Models {
			if !owned[model.ID] && model.Source != SourceSeed {
				appendModel(cfg.Tiers, name, model)
			}
		}
	}
}

// addDiscovered appends discovered local models the catalog does not already carry.
// An ID the seed list or an existing entry declares keeps that entry.
func addDiscovered(cfg *RoutingConfig, discovered []ModelDescriptor) {
	present := modelIDs(cfg)
	for _, model := range discovered {
		if present[model.ID] {
			continue
		}
		present[model.ID] = true
		model.Source = SourceLocal
		model.CostRatesDeclared = true
		appendModel(cfg.Tiers, ClassifyTier(model.ID), model)
	}
}

func appendModel(tiers map[string]Tier, name string, model ModelDescriptor) {
	tier := tiers[name]
	tier.Models = append(tier.Models, model)
	tiers[name] = tier
}

func modelIDs(cfg *RoutingConfig) map[string]bool {
	ids := make(map[string]bool)
	for _, tier := range cfg.Tiers {
		for _, model := range tier.Models {
			ids[model.ID] = true
		}
	}
	return ids
}

// missingIDs lists, sorted, the existing model IDs the planned catalog lacks.
func missingIDs(existing, planned *RoutingConfig) []string {
	kept := modelIDs(planned)
	var missing []string
	for _, tier := range existing.Tiers {
		for _, model := range tier.Models {
			if !kept[model.ID] {
				missing = append(missing, model.ID)
			}
		}
	}
	slices.Sort(missing)
	return missing
}

func summarizeCatalog(cfg, existing *RoutingConfig) *SyncResult {
	before := modelIDs(existing)
	result := &SyncResult{}
	for name, tier := range cfg.Tiers {
		for _, model := range tier.Models {
			result.TotalModels++
			recordTierCount(result, name)
			if model.Source == SourceLocal {
				result.LocalModels++
			}
			if model.Source != SourceSeed && before[model.ID] {
				result.Preserved++
			}
		}
	}
	return result
}
