package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func runDogfoodRepairs(ctx context.Context, args []string) error {
	if len(args) > 0 && (args[0] == "run" || args[0] == "status") {
		return runDogfoodRepairAction(ctx, args)
	}
	return runDogfoodRepairPlan(ctx, args)
}

func runDogfoodRepairPlan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dogfood repairs", flag.ContinueOnError)
	reportPath := fs.String("report", "", "Completed suite report JSON")
	routing := fs.String("routing-config", "", "Explicit declared routing configuration")
	task := fs.String("task", "ci_debugging", "Exact configured target_tasks label")
	input := fs.Int64("input-tokens", 0, "Estimated input tokens per review job")
	output := fs.Int64("output-tokens", 0, "Estimated output tokens per review job")
	ceiling := fs.Float64("max-cost", -1, "Required total configured-cost ceiling, including explicit zero")
	usage := fs.String("usage", "", "Optional explicit capacity snapshot")
	directory := fs.String("output", "", "New private local review directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *reportPath == "" || *routing == "" || *directory == "" {
		return errors.New("repairs requires --report, --routing-config, --output and flags only")
	}
	report, err := dogfood.LoadRepairReport(ctx, *reportPath)
	if err != nil {
		return err
	}
	policy := dogfood.RepairPolicy{RoutingConfig: *routing, UsagePath: *usage, Task: *task, InputTokens: *input, OutputTokens: *output, MaxCost: *ceiling}
	plan, planErr := dogfood.PlanRepairs(ctx, report, policy)
	if plan == nil {
		return planErr
	}
	if err := dogfood.SaveRepairPlan(ctx, *directory, plan); err != nil {
		return errors.Join(planErr, err)
	}
	summary := struct {
		Status        string  `json:"status"`
		Jobs          int     `json:"jobs"`
		EstimatedCost float64 `json:"estimated_cost"`
		ReportSHA256  string  `json:"report_sha256"`
	}{plan.Status, len(plan.Jobs), plan.EstimatedCost, plan.ReportSHA256}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return errors.Join(planErr, fmt.Errorf("write repair summary: %w", err))
	}
	return planErr
}
