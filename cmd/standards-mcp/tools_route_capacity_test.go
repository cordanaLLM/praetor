package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cordanaLLM/praetor/internal/repairrun"
)

func TestRepairStatusMCPUsesProjectedRequestCapacity(t *testing.T) {
	if err := repairrun.ExecutionSupported(); err != nil {
		t.Skipf("repair execution unavailable on this platform: %v", err)
	}
	srv, root := newFixtureServer(t)
	config := writeRepairExecutionMCPFixture(t, srv, root)
	routing := "version: 1\ntiers:\n  debug:\n    target_tasks: [ci_debugging]\n    models:\n      - {id: cheap, family: openai, rpm_limit: 10, tpm_limit: 1000, cost_per_m_in: 1, cost_per_m_out: 1}\ngovernance:\n  exhaustion_threshold_percent: 80\n"
	if err := os.WriteFile(config.RepairPolicy.RoutingConfig, []byte(routing), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		output int64
		status string
	}{{200, "ready"}, {201, "blocked"}} {
		config.RepairPolicy.InputTokens, config.RepairPolicy.OutputTokens = 600, test.output
		saveRepairMCPConfig(t, root, config)
		result := callTool(t, srv, "standards_dogfood_repair_status", repairStatusMCPArgs())
		if result.IsError {
			t.Fatalf("status tool failed: %+v", result)
		}
		var report repairrun.Report
		if err := json.Unmarshal([]byte(result.Content[0].Text), &report); err != nil {
			t.Fatal(err)
		}
		if report.Status != test.status || report.Consumed || report.CandidateVerified {
			t.Fatalf("projected %d+%d tokens: %+v", config.RepairPolicy.InputTokens, test.output, report)
		}
	}
}
