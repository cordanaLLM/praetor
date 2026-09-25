package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/router"
)

// cliSyncFixture carries one local entry and one seed entry the seed list no longer owns.
const cliSyncFixture = `version: 1
tiers:
  nano:
    models:
      - {id: qwen2.5:0.5b, family: open-weights, source: local, rpm_limit: 1, tpm_limit: 1, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: retired-seed-model, family: open-weights, source: seed, rpm_limit: 1, tpm_limit: 1, cost_per_m_in: 0, cost_per_m_out: 0}
governance:
  exhaustion_threshold_percent: 80
`

func TestModelsSyncCLIRefusesRemovalUntilPruned(t *testing.T) {
	path := writeRouteCLIInput(t, cliSyncFixture)
	offline := []string{"sync", "--discover-local=false", "--config=" + path}
	_, err := captureStdout(t, func() error { return runModels(offline) })
	if !errors.Is(err, router.ErrSyncWouldRemove) || !strings.Contains(err.Error(), "--prune") {
		t.Fatalf("want a refusal naming --prune, got %v", err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != cliSyncFixture {
		t.Fatalf("refused sync changed the catalog: %v", readErr)
	}
	out, err := captureStdout(t, func() error { return runModels(append(offline, "--prune")) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pruned:              qwen2.5:0.5b", "Pruned:              retired-seed-model", "Kept, not seed-owned: 0 models"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestModelsSyncCLIKeepsLocalEntriesWithoutDiscovery(t *testing.T) {
	path := writeRouteCLIInput(t, strings.Replace(cliSyncFixture, "id: retired-seed-model, family: open-weights, source: seed,", "id: hand-added, family: open-weights,", 1))
	out, err := captureStdout(t, func() error { return runModels([]string{"sync", "--discover-local=false", "--config=" + path}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Kept, not seed-owned: 2 models") || strings.Contains(out, "Pruned:") {
		t.Fatalf("merge output:\n%s", out)
	}
	cfg, err := router.LoadRoutingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if models := cfg.Tiers["nano"].Models; models[len(models)-2].ID != "qwen2.5:0.5b" || models[len(models)-2].Source != router.SourceLocal {
		t.Fatalf("local entry not kept after the seed entries: %+v", models)
	}
}

// A local endpoint that fails is named in the output and does not fail the sync; the
// governance threshold the fixture declares survives it.
func TestModelsSyncCLIReportsSkippedEndpoint(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	path := writeRouteCLIInput(t, strings.Replace(cliSyncFixture, "id: retired-seed-model, family: open-weights, source: seed,", "id: hand-added, family: open-weights,", 1))
	out, err := captureStdout(t, func() error {
		return runModels([]string{"sync", "--config=" + path, "--local-endpoints=" + down.URL})
	})
	if err != nil {
		t.Fatalf("a failed endpoint failed the sync: %v", err)
	}
	if !strings.Contains(out, "Endpoint skipped:    local model endpoint "+down.URL+": local catalog returned HTTP 503") {
		t.Fatalf("output lacks the skipped endpoint:\n%s", out)
	}
	cfg, err := router.LoadRoutingConfig(path)
	if err != nil || cfg.Governance.ExhaustionThresholdPercent != 80 || !cfg.Governance.OrthogonalAuditRequired {
		t.Fatalf("governance %+v, %v", cfg, err)
	}
}
