package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func TestHarvestWorkstationJSONPreservesRepositoryObservation(t *testing.T) {
	srv, root := newFixtureServer(t)
	repo := filepath.Join(root, "dev", "org", "local")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	result := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": "dev", "json": true})
	if result.IsError {
		t.Fatalf("inventory failed: %+v", result)
	}
	var report harvester.WorkstationReport
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	if !report.RepositoryInventoryComplete || len(report.RepositoryObservations) != 1 || report.RepositoryObservations[0].GitCommonDir == "" {
		t.Fatalf("missing complete local Git observation: %+v", report)
	}
}

func TestHarvestWorkstationJSONRejectsInvalidArgumentsAndUnknownGit(t *testing.T) {
	srv, root := newFixtureServer(t)
	invalid := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": ".", "json": "true"})
	expectError(t, "invalid json type", invalid, "json")
	if err := os.MkdirAll(filepath.Join(root, "dev", "broken", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": "dev", "json": true})
	if !result.IsError {
		t.Fatal("failed Git probes must not report successful complete inventory")
	}
	var report harvester.WorkstationReport
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	if report.RepositoryInventoryComplete || len(report.RepositoryObservations) != 1 {
		t.Fatalf("incomplete observation was lost: %+v", report)
	}
}

func TestHarvestWorkstationJSONEmptyBoundary(t *testing.T) {
	srv, root := newFixtureServer(t)
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": "empty", "json": true})
	var report harvester.WorkstationReport
	if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
		t.Fatal(err)
	}
	if result.IsError || !report.RepositoryInventoryComplete || len(report.RepositoryObservations) != 0 {
		t.Fatalf("empty directory is not a failed Git probe: %+v", result)
	}
}
