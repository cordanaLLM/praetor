package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func repairCLIFixture(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	transcript := filepath.Join(root, "empty.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(nil)
	config := dogfood.SuiteConfig{Version: 1, PublicRepositories: []string{}, Transcripts: []dogfood.SuiteTranscript{{ID: "bad", SourcePath: transcript, SHA256: hex.EncodeToString(sum[:]), Format: "claude-code-jsonl-v1"}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "suite.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := dogfood.RunSuite(context.Background(), dogfood.SuiteOptions{ConfigPath: configPath, Stage: "verify", ArtifactDir: filepath.Join(root, "run")})
	if err == nil || report == nil {
		t.Fatal("failed suite fixture did not fail")
	}
	routing := filepath.Join(root, "routing.yaml")
	body := "version: 1\ntiers:\n  debug:\n    target_tasks: [ci_debugging, architecture_synthesis, function_docstrings, commit_message_synthesis, waiver_signoff]\n    models:\n      - id: cheap\n        family: openai\n        cost_per_m_in: 1\n        cost_per_m_out: 1\ngovernance:\n  exhaustion_threshold_percent: 80\n"
	if err := os.WriteFile(routing, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"--report", filepath.Join(root, "run", "report.json"), "--routing-config", routing, "--input-tokens", "1000", "--max-cost", "0.1", "--output", filepath.Join(root, "review")}
}

func TestDogfoodRepairCLIActualFailedSuite(t *testing.T) {
	args := repairCLIFixture(t)
	if err := runDogfoodRepairs(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(args[len(args)-1], "plan.json"))
	if err != nil || !strings.Contains(string(data), `"review_required"`) {
		t.Fatalf("readback %v", err)
	}
	var plan dogfood.RepairPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Policy.Register != "internal" || plan.Policy.RegisterSource != "surfaces.agent" ||
		plan.Policy.PromptRegister != "internal" || plan.Policy.PromptRegisterSource != "surfaces.prompts" ||
		len(plan.Policy.RegisterManifestSHA256) != 64 {
		t.Fatalf("repair plan lost exact register resolutions: %+v", plan.Policy)
	}
	if len(plan.Jobs) != 1 || plan.Jobs[0].RegisterSource != plan.Policy.RegisterSource ||
		plan.Jobs[0].PromptRegister != plan.Policy.PromptRegister ||
		plan.Jobs[0].PromptRegisterSource != plan.Policy.PromptRegisterSource ||
		plan.Jobs[0].RegisterManifestSHA256 != plan.Policy.RegisterManifestSHA256 {
		t.Fatalf("repair job lost register resolution provenance: %+v", plan.Jobs)
	}
	_, explicit, err := loadRepairInputs(context.Background(), args[1], dogfood.RepairPolicy{Task: "architecture_synthesis"})
	if err != nil || explicit.Register != "docs" || explicit.RegisterSource != "tasks.architecture_synthesis" ||
		explicit.PromptRegister != "internal" || explicit.PromptRegisterSource != "surfaces.prompts" ||
		explicit.RegisterManifestSHA256 != plan.Policy.RegisterManifestSHA256 {
		t.Fatalf("task and prompt surfaces were not resolved independently: policy=%+v err=%v", explicit, err)
	}
	if err := runDogfoodRepairs(context.Background(), args); err == nil {
		t.Fatal("existing output overwritten")
	}
}

func TestDogfoodRepairCLIBlockedIsNotSuccess(t *testing.T) {
	args := repairCLIFixture(t)
	args[7] = "0"
	if err := runDogfoodRepairs(context.Background(), args); !errors.Is(err, dogfood.ErrRepairsBlocked) {
		t.Fatalf("blocked: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(args[len(args)-1], "plan.json"))
	if err != nil || !strings.Contains(string(data), "blocked_budget") {
		t.Fatalf("blocked plan lost: %v", err)
	}
}

func TestDogfoodRepairCLIRejectsIncompleteFlags(t *testing.T) {
	for _, args := range [][]string{nil, {"--unknown"}, {"--report", "anything", "positional"}} {
		if err := runDogfoodRepairs(context.Background(), args); err == nil {
			t.Fatal("invalid flags accepted")
		}
	}
	args := repairCLIFixture(t)
	args = append(args[:6], args[8:]...)
	if err := runDogfoodRepairs(context.Background(), args); err == nil {
		t.Fatal("missing ceiling accepted")
	}
	if _, err := os.Stat(args[len(args)-1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid flags wrote output")
	}
}
