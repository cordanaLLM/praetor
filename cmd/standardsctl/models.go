package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	configPath := fs.String("config", router.DefaultConfigPath, "Path to model routing config")
	discoverLocal := fs.Bool("discover-local", true, "Auto-discover local Ollama/vLLM models")
	endpoints := fs.String("local-endpoints", "http://localhost:11434,http://localhost:8000", "Comma-separated local runtime endpoints")
	prune := fs.Bool("prune", false, "models sync: rebuild the catalog from the seed list and this run's local discovery, removing every other entry")
	probeAliases := fs.Bool("probe-aliases", true, "models sync: probe each gateway alias entry once and record whether it answers")
	route := addModelRouteFlags(fs)
	outcome := addModelOutcomeFlags(fs)

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	action := positionalAt(positional, 0, "list")
	if len(positional) > 1 {
		return fmt.Errorf("models accepts one action")
	}
	if err := validateModelRouteFlags(fs, action); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
	defer cancel()

	switch action {
	case "sync":
		opts := modelSyncOptions(*endpoints, *discoverLocal, *prune)
		opts.ProbeAliases = *probeAliases
		return handleModelsSync(ctx, *configPath, opts)
	case "list":
		return handleModelsList(*configPath)
	case "route":
		return handleModelsRoute(ctx, *configPath, route)
	case "outcome":
		return handleModelsOutcome(ctx, *route.task, outcome)
	default:
		return fmt.Errorf("unknown action: %s (supported: sync, list, route, outcome)", action)
	}
}

// modelsTimeout bounds one models invocation, including every alias probe a sync makes.
const modelsTimeout = 60 * time.Second

func modelSyncOptions(endpoints string, discoverLocal, prune bool) router.SyncOptions {
	var localList []string
	for _, ep := range strings.Split(endpoints, ",") {
		if trimmed := strings.TrimSpace(ep); trimmed != "" {
			localList = append(localList, trimmed)
		}
	}
	return router.SyncOptions{
		IncludeOpenWeights: true,
		DiscoverLocal:      discoverLocal,
		LocalEndpoints:     localList,
		Prune:              prune,
	}
}

func handleModelsSync(ctx context.Context, configPath string, opts router.SyncOptions) error {
	fmt.Println("=== Praetor legacy catalog seed and optional local inventory ===")
	res, err := router.SyncCatalog(ctx, configPath, opts)
	if errors.Is(err, router.ErrSyncWouldRemove) {
		return fmt.Errorf("model catalog sync failed, nothing written: %w; pass --prune to remove them", err)
	}
	if err != nil {
		return fmt.Errorf("model catalog sync failed: %w", err)
	}

	fmt.Printf("Seed merged into %s (prices and quotas are not refreshed upstream):\n", configPath)
	fmt.Printf("  - Total Models:        %d\n", res.TotalModels)
	fmt.Printf("  - Tier 3 Frontier:     %d models (Opus, Pro, O3, Grok 3, DeepSeek-R1)\n", res.HeavyFrontier)
	fmt.Printf("  - Tier 2 Mid-Weight:   %d models (Qwen3.8-27B, Qwen3-30B, Coder-32B, Codestral)\n", res.MidWeight)
	fmt.Printf("  - Tier 1 Lightweight:  %d models (9B Qwythos/Gemma, 7B/14B Qwen, Phi-4)\n", res.LightWeight)
	fmt.Printf("  - Tier 0 Micro/Nano:   %d models (SmolLM2, 1.5B/3B Qwen, Phi-3.5-mini)\n", res.Nano)
	if res.LocalModels > 0 {
		fmt.Printf("  - Local Discovered:    %d models\n", res.LocalModels)
	}
	fmt.Printf("  - Kept, not seed-owned: %d models\n", res.Preserved)
	for _, id := range res.Removed {
		fmt.Printf("  - Pruned:              %s\n", id)
	}
	for _, failure := range res.DiscoveryFailures {
		fmt.Printf("  - Endpoint skipped:    %s\n", failure)
	}
	printAliasProbes(res.AliasProbes)
	printCatalogFindings(res.Findings)
	return nil
}

// printAliasProbes states, per alias entry, whether the gateway answered and why not.
func printAliasProbes(probes []router.AliasProbe) {
	for _, probe := range probes {
		switch probe.Status {
		case router.AliasAnswers:
			fmt.Printf("  - Alias answers:       %s (%s)\n", probe.Model, probe.Alias)
		case router.AliasUnanswered:
			fmt.Printf("  - Alias skipped:       %s (%s): %s\n", probe.Model, probe.Alias, probe.Reason)
		default:
			fmt.Printf("  - Alias not probed:    %s (%s): %s\n", probe.Model, probe.Alias, probe.Reason)
		}
	}
}

// printCatalogFindings lists every stale or preview entry; the audit fails on the same findings.
func printCatalogFindings(findings []router.CatalogFinding) {
	for _, finding := range findings {
		fmt.Printf("  - Catalog stale:       %s\n", finding)
	}
}

// modelListNotes marks the alias, probe status and preview flag of a listed entry.
func modelListNotes(m router.ModelDescriptor) string {
	var notes []string
	if m.Alias != "" {
		status := string(m.AliasStatus)
		if status == "" {
			status = "not probed"
		}
		notes = append(notes, "alias "+m.Alias+": "+status)
	}
	if router.IsPreviewModel(m) {
		notes = append(notes, "preview")
	}
	if len(notes) == 0 {
		return ""
	}
	return " [" + strings.Join(notes, ", ") + "]"
}

func handleModelsList(configPath string) error {
	cfg, err := router.LoadRoutingConfig(configPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s does not exist; run 'praetorctl models sync' first: %w", configPath, err)
		}
		return err
	}

	fmt.Printf("=== Active Cognitive Model Tiers (%s) ===\n", configPath)
	for tierName, tier := range cfg.Tiers {
		fmt.Printf("\n[TIER: %s] (%s)\n", strings.ToUpper(tierName), tier.Description)
		for _, m := range tier.Models {
			fmt.Printf("  - %-32s [%-12s] RPM: %-5d TPM: %-8d ($%.2f/M in, $%.2f/M out)%s\n",
				m.ID, m.Family, m.RPMLimit, m.TPMLimit, m.CostPerMIn, m.CostPerMOut, modelListNotes(m))
		}
	}
	return nil
}
