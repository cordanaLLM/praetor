package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

var auditModelNow = time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)

func writeAuditCatalog(t *testing.T, models string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(router.DefaultConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "version: 1\ntiers:\n  work:\n    target_tasks: [implement]\n    models:\n" + models
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func auditModelEntry(id, extra string) string {
	return "      - {id: " + id + ", family: openai, cost_per_m_in: 1, cost_per_m_out: 1" + extra + "}\n"
}

func runModelCatalogAudit(t *testing.T, root string) (string, error) {
	t.Helper()
	var err error
	out, captureErr := captureStdout(t, func() error {
		err = auditModelCatalog(context.Background(), root, auditModelNow)
		return nil
	})
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	return out, err
}

func TestAuditModelCatalogSkipsWithoutCatalog(t *testing.T) {
	out, err := runModelCatalogAudit(t, t.TempDir())
	if err != nil || !strings.Contains(out, "[SKIP] model catalog freshness not checked") {
		t.Fatalf("absent catalog: %q %v", out, err)
	}
}

func TestAuditModelCatalogPassesFreshCatalogAndUndatedEntries(t *testing.T) {
	root := writeAuditCatalog(t, auditModelEntry("fresh", `, as_of: "2026-10-01"`)+auditModelEntry("undated", ""))
	out, err := runModelCatalogAudit(t, root)
	if err != nil || !strings.Contains(out, "[PASS] model catalog") {
		t.Fatalf("fresh catalog: %q %v", out, err)
	}
}

func TestAuditModelCatalogFailsStaleAndPreviewEntries(t *testing.T) {
	cases := map[string]string{
		"preview flag": auditModelEntry("flagged", ", preview: true"),
		"preview name": auditModelEntry("vendor-preview-03-25", ""),
		"stale entry":  auditModelEntry("old", `, as_of: "2026-04-01"`),
	}
	for name, models := range cases {
		out, err := runModelCatalogAudit(t, writeAuditCatalog(t, models))
		if err == nil || !strings.Contains(err.Error(), "[FAIL] model catalog") || !strings.Contains(err.Error(), "stale") {
			t.Errorf("%s passed the audit: %v", name, err)
		}
		if !strings.Contains(out, "  - ") {
			t.Errorf("%s: findings not printed: %q", name, out)
		}
	}
}

func TestAuditModelCatalogWindowBoundary(t *testing.T) {
	atWindow := auditModelEntry("edge", `, as_of: "2026-04-12"`)
	if _, err := runModelCatalogAudit(t, writeAuditCatalog(t, atWindow)); err != nil {
		t.Fatalf("an entry exactly at the 180-day window failed: %v", err)
	}
	over := auditModelEntry("edge", `, as_of: "2026-04-11"`)
	if _, err := runModelCatalogAudit(t, writeAuditCatalog(t, over)); err == nil {
		t.Fatal("an entry one day past the window passed")
	}
}

func TestAuditModelCatalogFailsOnUnloadableCatalog(t *testing.T) {
	root := writeAuditCatalog(t, "      - {id: broken}\n")
	if _, err := runModelCatalogAudit(t, root); err == nil || !strings.Contains(err.Error(), "not checked") {
		t.Fatalf("a catalog that does not load must fail, not skip: %v", err)
	}
}

func TestAuditModelCatalogRemedyNamesOfflinePruneForRetiredSeed(t *testing.T) {
	root := writeAuditCatalog(t, auditModelEntry("retired-seed-model", `, source: "seed"`))
	out, err := runModelCatalogAudit(t, root)
	if err == nil {
		t.Fatal("retired seed entry must fail the audit")
	}
	if !strings.Contains(err.Error(), "retired seed entries leave with praetorctl models sync --prune --discover-local=false, or by hand") {
		t.Fatalf("unexpected remedy in error: %v (stdout: %q)", err, out)
	}
}

func TestModelsSyncCLIReportsPreviewEntryAndSkipsProbeWhenAsked(t *testing.T) {
	root := writeAuditCatalog(t, auditModelEntry("kept-preview", ", preview: true"))
	path := filepath.Join(root, filepath.FromSlash(router.DefaultConfigPath))
	out, err := captureStdout(t, func() error {
		return runModels([]string{"sync", "--config=" + path, "--discover-local=false", "--probe-aliases=false"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Catalog stale:       kept-preview: marked preview") {
		t.Fatalf("sync did not report the preview entry: %q", out)
	}
	list, err := captureStdout(t, func() error { return runModels([]string{"list", "--config=" + path}) })
	if err != nil || !strings.Contains(list, "kept-preview") || !strings.Contains(list, "[preview]") {
		t.Fatalf("list must mark the preview entry: %q %v", list, err)
	}
}

func TestRepositoryModelCatalogSyncRoundTrip(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, filepath.FromSlash(router.DefaultConfigPath))
	original, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(copyPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runModels([]string{"sync", "--config=" + copyPath, "--discover-local=false"}); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	synced, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(synced) {
		t.Fatal("tracked model catalog does not match 'models sync --discover-local=false' output; run 'praetorctl models sync --discover-local=false'")
	}
}
