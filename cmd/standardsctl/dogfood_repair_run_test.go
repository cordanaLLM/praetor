package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/repairrun"
)

func repairExecutionCLIFixture(t *testing.T) (string, string, string) {
	t.Helper()
	plannerArgs := repairCLIFixture(t)
	reportPath, routing := plannerArgs[1], plannerArgs[3]
	root := filepath.Dir(routing)
	cfg := repairrun.Config{Version: 1, SourceRoot: root, SourceSHA: strings.Repeat("a", 40), StateDir: filepath.Join(root, "execution-state"),
		AllowedFiles: []string{"internal/util/fixture.go"}, TestPackages: []string{"./internal/util"}, TimeoutSeconds: 30, MaxPatchBytes: 1024,
		RepairPolicy: dogfood.RepairPolicy{RoutingConfig: routing, Task: "ci_debugging", InputTokens: 1000, OutputTokens: 500, MaxCost: 0.1},
		Provider:     repairrun.ProviderConfig{BaseURL: "https://litellm.ai.cauda.dev/v1", TokenCommand: filepath.Join(root, "nonexistent-helper"), TokenCommandSHA256: strings.Repeat("b", 64), Model: "cheap", MaxInputBytes: 65536, MaxOutputTokens: 256}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "execution.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, reportPath, cfg.StateDir
}

func TestDogfoodRepairExecutionCLIStatusNoSideEffects(t *testing.T) {
	config, reportPath, state := repairExecutionCLIFixture(t)
	output, err := captureStdout(t, func() error {
		return runDogfood([]string{"repairs", "status", "--config", config, "--report", reportPath})
	})
	if err != nil {
		t.Fatal(err)
	}
	var result repairrun.Report
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || result.Consumed || result.CandidateVerified {
		t.Fatalf("wrong status: %s", output)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only status created execution state")
	}
}

func TestDogfoodRepairExecutionCLIInvalidActions(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{nil, {"unknown"}, {"run"}, {"status", "--config", "missing"}, {"run", "--config", "missing", "--report", "missing", "extra"}, {"status", "--run"}} {
		if err := runDogfoodRepairAction(ctx, args); err == nil {
			t.Fatalf("accepted invalid execution args: %v", args)
		}
	}
	config, reportPath, state := repairExecutionCLIFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runDogfoodRepairAction(ctx, []string{"run", "--config", config, "--report", reportPath}); err == nil {
		t.Fatal("canceled run accepted")
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled run created state")
	}
}

func TestDogfoodRepairExecutionCLIRejectsCorruptReport(t *testing.T) {
	config, reportPath, state := repairExecutionCLIFixture(t)
	if err := os.WriteFile(reportPath, []byte(`{"private":"sentinel-source-content"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"run", "status"} {
		output, err := captureStdout(t, func() error {
			return runDogfood([]string{"repairs", action, "--config", config, "--report", reportPath})
		})
		if err == nil || strings.Contains(output, "sentinel-source-content") || strings.Contains(err.Error(), "sentinel-source-content") {
			t.Fatalf("corrupt report disclosed or accepted: %s %v", output, err)
		}
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("corrupt report created state")
	}
}

func TestDogfoodRepairExecutionCLIResolvesExplicitRelativePaths(t *testing.T) {
	config, reportPath, state := repairExecutionCLIFixture(t)
	t.Chdir(filepath.Dir(config))
	output, err := captureStdout(t, func() error {
		return runDogfood([]string{"repairs", "status", "--config", filepath.Base(config), "--report", filepath.Join("run", "report.json")})
	})
	if err != nil {
		t.Fatal(err)
	}
	var report repairrun.Report
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ready" {
		t.Fatal("relative input failed admission")
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("relative read-only status created state")
	}
}

func TestDogfoodRepairExecutionCLIRetainsFailedAttemptAndNonzeroError(t *testing.T) {
	config, reportPath, _ := repairExecutionCLIFixture(t)
	output, runErr := captureStdout(t, func() error {
		return runDogfood([]string{"repairs", "run", "--config", config, "--report", reportPath})
	})
	if runErr == nil {
		t.Fatal("missing pinned source unexpectedly executed successfully")
	}
	var report repairrun.Report
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("partial report missing: %s %v", output, err)
	}
	if report.Status != "failed" || !report.Consumed || report.CandidateVerified {
		t.Fatalf("failure was misreported: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(report.AttemptDir, "result.json")); err != nil {
		t.Fatal(err)
	}
	status, err := captureStdout(t, func() error {
		return runDogfood([]string{"repairs", "status", "--config", config, "--report", reportPath})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(status), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "consumed" || !report.Consumed {
		t.Fatalf("failed attempt was re-admitted: %+v", report)
	}
}
