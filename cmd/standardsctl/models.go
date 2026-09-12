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
	configPath := fs.String("config", ".config/models/routing.yaml", "Path to model routing config")
	discoverLocal := fs.Bool("discover-local", true, "Auto-discover local Ollama/vLLM models")
	endpoints := fs.String("local-endpoints", "http://localhost:11434,http://localhost:8000", "Comma-separated local runtime endpoints")
	route := addModelRouteFlags(fs)

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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch action {
	case "sync":
		return handleModelsSync(ctx, *configPath, *endpoints, *discoverLocal)
	case "list":
		return handleModelsList(*configPath)
	case "route":
		return handleModelsRoute(ctx, *configPath, route)
	default:
		return fmt.Errorf("unknown action: %s (supported: sync, list, route)", action)
	}
}

func handleModelsSync(ctx context.Context, configPath, endpoints string, discoverLocal bool) error {
	fmt.Println("=== cordanaLLM/praetor Live Model & Benchmark Synchronizer ===")
	var localList []string
	for _, ep := range strings.Split(endpoints, ",") {
		if trimmed := strings.TrimSpace(ep); trimmed != "" {
			localList = append(localList, trimmed)
		}
	}

	opts := router.SyncOptions{
		IncludeOpenWeights: true,
		DiscoverLocal:      discoverLocal,
		LocalEndpoints:     localList,
	}

	res, err := router.SyncCatalog(ctx, configPath, opts)
	if err != nil {
		return fmt.Errorf("model catalog sync failed: %w", err)
	}

	fmt.Printf("Catalog synchronized cleanly to %s:\n", configPath)
	fmt.Printf("  - Total Models:        %d\n", res.TotalModels)
	fmt.Printf("  - Tier 3 Frontier:     %d models (Opus, Pro, O3, Grok 3, DeepSeek-R1)\n", res.HeavyFrontier)
	fmt.Printf("  - Tier 2 Mid-Weight:   %d models (Qwen3.8-27B, Qwen3-30B, Coder-32B, Codestral)\n", res.MidWeight)
	fmt.Printf("  - Tier 1 Lightweight:  %d models (9B Qwythos/Gemma, 7B/14B Qwen, Phi-4)\n", res.LightWeight)
	fmt.Printf("  - Tier 0 Micro/Nano:   %d models (SmolLM2, 1.5B/3B Qwen, Phi-3.5-mini)\n", res.Nano)
	if res.LocalModels > 0 {
		fmt.Printf("  - Local Discovered:    %d models\n", res.LocalModels)
	}
	return nil
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
			fmt.Printf("  - %-32s [%-12s] RPM: %-5d TPM: %-8d ($%.2f/M in, $%.2f/M out)\n",
				m.ID, m.Family, m.RPMLimit, m.TPMLimit, m.CostPerMIn, m.CostPerMOut)
		}
	}
	return nil
}
