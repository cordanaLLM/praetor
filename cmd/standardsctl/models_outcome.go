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
	recordToolList                  *bool
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
		log:            fs.String("outcome-log", router.DefaultOutcomeLogPath, "models outcome: JSON Lines log the record is appended to"),
		durationMS:     fs.Int64("duration-ms", 0, "models outcome: how long the run took"),
		physicalModel:  fs.String("physical-model", "", "models outcome: physical model that ran (required; never a catalog alias)"),
		harness:        fs.String("harness", "", "models outcome: harness name that executed the task"),
		harnessVersion: fs.String("harness-version", "", "models outcome: version of the harness"),
		promptDigest:   fs.String("prompt-digest", "", "models outcome: SHA-256 digest of prompt template or brief"),
		contextDigest:  fs.String("context-digest", "", "models outcome: SHA-256 digest of compiled context"),
		contextBytes:   fs.Int64("context-bytes", 0, "models outcome: bytes of compiled context"),
		tools:          fs.String("tools", "", "models outcome: comma-separated tools available to the run; recorded as digest and count"),
		recordToolList: fs.Bool("record-tool-list", false, "models outcome: also keep the full --tools list in the record (refused when the record then exceeds 4096 bytes)"),
		rounds:         fs.Int("rounds", 0, "models outcome: prior interaction rounds"),
		retries:        fs.Int("retries", 0, "models outcome: retry attempts before outcome"),
		costEstimate:   fs.Float64("cost-estimate", 0, "models outcome: cost estimate made before dispatch; omitted means no estimate, not zero"),
		actualCost:     fs.Float64("actual-cost", 0, "models outcome: actual cost measured after the run; omitted means not measured, not zero"),
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
	physical, err := checkPhysicalModel(ctx, configPath, flagWasSet(flags.fs, "config"), *flags.target, *flags.physicalModel)
	if err != nil {
		return err
	}
	outcome := buildOutcomeRecord(ctx, task, physical, flags)
	if err := setOutcomeToolSet(&outcome.Identity, flags); err != nil {
		return err
	}
	router.PrepareOutcome(&outcome)
	if err := router.AppendOutcome(ctx, *flags.log, outcome); err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	return printRecordedOutcome(outcome)
}

func buildOutcomeRecord(ctx context.Context, task, physical string, flags modelOutcomeFlags) router.Outcome {
	identity := router.RunIdentity{
		PhysicalModel:  physical,
		Harness:        *flags.harness,
		HarnessVersion: *flags.harnessVersion,
		PromptDigest:   *flags.promptDigest,
		ContextDigest:  *flags.contextDigest,
		ContextBytes:   *flags.contextBytes,
		PriorRounds:    *flags.rounds,
		Retries:        *flags.retries,
	}
	if flagWasSet(flags.fs, "cost-estimate") {
		estimate := *flags.costEstimate
		identity.CostEstimate = &estimate
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
	if flagWasSet(flags.fs, "actual-cost") {
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

// setOutcomeToolSet records the --tools set as its digest and count, and the full list only when
// --record-tool-list asks for it.
func setOutcomeToolSet(identity *router.RunIdentity, flags modelOutcomeFlags) error {
	tools := splitCommaList(*flags.tools)
	digest, count, err := router.DigestToolSet(tools)
	if err != nil {
		return fmt.Errorf("--tools: %w", err)
	}
	identity.ToolSetDigest, identity.ToolCount = digest, count
	if *flags.recordToolList {
		identity.ToolSet = tools
	}
	return nil
}

// checkPhysicalModel returns the --physical-model value after refusing what cannot be the model
// that ran. The flag is always required: the target may be a gateway alias the catalog does not
// declare, and copying it would record the alias as the physical model. A value naming an alias,
// or the catalog ID of an alias entry, of the loaded routing catalog is refused.
func checkPhysicalModel(ctx context.Context, configPath string, configExplicit bool, target, physical string) (string, error) {
	if physical == "" {
		return "", fmt.Errorf("outcome for target %q needs --physical-model naming the model that ran; "+
			"the target may be a gateway alias, and an alias is never recorded as the physical model", target)
	}
	cfg, err := loadAliasConfig(ctx, configPath, configExplicit)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		if _, err := fmt.Fprintf(os.Stderr, "models outcome: routing config %s not found; --physical-model %q not checked against catalog aliases\n", configPath, physical); err != nil {
			return "", fmt.Errorf("write config note: %w", err)
		}
		return physical, nil
	}
	if entry, ok := catalogAliasEntry(cfg, physical); ok {
		return "", fmt.Errorf("--physical-model %q names gateway alias %q (catalog entry %s), not the model that ran", physical, entry.Alias, entry.ID)
	}
	return physical, nil
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

// catalogAliasEntry returns the alias entry of cfg whose alias or catalog ID is name.
func catalogAliasEntry(cfg *router.RoutingConfig, name string) (router.ModelDescriptor, bool) {
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < router.MaxModelsPerTier; i++ {
			m := tier.Models[i]
			if m.Alias != "" && (m.Alias == name || m.ID == name) {
				return m, true
			}
		}
	}
	return router.ModelDescriptor{}, false
}

func printRecordedOutcome(outcome router.Outcome) error {
	data, err := json.MarshalIndent(outcome, "", "  ")
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
		return fmt.Errorf("write outcome: %w", err)
	}
	if outcome.Lane == "" {
		return nil
	}
	line := "not measured (needs --cost-estimate and --actual-cost)"
	if estimate, actual, ok := outcome.MeasuredCosts(); ok {
		line = router.FormatEstimateError(estimate, actual)
	}
	if _, err := fmt.Fprintf(os.Stderr, "estimate-error [%s]: %s\n", outcome.Lane, line); err != nil {
		return fmt.Errorf("write estimate-error: %w", err)
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
