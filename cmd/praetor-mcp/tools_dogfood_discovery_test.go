package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const discoveryTestPolicy = `{
  "version": 1,
  "rules": [{
    "key": "hiss:go",
    "title": "Go scanner",
    "kind": "scanner_extension",
    "matches": [".go"],
    "analyzer": "hiss"
  }]
}
`

func writeDiscoveryMCPFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "discovery-policy.json"), []byte(discoveryTestPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "source"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryMCPLocalObservePersistsPrivateReports(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeDiscoveryMCPFixture(t, root)
	result := callTool(t, srv, "standards_dogfood_discover", map[string]any{
		"path": "source", "policy_path": "discovery-policy.json", "artifact_dir": "discovery-run", "stage": "observe",
	})
	if result.IsError {
		t.Fatalf("discovery failed: %s", result.Content[0].Text)
	}
	var summary struct {
		ReportPath     string         `json:"report_path"`
		Status         string         `json:"status"`
		Complete       bool           `json:"complete"`
		RequestedCases int            `json:"requested_cases"`
		CandidateCount int            `json:"candidate_count"`
		CaseStatuses   map[string]int `json:"case_statuses"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != "observed" || !summary.Complete || summary.RequestedCases != 1 || summary.CandidateCount != 0 || summary.CaseStatuses["observed"] != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.ReportPath != filepath.Join(root, "discovery-run", "report.json") {
		t.Fatalf("unexpected confined report path: %q", summary.ReportPath)
	}
	if _, err := os.Stat(filepath.Join(root, "discovery-run", "report.json")); err != nil {
		t.Fatalf("report readback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "discovery-run", "case-001", "report.json")); err != nil {
		t.Fatalf("case evidence readback: %v", err)
	}
	if strings.Contains(result.Content[0].Text, "package main") {
		t.Fatal("MCP response disclosed source content")
	}
}

func TestDiscoveryMCPRejectsInvalidPathsAndRemoteObservation(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeDiscoveryMCPFixture(t, root)
	for _, args := range []map[string]any{
		{},
		{"path": true, "policy_path": "discovery-policy.json", "artifact_dir": "run"},
		{"path": "source", "policy_path": "discovery-policy.json", "artifact_dir": "run", "unknown": "x"},
		{"path": "source", "policy_path": "discovery-policy.json", "artifact_dir": "run", "stage": nil},
		{"path": "../outside", "policy_path": "discovery-policy.json", "artifact_dir": "run"},
		{"path": "source", "policy_path": filepath.Join(t.TempDir(), "policy.json"), "artifact_dir": "run"},
		{"path": "source", "policy_path": "discovery-policy.json", "artifact_dir": t.TempDir()},
	} {
		if result := callTool(t, srv, "standards_dogfood_discover", args); !result.IsError {
			t.Fatalf("accepted invalid discovery args: %+v", args)
		}
	}
	cohort := `{"version":1,"public_repositories":["https://github.com/spf13/cobra#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`
	if err := os.WriteFile(filepath.Join(root, "cohort.json"), []byte(cohort), 0o600); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_discover", map[string]any{
		"config_path": "cohort.json", "policy_path": "discovery-policy.json", "artifact_dir": "remote-run", "stage": "observe",
	})
	if !result.IsError || !strings.Contains(result.Content[0].Text, "public discovery requires server remote opt-in") {
		t.Fatalf("remote observation was not rejected: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "remote-run")); !os.IsNotExist(err) {
		t.Fatalf("remote rejection created evidence: %v", err)
	}
}

func TestDiscoveryMCPAllowsOfflinePublicPlanWithoutRemoteOptIn(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeDiscoveryMCPFixture(t, root)
	cohort := `{"version":1,"public_repositories":["https://github.com/spf13/cobra#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`
	if err := os.WriteFile(filepath.Join(root, "cohort.json"), []byte(cohort), 0o600); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_discover", map[string]any{
		"config_path": "cohort.json", "policy_path": "discovery-policy.json", "artifact_dir": "plan-run", "stage": "plan",
	})
	if result.IsError || !strings.Contains(result.Content[0].Text, `"status":"planned"`) {
		t.Fatalf("offline plan rejected: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "plan-run", "report.json")); err != nil {
		t.Fatalf("plan report missing: %v", err)
	}
}

func TestDiscoveryMCPSummaryBoundsErrors(t *testing.T) {
	summary := discoveryToolSummary(nil, errors.New(strings.Repeat("upstream observation failed\n", 200)))
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 2400 || summary["error_truncated"] != true || summary["verified"] != false {
		t.Fatalf("unbounded error response: %d bytes", len(encoded))
	}
}
