// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// runUI dispatches the ui command and its subcommands (env) for order engine-fr-rule-checks-20261027.
func runUI(args []string) error {
	if len(args) > 0 && isHelpToken(args[0]) {
		printUIUsage()
		return nil
	}
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	manifestPath := fs.String("config", config.ManifestFileName, "Path to .standards.yaml")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	action := positionalAt(positional, 0, "env")
	if isHelpToken(action) {
		printUIUsage()
		return nil
	}
	if len(positional) > 1 {
		return fmt.Errorf("ui accepts at most one action, got %q", positional)
	}

	switch action {
	case "env":
		return runUIEnv(*manifestPath)
	default:
		return fmt.Errorf("unknown ui action: %s (supported: env)", action)
	}
}

func printUIUsage() {
	fmt.Println("Usage: praetorctl ui <subcommand> [options]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  env   Export PRAETOR_UI_INPUT_LATENCY_MS from .standards.yaml for Playwright UI gates")
	fmt.Println("\nOptions:")
	fmt.Println("  --config string   Path to .standards.yaml (default \".standards.yaml\")")
}

// runUIEnv reads ui.input_latency_ms from manifestPath and prints a shell export statement
// setting PRAETOR_UI_INPUT_LATENCY_MS for Playwright responsiveness gates.
func runUIEnv(manifestPath string) error {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest %s: %w", manifestPath, err)
	}
	if manifest == nil || manifest.UI == nil || manifest.UI.InputLatencyMs <= 0 {
		return fmt.Errorf("%s is missing required ui.input_latency_ms", manifestPath)
	}
	if err := manifest.UI.Validate(); err != nil {
		return fmt.Errorf("invalid ui section in %s: %w", manifestPath, err)
	}
	fmt.Printf("export %s=%d\n", config.UIInputLatencyEnv, manifest.UI.InputLatencyMs)
	return nil
}
