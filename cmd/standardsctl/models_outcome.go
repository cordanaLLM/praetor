package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

// defaultOutcomeLog is the private, git-ignored log the efficiency ledger reads later.
const defaultOutcomeLog = ".workingdir/routing/outcomes.jsonl"

type modelOutcomeFlags struct {
	fs                              *flag.FlagSet
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
	branch                          *string
}

// addModelOutcomeFlags registers the models outcome flags; the task label reuses --task.
func addModelOutcomeFlags(fs *flag.FlagSet) modelOutcomeFlags {
	return modelOutcomeFlags{
		fs:             fs,
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
		branch:         fs.String("branch", "", "models outcome: git branch associated with the run"),
	}
}

// handleModelsOutcome appends one measured result of a routed task to the outcome log and
// echoes the stored record, so a dispatcher records what it ran and reads back what was kept.
func handleModelsOutcome(ctx context.Context, configPath, task string, flags modelOutcomeFlags) error {
	if *flags.reconcile {
		if task != "" || *flags.target != "" || *flags.lane != "" || *flags.result != "" {
			return errors.New("--reconcile cannot be combined with recording an outcome")
		}
		return handleModelsOutcomeReconcile(ctx, *flags.log)
	}
	configExplicit := isFlagPassed(flags.fs, "config")
	physical, err := resolvePhysicalModel(ctx, configPath, configExplicit, *flags.target, *flags.physicalModel)
	if err != nil {
		return err
	}
	tools, err := parseOutcomeTools(*flags.tools)
	if err != nil {
		return err
	}
	outcome := buildOutcomeRecord(ctx, task, physical, flags, tools)
	router.PrepareOutcome(&outcome)
	if err := router.AppendOutcome(ctx, *flags.log, outcome); err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	return printRecordedOutcome(outcome)
}

func buildOutcomeRecord(ctx context.Context, task, physical string, flags modelOutcomeFlags, tools []string) router.Outcome {
	identity := router.RunIdentity{
		PhysicalModel:  physical,
		Harness:        *flags.harness,
		HarnessVersion: *flags.harnessVersion,
		PromptDigest:   *flags.promptDigest,
		ContextDigest:  *flags.contextDigest,
		ContextBytes:   *flags.contextBytes,
		ToolSet:        tools,
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
		Branch:        resolveOutcomeBranch(ctx, flags),
	}
	if isFlagPassed(flags.fs, "actual-cost") {
		cost := *flags.actualCost
		outcome.ActualCost = &cost
	}
	return outcome
}

func resolveOutcomeBranch(ctx context.Context, flags modelOutcomeFlags) string {
	if flags.branch != nil && *flags.branch != "" {
		return *flags.branch
	}
	subCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if out, err := util.RunGit(subCtx, ".", "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

func isFlagPassed(fs *flag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	passed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			passed = true
		}
	})
	return passed
}

func resolvePhysicalModel(ctx context.Context, configPath string, configExplicit bool, target, explicit string) (string, error) {
	if configPath == "" || target == "" {
		if explicit != "" {
			return explicit, nil
		}
		return target, nil
	}
	catalogID, isAlias, err := lookupAliasEntry(ctx, configPath, configExplicit, target)
	if err != nil {
		return "", err
	}
	if isAlias {
		if err := validateAliasExplicit(target, explicit, catalogID); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if explicit != "" {
		return explicit, nil
	}
	return target, nil
}

func validateAliasExplicit(target, explicit, catalogID string) error {
	if explicit == "" {
		return fmt.Errorf("outcome written through alias %q requires explicit --physical-model", target)
	}
	if explicit == target || (catalogID != "" && explicit == catalogID) {
		return fmt.Errorf("outcome written through alias %q must name the resolved physical model, not the alias", target)
	}
	return nil
}

func loadAliasConfig(ctx context.Context, configPath string, configExplicit bool) (*router.RoutingConfig, error) {
	if configPath == "" {
		return nil, nil
	}
	if !configExplicit {
		if _, err := os.Stat(configPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("stat routing config: %w", err)
		}
	}
	cfg, err := router.LoadRoutingConfigContext(ctx, configPath)
	if err != nil {
		return nil, fmt.Errorf("load routing config: %w", err)
	}
	if cfg == nil {
		return nil, errors.New("empty routing config")
	}
	return cfg, nil
}

func findAliasInConfig(cfg *router.RoutingConfig, target string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < router.MaxModelsPerTier; i++ {
			m := tier.Models[i]
			if m.Alias != "" && (m.Alias == target || m.ID == target) {
				return m.ID, true
			}
		}
	}
	return "", false
}

func lookupAliasEntry(ctx context.Context, configPath string, configExplicit bool, target string) (string, bool, error) {
	cfg, err := loadAliasConfig(ctx, configPath, configExplicit)
	if err != nil {
		return "", false, err
	}
	id, ok := findAliasInConfig(cfg, target)
	return id, ok, nil
}

func parseOutcomeTools(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > router.MaxRoutingTags {
		return nil, fmt.Errorf("tool count %d exceeds maximum of %d", len(parts), router.MaxRoutingTags)
	}
	tools := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for i := 0; i < len(parts); i++ {
		trimmed := strings.TrimSpace(parts[i])
		if trimmed == "" {
			continue
		}
		if seen[trimmed] {
			return nil, fmt.Errorf("duplicate tool name %q in tools list", trimmed)
		}
		seen[trimmed] = true
		tools = append(tools, trimmed)
	}
	if len(tools) > router.MaxRoutingTags {
		return nil, fmt.Errorf("tool count %d exceeds maximum of %d", len(tools), router.MaxRoutingTags)
	}
	return tools, nil
}

func printRecordedOutcome(outcome router.Outcome) error {
	data, err := json.MarshalIndent(outcome, "", "  ")
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
		return fmt.Errorf("write outcome: %w", err)
	}
	if outcome.Lane != "" {
		var err error
		if outcome.ActualCost == nil {
			_, err = fmt.Fprintf(os.Stderr, "estimate-error [%s]: not measured\n", outcome.Lane)
		} else {
			_, err = fmt.Fprintf(os.Stderr, "estimate-error [%s]: %s\n", outcome.Lane, router.FormatEstimateError(outcome.Identity.CostEstimate, *outcome.ActualCost))
		}
		if err != nil {
			return fmt.Errorf("write estimate-error: %w", err)
		}
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
