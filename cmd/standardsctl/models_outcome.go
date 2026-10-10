package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// defaultOutcomeLog is the private, git-ignored log the efficiency ledger reads later.
const defaultOutcomeLog = ".workingdir/routing/outcomes.jsonl"

type modelOutcomeFlags struct {
	lane, target, result, note, log *string
	durationMS                      *int64
	physicalModel                   *string
	harness                         *string
	harnessVersion                  *string
	promptDigest                    *string
	contextDigest                   *string
	contextBytes                    *int64
	tools                           *string
	rounds                          *int
	retries                         *int
	costEstimate                    *float64
	actualCost                      *float64
	reconcile                       *bool
}

// addModelOutcomeFlags registers the models outcome flags; the task label reuses --task.
func addModelOutcomeFlags(fs *flag.FlagSet) modelOutcomeFlags {
	return modelOutcomeFlags{
		lane:           fs.String("lane", "", "models outcome: lane name the route returned"),
		target:         fs.String("target", "", "models outcome: alias or model ID the harness ran"),
		result:         fs.String("result", "", "models outcome: ok, fail or timeout"),
		note:           fs.String("note", "", "models outcome: short note, at most 256 bytes"),
		log:            fs.String("outcome-log", defaultOutcomeLog, "models outcome: JSON Lines log the record is appended to"),
		durationMS:     fs.Int64("duration-ms", 0, "models outcome: how long the run took"),
		physicalModel:  fs.String("physical-model", "", "models outcome: resolved physical model behind the alias"),
		harness:        fs.String("harness", "", "models outcome: harness name that executed the task"),
		harnessVersion: fs.String("harness-version", "", "models outcome: version of the harness"),
		promptDigest:   fs.String("prompt-digest", "", "models outcome: SHA-256 digest of prompt template or brief"),
		contextDigest:  fs.String("context-digest", "", "models outcome: SHA-256 digest of compiled context"),
		contextBytes:   fs.Int64("context-bytes", 0, "models outcome: bytes of compiled context"),
		tools:          fs.String("tools", "", "models outcome: comma-separated list of available tools"),
		rounds:         fs.Int("rounds", 0, "models outcome: prior interaction rounds"),
		retries:        fs.Int("retries", 0, "models outcome: retry attempts before outcome"),
		costEstimate:   fs.Float64("cost-estimate", 0, "models outcome: cost estimate before dispatch"),
		actualCost:     fs.Float64("actual-cost", 0, "models outcome: actual cost measured after run"),
		reconcile:      fs.Bool("reconcile", false, "models outcome: reconcile and print estimate-error metric per lane"),
	}
}

// handleModelsOutcome appends one measured result of a routed task to the outcome log and
// echoes the stored record, so a dispatcher records what it ran and reads back what was kept.
func handleModelsOutcome(ctx context.Context, configPath, task string, flags modelOutcomeFlags) error {
	if *flags.reconcile && task == "" && *flags.target == "" {
		return handleModelsOutcomeReconcile(ctx, *flags.log)
	}
	physical, err := resolvePhysicalModel(ctx, configPath, *flags.target, *flags.physicalModel)
	if err != nil {
		return err
	}
	outcome := buildOutcomeRecord(task, physical, flags)
	if err := router.AppendOutcome(ctx, *flags.log, outcome); err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	return printRecordedOutcome(outcome, *flags.reconcile)
}

func buildOutcomeRecord(task, physical string, flags modelOutcomeFlags) router.Outcome {
	identity := router.RunIdentity{
		PhysicalModel:  physical,
		Harness:        *flags.harness,
		HarnessVersion: *flags.harnessVersion,
		PromptDigest:   *flags.promptDigest,
		ContextDigest:  *flags.contextDigest,
		ContextBytes:   *flags.contextBytes,
		ToolSet:        parseOutcomeTools(*flags.tools),
		PriorRounds:    *flags.rounds,
		Retries:        *flags.retries,
		CostEstimate:   *flags.costEstimate,
	}
	outcome := router.Outcome{
		Time:          time.Now().UTC(),
		Task:          task,
		Lane:          *flags.lane,
		Target:        *flags.target,
		ResolvedModel: physical,
		Result:        *flags.result,
		DurationMS:    *flags.durationMS,
		Note:          *flags.note,
		Identity:      identity,
		ActualCost:    *flags.actualCost,
	}
	if *flags.actualCost > 0 || *flags.costEstimate > 0 {
		diff := *flags.actualCost - *flags.costEstimate
		outcome.EstimateError = &diff
	}
	return outcome
}

func resolvePhysicalModel(ctx context.Context, configPath, target, explicit string) (string, error) {
	if explicit != "" {
		if configPath != "" && isAliasTarget(ctx, configPath, target) && explicit == target {
			return "", fmt.Errorf("outcome written through alias %q must name the resolved physical model, not the alias", target)
		}
		return explicit, nil
	}
	if configPath != "" && target != "" {
		if phys, ok := findAliasPhysicalModel(ctx, configPath, target); ok {
			return phys, nil
		}
	}
	return target, nil
}

func isAliasTarget(ctx context.Context, configPath, target string) bool {
	cfg, err := router.LoadRoutingConfigContext(ctx, configPath)
	if err != nil || cfg == nil {
		return false
	}
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < router.MaxModelsPerTier; i++ {
			if tier.Models[i].Alias == target {
				return true
			}
		}
	}
	return false
}

func findAliasPhysicalModel(ctx context.Context, configPath, target string) (string, bool) {
	cfg, err := router.LoadRoutingConfigContext(ctx, configPath)
	if err != nil || cfg == nil {
		return "", false
	}
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < router.MaxModelsPerTier; i++ {
			if tier.Models[i].Alias == target {
				return tier.Models[i].ID, true
			}
		}
	}
	return "", false
}

func parseOutcomeTools(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tools := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && i < router.MaxRoutingTags; i++ {
		trimmed := strings.TrimSpace(parts[i])
		if trimmed != "" {
			tools = append(tools, trimmed)
		}
	}
	return tools
}

func printRecordedOutcome(outcome router.Outcome, reconcile bool) error {
	data, err := json.MarshalIndent(outcome, "", "  ")
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
		return fmt.Errorf("write outcome: %w", err)
	}
	if outcome.Lane != "" {
		fmt.Fprintf(os.Stdout, "estimate-error [%s]: %s\n", outcome.Lane, router.FormatEstimateError(outcome.Identity.CostEstimate, outcome.ActualCost))
	}
	return nil
}

func handleModelsOutcomeReconcile(ctx context.Context, logPath string) error {
	outcomes, err := router.ReadOutcomes(ctx, logPath)
	if err != nil {
		return fmt.Errorf("read outcomes: %w", err)
	}
	recs := router.ReconcileLanes(outcomes)
	if _, err := fmt.Fprint(os.Stdout, router.RenderLaneReconciliation(recs)); err != nil {
		return fmt.Errorf("write reconciliation: %w", err)
	}
	return nil
}
