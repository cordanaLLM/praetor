// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// efficiencyRecords is one records row in the current schema: metric epoch, disposition and
// every vector field with a provenance label.
const efficiencyRecords = `[{"number": 1, "head_branch": "feat/main", "title": "Main", "created_at": "2026-10-01T10:00:00Z", "merged_at": "2026-10-01T11:00:00Z",
  "disposition": "qualified", "metric_epoch": "2026-10-10",
  "tokens_by_provider": {"value": {"anthropic": 10}, "provenance": "measured"},
  "wall_seconds": {"value": 3600, "provenance": "measured"},
  "review_rounds": {"value": 1, "provenance": "measured"},
  "retries": {"value": 0, "provenance": "modeled"},
  "operator_minutes": {"value": 2.5, "provenance": "cited"},
  "escaped_defects": {"value": 0, "provenance": "measured"}}]`

func TestRunEfficiency_Positive(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsPath, []byte(efficiencyRecords), 0o600); err != nil {
		t.Fatal(err)
	}

	// Test table format
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--format=table"}); err != nil {
		t.Fatalf("unexpected error running efficiency table: %v", err)
	}

	// Test json format
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--format=json"}); err != nil {
		t.Fatalf("unexpected error running efficiency json: %v", err)
	}

	// Test --json alias
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--json"}); err != nil {
		t.Fatalf("unexpected error running efficiency --json: %v", err)
	}
}

func TestRunEfficiency_Negative_InvalidArgs(t *testing.T) {
	// Positional arguments refused
	if err := runEfficiency([]string{"unexpected-arg"}); err == nil {
		t.Error("expected error when positional arguments are supplied")
	}

	// Invalid format
	if err := runEfficiency([]string{"--format=xml"}); err == nil {
		t.Error("expected error for invalid format 'xml'")
	}

	// Misspelled manifest field must fail
	dir := t.TempDir()
	badManifest := `version: 1
repository:
  owner: "example"
  name: "repo"
efficiency:
  frontier_modelz: ["claude"]
`
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte(badManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runEfficiency([]string{"--path=" + dir}); err == nil {
		t.Error("expected error for misspelled manifest key in efficiency policy")
	}
}

func TestRunEfficiency_Boundary_EmptySource(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(prsPath, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--json"}); err != nil {
		t.Fatalf("unexpected error running efficiency with empty PR list: %v", err)
	}
}

func TestCommandTableRegistersEfficiency(t *testing.T) {
	table := commandTable()
	if table["efficiency"] == nil {
		t.Error("efficiency command missing from command table")
	}
	if table["efficiency-ledger"] == nil {
		t.Error("efficiency-ledger command missing from command table")
	}
}

func TestPrintUsageCarriesEfficiency(t *testing.T) {
	table := coreCommandTable()
	if _, ok := table["efficiency"]; !ok {
		t.Error("efficiency missing from coreCommandTable")
	}
}

func TestRunEfficiencyTo_Positive_OutputCarriesUnitsAndNotMeasured(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsPath, []byte(efficiencyRecords), 0o600); err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	if err := runEfficiencyTo([]string{"--forge-records=" + prsPath}, &table); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "#1") || !strings.Contains(table.String(), "not measured") || strings.Contains(table.String(), "(PR)") {
		t.Errorf("table: %s", table.String())
	}
	var js bytes.Buffer
	if err := runEfficiencyTo([]string{"--forge-records=" + prsPath, "--json"}, &js); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Units []struct {
			IssueToMerge string `json:"issue_to_merge"`
			MetricEpoch  string `json:"metric_epoch"`
			Retries      struct {
				Provenance string `json:"provenance"`
			} `json:"retries"`
		} `json:"units"`
		Summary struct {
			Retries struct {
				Display string `json:"display"`
			} `json:"retries"`
		} `json:"milestone_summary"`
	}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil || len(decoded.Units) != 1 || decoded.Units[0].IssueToMerge != "not measured" {
		t.Fatalf("json: %v %s", err, js.String())
	}
	if u := decoded.Units[0]; u.MetricEpoch != "2026-10-10" || u.Retries.Provenance != "modeled" {
		t.Errorf("unit epoch and provenance must reach the JSON output: %+v", u)
	}
	if got := decoded.Summary.Retries.Display; got != "0.00 per qualified unit (total 0 over 1 units / 1 qualified) [modeled 1]" {
		t.Errorf("summary retries = %q", got)
	}
}

func TestRunEfficiencyTo_Negative_RecordsWithoutLedgerFieldsRefused(t *testing.T) {
	dir := t.TempDir()
	for name, records := range map[string]string{
		"no metric epoch":       strings.Replace(efficiencyRecords, `"metric_epoch": "2026-10-10",`, "", 1),
		"no provenance label":   strings.Replace(efficiencyRecords, `"retries": {"value": 0, "provenance": "modeled"}`, `"retries": {"value": 0}`, 1),
		"vector field omitted":  strings.Replace(efficiencyRecords, `"retries": {"value": 0, "provenance": "modeled"},`, "", 1),
		"misspelled record key": strings.Replace(efficiencyRecords, `"disposition"`, `"dispositon"`, 1),
	} {
		if records == efficiencyRecords {
			t.Fatalf("%s: the fixture edit did not apply", name)
		}
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(records), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runEfficiencyTo([]string{"--forge-records=" + path}, &bytes.Buffer{}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestRunEfficiencyTo_Negative_SourceAndManifestErrorsFail(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsPath, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"missing transcripts": {"--forge-records=" + prsPath, "--transcripts-dir=" + filepath.Join(dir, "none")},
		"missing spend log":   {"--forge-records=" + prsPath, "--spend-log=" + filepath.Join(dir, "none.jsonl")},
		"missing manifest":    {"--forge-records=" + prsPath, "--manifest=" + filepath.Join(dir, "none.yaml")},
		"bad forge records":   {"--forge-records=" + filepath.Join(dir, "none.json")},
	} {
		if err := runEfficiencyTo(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

func ledgerManifest(kind config.Forge) *config.Manifest {
	m := &config.Manifest{}
	m.Repository.Owner, m.Repository.Name, m.Repository.Forge = "o", "r", kind
	return m
}

func TestResolveLiveForge_Negative_MissingRepositoryOrToken(t *testing.T) {
	ctx := context.Background()
	manifest := ledgerManifest
	if d, note, err := resolveLiveForge(ctx, nil); d != nil || err != nil || !strings.Contains(note, "no repository") {
		t.Errorf("no repository: %v %q %v", d, note, err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	if d, note, err := resolveLiveForge(ctx, manifest("github")); d != nil || err != nil || !strings.Contains(note, "no forge token") {
		t.Errorf("no token: %v %q %v", d, note, err)
	}
}

func TestResolveLiveForge_Positive_KindFromManifest(t *testing.T) {
	ctx := context.Background()
	manifest := ledgerManifest
	t.Setenv("GITHUB_TOKEN", "token")
	d, note, err := resolveLiveForge(ctx, manifest("github"))
	if _, ok := d.(*forge.GitHubDriver); !ok || note != "" || err != nil {
		t.Errorf("github: %v %q %v", d, note, err)
	}
	if _, _, err := resolveLiveForge(ctx, manifest("bogus")); err == nil {
		t.Error("an unsupported forge kind must fail, not fall back to GitHub")
	}
}

func TestLoadEfficiencyPolicy_Boundary_NoManifestIsDefaultsExplicitMissingFails(t *testing.T) {
	dir := t.TempDir()
	if p, m, err := loadEfficiencyPolicy(dir, ""); p != nil || m != nil || err != nil {
		t.Errorf("no manifest in root: %v %v %v", p, m, err)
	}
	if _, _, err := loadEfficiencyPolicy(dir, filepath.Join(dir, "x.yaml")); err == nil {
		t.Error("an explicit manifest that is missing must fail")
	}
}
