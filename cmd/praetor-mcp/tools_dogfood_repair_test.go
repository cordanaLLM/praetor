package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/repairrun"
)

func writeRepairExecutionMCPFixture(t *testing.T, srv *Server, root string) repairrun.Config {
	t.Helper()
	source := filepath.Join(root, "transcript_full.jsonl")
	writeSuiteMCPFixture(t, root, source, false)
	if err := os.WriteFile(source, []byte("changed pinned source"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_suite", map[string]any{"config_path": "suite.json", "artifact_dir": "failed-suite", "stage": "verify"})
	if !result.IsError {
		t.Fatal("suite fixture did not retain a real failure")
	}
	routing := filepath.Join(root, "routing.yaml")
	body := "version: 1\ntiers:\n  debug:\n    target_tasks: [ci_debugging]\n    models:\n      - id: cheap\n        family: openai\n        cost_per_m_in: 1\n        cost_per_m_out: 1\ngovernance:\n  exhaustion_threshold_percent: 80\n"
	if err := os.WriteFile(routing, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config := repairrun.Config{Version: 1, SourceRoot: root, SourceSHA: strings.Repeat("a", 40), StateDir: filepath.Join(root, "repair-state"),
		AllowedFiles: []string{"internal/util/fixture.go"}, TestPackages: []string{"./internal/util"}, TimeoutSeconds: 30, MaxPatchBytes: 1024,
		RepairPolicy: dogfood.RepairPolicy{RoutingConfig: routing, Task: "ci_debugging", InputTokens: 1000, OutputTokens: 500, MaxCost: 0.1},
		Provider:     repairrun.ProviderConfig{BaseURL: "https://provider.example/v1", TokenCommand: filepath.Join(root, "nonexistent-helper"), TokenCommandSHA256: strings.Repeat("b", 64), Model: "cheap", MaxInputBytes: 65536, MaxOutputTokens: 256}}
	saveRepairMCPConfig(t, root, config)
	return config
}

func saveRepairMCPConfig(t *testing.T, root string, config repairrun.Config) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repair.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func repairStatusMCPArgs() map[string]any {
	return map[string]any{"config_path": "repair.json", "report_path": "failed-suite/report.json"}
}

func TestRepairStatusMCPRealFailureWithoutExecution(t *testing.T) {
	srv, root := newFixtureServer(t)
	config := writeRepairExecutionMCPFixture(t, srv, root)
	before, err := os.ReadFile(filepath.Join(root, "failed-suite/report.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs())
	if result.IsError {
		t.Fatalf("status failed: %+v", result)
	}
	var report repairrun.Report
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ready" || report.Consumed || report.CandidateVerified {
		t.Fatalf("status claimed execution: %+v", report)
	}
	if _, err := os.Stat(config.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created execution state")
	}
	after, err := os.ReadFile(filepath.Join(root, "failed-suite/report.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("status changed retained source report")
	}
	if strings.Contains(result.Content[0].Text, "changed pinned source") {
		t.Fatal("status disclosed source content")
	}
}

func TestRepairStatusMCPRejectsExplicitAndEmbeddedEscapes(t *testing.T) {
	srv, root := newFixtureServer(t)
	config := writeRepairExecutionMCPFixture(t, srv, root)
	outside := t.TempDir()
	changes := []func(*repairrun.Config){
		func(c *repairrun.Config) { c.SourceRoot = outside }, func(c *repairrun.Config) { c.StateDir = outside },
		func(c *repairrun.Config) { c.RepairPolicy.RoutingConfig = filepath.Join(outside, "routing.yaml") },
		func(c *repairrun.Config) { c.RepairPolicy.UsagePath = filepath.Join(outside, "usage.json") },
		func(c *repairrun.Config) { c.Provider.TokenCommand = filepath.Join(outside, "helper") },
	}
	for index, change := range changes {
		altered := config
		change(&altered)
		saveRepairMCPConfig(t, root, altered)
		if result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs()); !result.IsError {
			t.Fatalf("accepted embedded escape%d", index)
		}
	}
	saveRepairMCPConfig(t, root, config)
	for _, args := range []map[string]any{{}, {"config_path": true, "report_path": "failed-suite/report.json"},
		{"config_path": "repair.json", "report_path": ""}, {"config_path": outside, "report_path": "failed-suite/report.json"},
		{"config_path": "repair.json", "report_path": outside}, {"config_path": "repair.json", "report_path": "failed-suite/report.json", "run": true}} {
		if result := callTool(t, srv, "standards_dogfood_repair_status", args); !result.IsError {
			t.Fatalf("accepted invalid args: %+v", args)
		}
	}
}

func TestRepairStatusMCPRejectsSymlinksAndMalformedReports(t *testing.T) {
	srv, root := newFixtureServer(t)
	config := writeRepairExecutionMCPFixture(t, srv, root)
	if err := os.Symlink(config.RepairPolicy.RoutingConfig, filepath.Join(root, "linked-routing")); err != nil {
		t.Fatal(err)
	}
	config.RepairPolicy.RoutingConfig = filepath.Join(root, "linked-routing")
	saveRepairMCPConfig(t, root, config)
	if result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs()); !result.IsError {
		t.Fatal("accepted symlink routing")
	}
	config.RepairPolicy.RoutingConfig = filepath.Join(root, "routing.yaml")
	saveRepairMCPConfig(t, root, config)
	report := filepath.Join(root, "failed-suite/report.json")
	if err := os.WriteFile(report, []byte(`{"source":"PRIVATE_SENTINEL"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs())
	if !result.IsError || strings.Contains(result.Content[0].Text, "PRIVATE_SENTINEL") {
		t.Fatal("malformed report accepted or echoed")
	}
}

func TestRepairStatusMCPConsumesTerminalWithoutOptionalCost(t *testing.T) {
	srv, root := newFixtureServer(t)
	config := writeRepairExecutionMCPFixture(t, srv, root)
	first := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs())
	if first.IsError {
		t.Fatal(first.Content[0].Text)
	}
	var terminal repairrun.Report
	if err := json.Unmarshal([]byte(first.Content[0].Text), &terminal); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(terminal.AttemptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "execution.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	started := map[string]any{"version": 1, "execution_key": terminal.ExecutionKey, "source_sha": terminal.SourceSHA, "config_sha256": terminal.ConfigSHA256, "started_at": "2026-09-12T12:00:00Z"}
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(terminal.AttemptDir, "started.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	terminal.Status, terminal.Consumed = "agent_failed", true
	terminal.Usage = &repairrun.Usage{InputTokens: 1000, OutputTokens: 100}
	data, err = json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(terminal.AttemptDir, "result.json")
	if err := os.WriteFile(resultPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs())
	if result.IsError {
		t.Fatalf("valid optional-cost terminal rejected: %+v", result)
	}
	var status repairrun.Report
	if err := json.Unmarshal([]byte(result.Content[0].Text), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "consumed" || !status.Consumed || len(status.Jobs) != 1 || status.Jobs[0].Status != "agent_failed" {
		t.Fatalf("terminal outcome lost: %+v", status)
	}
	after, err := os.ReadFile(resultPath)
	if err != nil || string(after) != string(data) {
		t.Fatal("status rewrote terminal record")
	}
}

func TestRepairStatusMCPKeepsBoundaryWhenOtherToolsAllowOutside(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeRepairExecutionMCPFixture(t, srv, root)
	srv.opts.AllowOutsideRoot = true
	outside := filepath.Join(t.TempDir(), "repair.json")
	if err := os.WriteFile(outside, []byte(`{"private":"outside-sentinel"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := repairStatusMCPArgs()
	args["config_path"] = outside
	result := callTool(t, srv, "standards_dogfood_repair_status", args)
	if !result.IsError || strings.Contains(result.Content[0].Text, "outside-sentinel") {
		t.Fatal("repair tool abandoned its root boundary")
	}
}
