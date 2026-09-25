package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func syncModel(id string, source ModelSource) ModelDescriptor {
	return ModelDescriptor{ID: id, Family: FamilyOpenWeights, Source: source, RPMLimit: 1, TPMLimit: 1, CostRatesDeclared: true}
}

func syncFixture(tiers map[string]Tier) *RoutingConfig {
	return &RoutingConfig{Version: 1, Tiers: tiers, Governance: GovernancePolicy{ExhaustionThresholdPercent: 80}}
}

// writeSyncCatalog writes cfg as the catalog a sync starts from and returns its path.
func writeSyncCatalog(t *testing.T, cfg *RoutingConfig) string {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func stubDiscovery(models ...ModelDescriptor) localDiscovery {
	return func(context.Context, []string) ([]ModelDescriptor, error) { return models, nil }
}

var discoverOpts = SyncOptions{DiscoverLocal: true, LocalEndpoints: []string{"http://localhost:11434"}}

func loadSynced(t *testing.T, path string) *RoutingConfig {
	t.Helper()
	cfg, err := LoadRoutingConfig(path)
	if err != nil {
		t.Fatalf("synced catalog does not load: %v", err)
	}
	return cfg
}

// findSynced returns the tier and descriptor holding id.
func findSynced(cfg *RoutingConfig, id string) (string, ModelDescriptor, bool) {
	for name, tier := range cfg.Tiers {
		for _, model := range tier.Models {
			if model.ID == id {
				return name, model, true
			}
		}
	}
	return "", ModelDescriptor{}, false
}

// requireRefused asserts the sync failed, with want when it is set, and left the
// catalog bytes untouched.
func requireRefused(t *testing.T, path string, opts SyncOptions, discover localDiscovery, want error) {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := syncCatalog(context.Background(), path, opts, discover)
	if err == nil {
		t.Fatalf("sync accepted, result %+v", result)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused sync still changed the catalog")
	}
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestSyncCatalogMergeKeepsLocalAndOperatorEntriesAndUpdatesSeed(t *testing.T) {
	stale := syncModel("o1", SourceOperator)
	stale.CostPerMIn, stale.Family = 99, FamilyOpenWeights
	path := writeSyncCatalog(t, syncFixture(map[string]Tier{
		"lightweight":    {Models: []ModelDescriptor{syncModel("qwen2.5-coder:7b", SourceLocal)}},
		"heavy-frontier": {Models: []ModelDescriptor{stale}},
		"gpu-local": {Description: "operator tier", TargetTasks: []string{"grunt"}, FallbackTier: "lightweight",
			Models: []ModelDescriptor{syncModel("cordana/qwen3-8-27b", SourceOperator)}},
	}))
	result, err := syncCatalog(context.Background(), path, SyncOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadSynced(t, path)
	if tier, model, ok := findSynced(cfg, "qwen2.5-coder:7b"); !ok || tier != "lightweight" || model.Source != SourceLocal {
		t.Fatalf("local entry lost or moved: tier=%q model=%+v", tier, model)
	}
	if tier, _, ok := findSynced(cfg, "cordana/qwen3-8-27b"); !ok || tier != "gpu-local" || cfg.Tiers["gpu-local"].TargetTasks[0] != "grunt" {
		t.Fatalf("operator entry or tier lost: tier=%q", tier)
	}
	if _, model, _ := findSynced(cfg, "o1"); model.CostPerMIn != 15.0 || model.Source != SourceSeed || model.Family != FamilyOpenAI {
		t.Fatalf("seed entry not updated in place: %+v", model)
	}
	if result.Preserved != 2 || result.LocalModels != 1 || len(result.Removed) != 0 || result.TotalModels != len(legacySeedCatalog)+2 {
		t.Fatalf("result %+v", result)
	}
}

func TestSyncCatalogDiscoveryAddsOnlyModelsNotAlreadyDeclared(t *testing.T) {
	path := writeSyncCatalog(t, syncFixture(map[string]Tier{
		"lightweight": {Models: []ModelDescriptor{syncModel("qwen2.5-coder:7b", SourceLocal)}},
	}))
	seedID := "hf.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF:Q8_0"
	discover := stubDiscovery(syncModel("qwen2.5-coder:7b", SourceOperator), syncModel(seedID, SourceOperator), syncModel("qwen3:30b", SourceOperator))
	result, err := syncCatalog(context.Background(), path, discoverOpts, discover)
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadSynced(t, path)
	if tier, model, _ := findSynced(cfg, "qwen3:30b"); tier != "midweight" || model.Source != SourceLocal {
		t.Fatalf("new local model: tier=%q model=%+v", tier, model)
	}
	if _, model, _ := findSynced(cfg, seedID); model.Source != SourceSeed {
		t.Fatalf("discovery took over a seed entry: %+v", model)
	}
	if result.LocalModels != 2 || result.Preserved != 1 {
		t.Fatalf("result %+v", result)
	}
}

func TestSyncCatalogPruneRemovesUnownedEntriesOnlyWhenAsked(t *testing.T) {
	fixture := syncFixture(map[string]Tier{
		"lightweight": {Models: []ModelDescriptor{syncModel("qwen2.5-coder:7b", SourceLocal), syncModel("hand-added", SourceOperator)}},
	})
	path := writeSyncCatalog(t, fixture)
	if _, err := syncCatalog(context.Background(), path, SyncOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := findSynced(loadSynced(t, path), "hand-added"); !ok {
		t.Fatal("sync without prune removed an entry")
	}
	result, err := syncCatalog(context.Background(), path, SyncOptions{Prune: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Removed, []string{"hand-added", "qwen2.5-coder:7b"}) || result.TotalModels != len(legacySeedCatalog) {
		t.Fatalf("prune result %+v", result)
	}
	path = writeSyncCatalog(t, fixture)
	opts := discoverOpts
	opts.Prune = true
	result, err = syncCatalog(context.Background(), path, opts, stubDiscovery(syncModel("qwen2.5-coder:7b", SourceOperator)))
	if err != nil || !reflect.DeepEqual(result.Removed, []string{"hand-added"}) {
		t.Fatalf("prune with rediscovery: removed=%v err=%v", result, err)
	}
}

func TestSyncCatalogRefusesRemovalWithoutPrune(t *testing.T) {
	path := writeSyncCatalog(t, syncFixture(map[string]Tier{
		"nano": {Models: []ModelDescriptor{syncModel("retired-seed-model", SourceSeed), syncModel("qwen2.5:0.5b", SourceLocal)}},
	}))
	requireRefused(t, path, SyncOptions{}, nil, ErrSyncWouldRemove)
	result, err := syncCatalog(context.Background(), path, SyncOptions{Prune: true, DiscoverLocal: true}, nil)
	if err != nil || !reflect.DeepEqual(result.Removed, []string{"qwen2.5:0.5b", "retired-seed-model"}) {
		t.Fatalf("prune without endpoints: result=%+v err=%v", result, err)
	}
}

func TestSyncCatalogRefusesUnreadableCatalogsAndFailedDiscovery(t *testing.T) {
	duplicate := writeSyncCatalog(t, syncFixture(map[string]Tier{
		"nano":        {Models: []ModelDescriptor{syncModel("twice", SourceLocal)}},
		"lightweight": {Models: []ModelDescriptor{syncModel("twice", SourceLocal)}},
	}))
	requireRefused(t, duplicate, SyncOptions{Prune: true}, nil, nil)
	unknownSource := writeSyncCatalog(t, syncFixture(map[string]Tier{"nano": {Models: []ModelDescriptor{syncModel("m", "remote")}}}))
	requireRefused(t, unknownSource, SyncOptions{}, nil, nil)
	empty := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	requireRefused(t, empty, SyncOptions{}, nil, nil)
}

// Negative + positive: an endpoint that does not answer is reported and the rest of the
// sync proceeds, keeping catalogued local entries; a pruning sync or a canceled context
// refuses instead, because either would act on a partial inventory.
func TestSyncCatalogIsolatesFailedDiscovery(t *testing.T) {
	fixture := syncFixture(map[string]Tier{"nano": {Models: []ModelDescriptor{syncModel("qwen2.5:0.5b", SourceLocal)}}})
	partial := func(context.Context, []string) ([]ModelDescriptor, error) {
		return []ModelDescriptor{syncModel("qwen3:30b", SourceOperator)},
			errors.Join(&EndpointError{Endpoint: "http://localhost:8000", Err: errors.New("connection refused")})
	}
	path := writeSyncCatalog(t, fixture)
	result, err := syncCatalog(context.Background(), path, discoverOpts, partial)
	if err != nil {
		t.Fatalf("a failed endpoint aborted the sync: %v", err)
	}
	if len(result.DiscoveryFailures) != 1 || result.DiscoveryFailures[0] != "local model endpoint http://localhost:8000: connection refused" {
		t.Fatalf("failures %q", result.DiscoveryFailures)
	}
	cfg := loadSynced(t, path)
	for _, id := range []string{"qwen2.5:0.5b", "qwen3:30b"} {
		if _, _, ok := findSynced(cfg, id); !ok {
			t.Fatalf("%s lost after a partial discovery", id)
		}
	}
	failing := func(context.Context, []string) ([]ModelDescriptor, error) { return nil, errors.New("daemon down") }
	result, err = syncCatalog(context.Background(), writeSyncCatalog(t, fixture), discoverOpts, failing)
	if err != nil || !reflect.DeepEqual(result.DiscoveryFailures, []string{"daemon down"}) {
		t.Fatalf("total discovery failure: result=%+v err=%v", result, err)
	}
	pruning := discoverOpts
	pruning.Prune = true
	requireRefused(t, writeSyncCatalog(t, fixture), pruning, partial, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := func(context.Context, []string) ([]ModelDescriptor, error) { return nil, context.Canceled }
	if _, err := syncCatalog(ctx, writeSyncCatalog(t, fixture), discoverOpts, canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery: %v", err)
	}
}

// Positive (#249): operator edits to governance and to default-tier metadata survive a
// sync, in both merge and prune mode.
func TestSyncCatalogKeepsDeclaredGovernanceAndTierMetadata(t *testing.T) {
	for _, prune := range []bool{false, true} {
		fixture := syncFixture(map[string]Tier{
			"heavy-frontier": {Description: "operator text", TargetTasks: []string{"custom_task"}, FallbackTier: "nano"},
			"nano":           {Description: "", TargetTasks: []string{}, FallbackTier: ""},
		})
		fixture.Governance = GovernancePolicy{MaxConcurrentSameModel: 5, ExhaustionThresholdPercent: 60, OrthogonalAuditRequired: false}
		path := writeSyncCatalog(t, fixture)
		if _, err := syncCatalog(context.Background(), path, SyncOptions{Prune: prune}, nil); err != nil {
			t.Fatalf("prune=%v: %v", prune, err)
		}
		cfg := loadSynced(t, path)
		if cfg.Governance != fixture.Governance {
			t.Fatalf("prune=%v: governance %+v, want %+v", prune, cfg.Governance, fixture.Governance)
		}
		heavy, nano := cfg.Tiers["heavy-frontier"], cfg.Tiers["nano"]
		if heavy.Description != "operator text" || !reflect.DeepEqual(heavy.TargetTasks, []string{"custom_task"}) || heavy.FallbackTier != "nano" {
			t.Fatalf("prune=%v: heavy-frontier metadata %+v", prune, heavy)
		}
		if nano.Description != "" || len(nano.TargetTasks) != 0 {
			t.Fatalf("prune=%v: explicit empty nano metadata overwritten: %+v", prune, nano)
		}
		if len(heavy.Models) == 0 {
			t.Fatalf("prune=%v: seed entries missing from heavy-frontier", prune)
		}
	}
}

// Negative: a pruning sync keeps a declared fallback that names an operator tier the
// rebuild drops, and refuses with that tier named rather than rewriting the fallback.
func TestSyncCatalogPruneRefusesDanglingDeclaredFallback(t *testing.T) {
	path := writeSyncCatalog(t, syncFixture(map[string]Tier{
		"heavy-frontier": {FallbackTier: "gpu-local"},
		"gpu-local":      {Models: []ModelDescriptor{syncModel("cordana/qwen3-8-27b", SourceOperator)}},
	}))
	if _, err := syncCatalog(context.Background(), path, SyncOptions{}, nil); err != nil {
		t.Fatalf("merge with an operator fallback: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = syncCatalog(context.Background(), path, SyncOptions{Prune: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown fallback tier gpu-local") {
		t.Fatalf("want a refusal naming gpu-local, got %v", err)
	}
	if after, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("refused prune changed the catalog: %v", readErr)
	}
}

// Boundary (#249): a catalog that declares neither governance nor tier metadata, or only
// part of them, receives the built-in default for each undeclared key only.
func TestSyncCatalogFillsOnlyUndeclaredSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routing.yaml")
	partial := "version: 1\ntiers:\n  midweight:\n    target_tasks: [only_this]\n    models: []\ngovernance:\n  exhaustion_threshold_percent: 55\n"
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncCatalog(context.Background(), path, SyncOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	cfg := loadSynced(t, path)
	defaults := seedCatalog()
	want := GovernancePolicy{MaxConcurrentSameModel: defaults.Governance.MaxConcurrentSameModel, ExhaustionThresholdPercent: 55, OrthogonalAuditRequired: defaults.Governance.OrthogonalAuditRequired}
	if cfg.Governance != want {
		t.Fatalf("governance %+v, want %+v", cfg.Governance, want)
	}
	mid, defaultMid := cfg.Tiers["midweight"], defaults.Tiers["midweight"]
	if !reflect.DeepEqual(mid.TargetTasks, []string{"only_this"}) || mid.Description != defaultMid.Description || mid.FallbackTier != defaultMid.FallbackTier {
		t.Fatalf("midweight %+v", mid)
	}
	if heavy := cfg.Tiers["heavy-frontier"]; !reflect.DeepEqual(heavy.TargetTasks, defaults.Tiers["heavy-frontier"].TargetTasks) {
		t.Fatalf("undeclared tier did not take the defaults: %+v", heavy)
	}
}

func TestSyncCatalogEmptyCatalogsReceiveTheSeedOnly(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "routing.yaml")
	noModels := writeSyncCatalog(t, syncFixture(map[string]Tier{"nano": {}, "midweight": {}}))
	for _, path := range []string{absent, noModels} {
		result, err := syncCatalog(context.Background(), path, SyncOptions{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalModels != len(legacySeedCatalog) || result.Preserved != 0 || len(result.Removed) != 0 {
			t.Fatalf("%s: result %+v", path, result)
		}
		if cfg := loadSynced(t, path); len(cfg.Tiers) != 4 {
			t.Fatalf("%s: %d tiers", path, len(cfg.Tiers))
		}
	}
}

// customTiers builds n operator tiers, each holding one operator entry.
func customTiers(n int) map[string]Tier {
	tiers := make(map[string]Tier, n)
	for i := 0; i < n; i++ {
		tiers[fmt.Sprintf("custom-%d", i)] = Tier{Models: []ModelDescriptor{syncModel(fmt.Sprintf("model-%d", i), SourceOperator)}}
	}
	return tiers
}

func TestSyncCatalogTierBound(t *testing.T) {
	atBound := writeSyncCatalog(t, syncFixture(customTiers(MaxRoutingTiers-4)))
	if _, err := syncCatalog(context.Background(), atBound, SyncOptions{}, nil); err != nil {
		t.Fatalf("merge reaching %d tiers refused: %v", MaxRoutingTiers, err)
	}
	if cfg := loadSynced(t, atBound); len(cfg.Tiers) != MaxRoutingTiers {
		t.Fatalf("%d tiers written", len(cfg.Tiers))
	}
	overBound := writeSyncCatalog(t, syncFixture(customTiers(MaxRoutingTiers-3)))
	requireRefused(t, overBound, SyncOptions{}, nil, nil)
}

func TestSyncCatalogModelAndTagBounds(t *testing.T) {
	tags := make([]string, MaxRoutingTags)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	tagged := syncModel("tagged", SourceOperator)
	tagged.Capabilities = tags
	models := []ModelDescriptor{tagged}
	seedLightweight := len(seedCatalog().Tiers["lightweight"].Models)
	for i := len(models); i < MaxModelsPerTier-seedLightweight; i++ {
		models = append(models, syncModel(fmt.Sprintf("local-%d", i), SourceLocal))
	}
	fixture := syncFixture(map[string]Tier{"lightweight": {Models: models}})
	path := writeSyncCatalog(t, fixture)
	if _, err := syncCatalog(context.Background(), path, SyncOptions{}, nil); err != nil {
		t.Fatalf("tier at %d models refused: %v", MaxModelsPerTier, err)
	}
	cfg := loadSynced(t, path)
	if _, model, _ := findSynced(cfg, "tagged"); len(model.Capabilities) != MaxRoutingTags || len(cfg.Tiers["lightweight"].Models) != MaxModelsPerTier {
		t.Fatal("bounded entry not preserved intact")
	}
	requireRefused(t, path, discoverOpts, stubDiscovery(syncModel("extra-7b", SourceOperator)), nil)
	tagged.Capabilities = append(tags, "overflow")
	requireRefused(t, writeSyncCatalog(t, syncFixture(map[string]Tier{"nano": {Models: []ModelDescriptor{tagged}}})), SyncOptions{}, nil, nil)
}
