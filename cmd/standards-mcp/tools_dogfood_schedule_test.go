package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func writeScheduleMCPFixture(t *testing.T, root string, changes map[string]any) {
	t.Helper()
	writeFixtureFile(t, root, ".config/archetypes/framework.yaml", auditLockSource)
	writeSuiteMCPFixture(t, root, filepath.Join(root, "transcript_full.jsonl"), false)
	if err := os.WriteFile(filepath.Join(root, "runner"), []byte("status-only runner identity fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"version": 1, "suite_config": filepath.Join(root, "suite.json"),
		"source_root": root, "state_dir": filepath.Join(root, "schedule-state"), "allow_remote": false,
		"runner_binary": filepath.Join(root, "runner")}
	for key, value := range changes {
		config[key] = value
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schedule.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleStatusMCPReadsWithoutRunningOrCreatingState(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeScheduleMCPFixture(t, root, nil)
	result := callTool(t, srv, "standards_dogfood_schedule_status", map[string]any{"config_path": "schedule.json"})
	if result.IsError {
		t.Fatalf("status failed: %+v", result)
	}
	var report dogfood.ScheduleReport
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "due" || report.Verified || report.Attempts != 0 || report.Suite != nil {
		t.Fatalf("status claimed execution: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "schedule-state")); !os.IsNotExist(err) {
		t.Fatal("status created state")
	}
}

func TestScheduleStatusMCPRejectsEmbeddedEscapesAndInvalidArguments(t *testing.T) {
	srv, root := newFixtureServer(t)
	for _, key := range []string{"source_root", "suite_config", "state_dir", "runner_binary"} {
		writeScheduleMCPFixture(t, root, map[string]any{key: t.TempDir()})
		result := callTool(t, srv, "standards_dogfood_schedule_status", map[string]any{"config_path": "schedule.json"})
		if !result.IsError {
			t.Fatalf("accepted embedded escape: %s", key)
		}
	}
	writeScheduleMCPFixture(t, root, nil)
	for _, args := range []map[string]any{{}, {"config_path": true}, {"config_path": ""},
		{"config_path": t.TempDir()}, {"config_path": "schedule.json", "run": true}} {
		if result := callTool(t, srv, "standards_dogfood_schedule_status", args); !result.IsError {
			t.Fatalf("accepted invalid arguments: %+v", args)
		}
	}
}
