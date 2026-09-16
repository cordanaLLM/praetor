package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/planning"

	"github.com/cordanaLLM/praetor/internal/util"
)

func planningDraftFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "planning", "testdata", "valid-draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPlanningCLIPreparesMetadataAndPrivateArtifacts(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "draft.json")
	if err := os.WriteFile(input, planningDraftFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := captureStdout(t, func() error {
		return dispatchCommand("planning", []string{"prepare", "--input", input})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report planning.Report
	if err := json.Unmarshal([]byte(actual), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != planning.StatusStructurallyValid || !report.ReviewRequired || report.ArtifactsWritten {
		t.Fatalf("incorrect metadata: %+v", report)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Fatalf("metadata-only prepare wrote files: %v", err)
	}
	directory := filepath.Join(root, "private-plan")
	var output bytes.Buffer
	if err := planningCommand(context.Background(), []string{"prepare", "--input", input, "--output-dir", directory}, &output); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil || (util.ModeIsProtection() && directoryInfo.Mode().Perm() != 0o700) {
		t.Fatalf("artifact directory is not private: %v", err)
	}
	for _, name := range []string{"plan.json", "TODO.md", "ROADMAP.md", "MILESTONES.md"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o600) {
			t.Fatalf("artifact %s missing or not private: %v", name, err)
		}
	}
	if err := planningCommand(context.Background(), []string{"prepare", "--input", input, "--output-dir", directory}, &output); err == nil {
		t.Fatal("existing output directory was overwritten")
	}
	if _, ok := commandTable()["planning"]; !ok {
		t.Fatal("planning command not registered")
	}
}

func TestPlanningCLIRejectsInvalidArgumentsAndBoundaries(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "draft.json")
	if err := os.WriteFile(input, planningDraftFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"validate", "--input", input}, {"prepare"},
		{"prepare", "--unknown"}, {"prepare", "--input", input, "extra"}} {
		var output bytes.Buffer
		if err := planningCommand(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	fixture := planningDraftFixture(t)
	exact := append(fixture, bytes.Repeat([]byte(" "), planning.MaxJSONBytes-len(fixture))...)
	if err := os.WriteFile(input, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := planningCommand(context.Background(), []string{"prepare", "--input", input}, &output); err != nil {
		t.Fatalf("exact input boundary rejected: %v", err)
	}
	if err := os.WriteFile(input, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := planningCommand(context.Background(), []string{"prepare", "--input", input}, &output); err == nil || output.Len() != 0 {
		t.Fatal("input over planning byte bound accepted")
	}
}

func TestPlanningCLIRejectsCaseFoldedSchemaAliases(t *testing.T) {
	root := t.TempDir()
	fixture := planningDraftFixture(t)
	closing := bytes.LastIndexByte(fixture, '}')
	aliased := append([]byte(nil), fixture[:closing]...)
	aliased = append(aliased, []byte(`,"ID":"shadow-draft"}`)...)
	input := filepath.Join(root, "aliased.json")
	if err := os.WriteFile(input, aliased, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := planningCommand(context.Background(), []string{"prepare", "--input", input}, &output)
	if err == nil || output.Len() != 0 {
		t.Fatalf("case-folded schema alias accepted: %v, %s", err, output.String())
	}
}
