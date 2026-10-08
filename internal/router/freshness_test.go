package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var freshnessNow = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func datedModel(id, asOf string, preview bool) ModelDescriptor {
	model := costTaskModel(id, 1, 1)
	model.AsOf, model.Preview = asOf, preview
	return model
}

func TestCatalogFindingsWindowBoundary(t *testing.T) {
	cfg := taskConfig(datedModel("exact", "2026-04-11", false), datedModel("one-over", "2026-04-10", false),
		datedModel("fresh", "2026-10-01", false), datedModel("undated", "", false), datedModel("future", "2027-01-01", false))
	findings := CatalogFindings(cfg, freshnessNow)
	if len(findings) != 1 || findings[0].Model != "one-over" || !strings.Contains(findings[0].Reason, "181 days old, beyond the 180-day window") {
		t.Fatalf("window boundary wrong: %v", findings)
	}
}

func TestCatalogFindingsPreviewByFlagAndName(t *testing.T) {
	cfg := taskConfig(datedModel("flagged", "2026-10-01", true), datedModel("vendor-model-preview-03-25", "2026-10-01", false),
		datedModel("stable", "2026-10-01", false))
	findings := CatalogFindings(cfg, freshnessNow)
	if len(findings) != 2 || findings[0].Model != "flagged" || findings[1].Model != "vendor-model-preview-03-25" {
		t.Fatalf("preview entries wrong: %v", findings)
	}
	for _, finding := range findings {
		if finding.Reason != "marked preview" {
			t.Fatalf("reason: %q", finding.Reason)
		}
	}
}

func TestCatalogFindingsGovernedWindow(t *testing.T) {
	cfg := taskConfig(datedModel("old", "2026-10-01", false))
	cfg.Governance.CatalogMaxAgeDays = 3
	if findings := CatalogFindings(cfg, freshnessNow); len(findings) != 1 || !strings.Contains(findings[0].Reason, "3-day window") {
		t.Fatalf("declared window ignored: %v", findings)
	}
	if got := CatalogMaxAge(taskConfig()); got != DefaultCatalogMaxAgeDays*24*time.Hour {
		t.Fatalf("default window: %v", got)
	}
}

func TestCatalogFindingsReportsStaleAndPreviewTogether(t *testing.T) {
	cfg := taskConfig(datedModel("both", "2025-01-01", false))
	if findings := CatalogFindings(cfg, freshnessNow); len(findings) != 1 {
		// Not flagged and not named preview: stale alone.
		t.Fatalf("findings: %v", findings)
	}
	cfg = taskConfig(datedModel("both", "2025-01-01", true))
	if findings := CatalogFindings(cfg, freshnessNow); len(findings) != 2 {
		t.Fatalf("a stale preview entry needs both findings: %v", findings)
	}
}

func writeCatalogText(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const syncAliasCatalog = `version: 1
gateway:
  address: https://gateway.example.invalid/v1
lanes:
  runner:
    harness: runner
    command: [runner, "{target}"]
tiers:
  lightweight:
    lane: runner
    models:
      - {id: gw-light, family: openai, provider: gw, alias: light, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: gw-dead, family: openai, provider: gw, alias: dead, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: old-preview, family: google, preview: true, as_of: "2025-01-01", cost_per_m_in: 0, cost_per_m_out: 0}
governance:
  catalog_max_age_days: 90
`

func TestSyncProbesAliasesAndReportsStalePreviewEntries(t *testing.T) {
	path := writeCatalogText(t, syncAliasCatalog)
	prober := fakeProber(map[string]error{"dead": errors.New("HTTP 400: Invalid model name passed")})
	result, err := syncCatalog(context.Background(), path, SyncOptions{ProbeAliases: true, Prober: prober, Now: freshnessNow}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AliasProbes) != 2 {
		t.Fatalf("probes: %+v", result.AliasProbes)
	}
	var reasons []string
	for _, finding := range result.Findings {
		reasons = append(reasons, finding.String())
	}
	joined := strings.Join(reasons, "\n")
	if !strings.Contains(joined, "old-preview: marked preview") || !strings.Contains(joined, "old-preview: as_of 2025-01-01") {
		t.Fatalf("stale preview entry not reported: %v", reasons)
	}
	cfg, err := LoadRoutingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSyncedAliases(t, cfg)
}

func assertSyncedAliases(t *testing.T, cfg *RoutingConfig) {
	t.Helper()
	byID := map[string]ModelDescriptor{}
	for _, model := range cfg.Tiers["lightweight"].Models {
		byID[model.ID] = model
	}
	if byID["gw-light"].AliasStatus != AliasAnswers || byID["gw-light"].AsOf != "2026-10-08" {
		t.Fatalf("answer not persisted: %+v", byID["gw-light"])
	}
	if byID["gw-dead"].AliasStatus != AliasUnanswered || !strings.Contains(byID["gw-dead"].AliasReason, "Invalid model name passed") {
		t.Fatalf("refusal not persisted: %+v", byID["gw-dead"])
	}
	if cfg.Gateway == nil || cfg.Lanes["runner"].Harness != "runner" || cfg.Tiers["lightweight"].Lane != "runner" || cfg.Governance.CatalogMaxAgeDays != 90 {
		t.Fatalf("sync dropped operator settings: gateway %+v lanes %+v tier lane %q window %d", cfg.Gateway, cfg.Lanes, cfg.Tiers["lightweight"].Lane, cfg.Governance.CatalogMaxAgeDays)
	}
}

func TestSyncWithoutProbeFlagMakesNoCall(t *testing.T) {
	path := writeCatalogText(t, syncAliasCatalog)
	prober := func(context.Context, GatewayConfig, string) error {
		t.Error("probe call without ProbeAliases")
		return nil
	}
	result, err := syncCatalog(context.Background(), path, SyncOptions{Prober: prober, Now: freshnessNow}, nil)
	if err != nil || len(result.AliasProbes) != 0 {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestSyncSeedEntriesAreNeverPreviewAndNeverAgeJudged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routing.yaml")
	result, err := syncCatalog(context.Background(), path, SyncOptions{Now: freshnessNow}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("a fresh seed catalog has findings: %v", result.Findings)
	}
	cfg, err := LoadRoutingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range cfg.Tiers {
		for _, model := range tier.Models {
			if model.AsOf != "" || IsPreviewModel(model) {
				t.Fatalf("seed entry %s: as_of %q", model.ID, model.AsOf)
			}
		}
	}
	far := freshnessNow.AddDate(10, 0, 0)
	if got := CatalogFindings(cfg, far); len(got) != 0 {
		t.Fatalf("a seed-only catalog turned stale with the wall clock: %v", got)
	}
}

func TestEntryFindingsIgnoresSeedSourceAsOf(t *testing.T) {
	far := freshnessNow.AddDate(10, 0, 0)
	stamped := ModelDescriptor{ID: "seed-with-old-date", Source: SourceSeed, AsOf: "2020-01-01"}
	if got := entryFindings(stamped, far, 24*time.Hour); len(got) != 0 {
		t.Fatalf("a seed-owned entry carrying an old as_of must not be age-judged: %v", got)
	}
	handMade := ModelDescriptor{ID: "hand-made", AsOf: "2020-01-01"}
	if got := entryFindings(handMade, far, 24*time.Hour); len(got) != 1 {
		t.Fatalf("a hand-made entry must still be age-judged: %v", got)
	}
	preview := ModelDescriptor{ID: "seed-preview", Source: SourceSeed, Preview: true}
	if got := entryFindings(preview, far, 24*time.Hour); len(got) != 1 {
		t.Fatalf("a seed preview entry must still be flagged: %v", got)
	}
}

func TestCatalogFindingsReportsRetiredSeedEntry(t *testing.T) {
	cfg := &RoutingConfig{
		Version: 1,
		Tiers: map[string]Tier{
			"work": {
				TargetTasks: []string{"implement"},
				Models: []ModelDescriptor{
					{ID: "retired-seed-model", Source: SourceSeed, CostRatesDeclared: true, CostPerMIn: 1, CostPerMOut: 1},
				},
			},
		},
	}
	findings := CatalogFindings(cfg, freshnessNow)
	if len(findings) != 1 || findings[0].Model != "retired-seed-model" || findings[0].Reason != "retired seed entry" {
		t.Fatalf("want retired seed entry finding, got: %v", findings)
	}
}
