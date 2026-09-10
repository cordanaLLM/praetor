package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/router"
)

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	configPath := fs.String("config", ".config/models/routing.yaml", "Path to model routing config")
	discoverLocal := fs.Bool("discover-local", true, "Auto-discover local Ollama/vLLM models")
	endpoints := fs.String("local-endpoints", "http://localhost:11434,http://localhost:8000", "Comma-separated local runtime endpoints")

	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "list"
	if len(fs.Args()) > 0 {
		action = fs.Args()[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch action {
	case "sync":
		fmt.Println("=== cordanaLLM/standards Live Model & Benchmark Synchronizer ===")
		var localList []string
		for _, ep := range strings.Split(*endpoints, ",") {
			if trimmed := strings.TrimSpace(ep); trimmed != "" {
				localList = append(localList, trimmed)
			}
		}

		opts := router.SyncOptions{
			IncludeOpenWeights: true,
			DiscoverLocal:      *discoverLocal,
			LocalEndpoints:     localList,
		}

		res, err := router.SyncCatalog(ctx, *configPath, opts)
		if err != nil {
			return fmt.Errorf("model catalog sync failed: %w", err)
		}

		fmt.Printf("Catalog synchronized cleanly to %s:\n", *configPath)
		fmt.Printf("  - Total Models:    %d\n", res.TotalModels)
		fmt.Printf("  - Tier 1 Frontier: %d models (Opus, Pro, O3, Grok 3, DeepSeek-R1)\n", res.FrontierTier)
		fmt.Printf("  - Tier 2 Workhorse:%d models (Sonnet, GPT-4o, Flash, Mistral)\n", res.Workhorse)
		fmt.Printf("  - Tier 3 Open OSS: %d models (Qwen, Llama 3.3, gpt-oss)\n", res.OSSFast)
		if res.LocalModels > 0 {
			fmt.Printf("  - Local Discovered:%d models\n", res.LocalModels)
		}
		return nil

	case "list":
		cfg, err := router.LoadRoutingConfig(*configPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s does not exist; run 'standardsctl models sync' first", *configPath)
			}
			return err
		}

		fmt.Printf("=== Active Cognitive Model Tiers (%s) ===\n", *configPath)
		for tierName, tier := range cfg.Tiers {
			fmt.Printf("\n[TIER: %s] (%s)\n", strings.ToUpper(tierName), tier.Description)
			for _, m := range tier.Models {
				fmt.Printf("  - %-32s [%-12s] RPM: %-5d TPM: %-8d ($%.2f/M in, $%.2f/M out)\n",
					m.ID, m.Family, m.RPMLimit, m.TPMLimit, m.CostPerMIn, m.CostPerMOut)
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown action: %s (supported: sync, list)", action)
	}
}
