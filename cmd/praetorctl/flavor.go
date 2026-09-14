package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

func runFlavor(args []string) error {
	if len(args) == 0 {
		printFlavorUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list":
		return runFlavorList()
	case "inspect":
		return runFlavorInspect(subArgs)
	case "audit":
		return runFlavorAudit(subArgs)
	case "apply":
		return runFlavorApply(subArgs)
	case "-h", "--help", "help":
		printFlavorUsage()
		return nil
	default:
		return fmt.Errorf("unknown flavor subcommand: %s", sub)
	}
}

func printFlavorUsage() {
	fmt.Println("Usage: praetorctl flavor <subcommand> [args]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  list                           List all supported repository engineering flavors")
	fmt.Println("  inspect <flavor>               Inspect templates, settings, and toolchains for a flavor")
	fmt.Println("  audit [dir] [--flavor=name]    Audit a repository against its flavor requirements")
	fmt.Println("  apply [dir] [--flavor=name]    Scaffold required templates and settings for a flavor")
}

func runFlavorList() error {
	flavors := flavor.List()
	fmt.Println("=== Praetor Engineering Flavors ===")
	for _, f := range flavors {
		fmt.Printf("  %-22s : %-45s [HISS: %s]\n", f.Name(), f.Description(), f.HISSProfile())
	}
	return nil
}

func runFlavorInspect(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: praetorctl flavor inspect <flavor-name>")
	}
	flv, err := flavor.Get(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("=== Flavor: %s ===\n", flv.Name())
	fmt.Printf("Description:  %s\n", flv.Description())
	fmt.Printf("HISS Profile: %s\n\n", flv.HISSProfile())

	fmt.Println("Required Templates:")
	for _, t := range flv.RequiredTemplates() {
		fmt.Printf("  - %-30s (%s)\n", t.Path, t.Description)
	}

	fmt.Println("\nRequired Settings:")
	for _, s := range flv.RequiredSettings() {
		fmt.Printf("  - %-25s : %s\n", s.Name, s.Path)
	}

	fmt.Println("\nRequired Toolchains:")
	for _, tc := range flv.RequiredToolchains() {
		fmt.Printf("  - %-15s : %s\n", tc.Binary, tc.Purpose)
	}
	return nil
}

func runFlavorAudit(args []string) error {
	fs := flag.NewFlagSet("flavor audit", flag.ContinueOnError)
	targetFlv := fs.String("flavor", "auto", "Target flavor (default: auto-detect)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	dir := positionalAt(positional, 0, ".")

	report, err := flavor.AuditFlavor(dir, *targetFlv)
	if err != nil {
		return fmt.Errorf("flavor audit failed: %w", err)
	}

	fmt.Printf("=== Flavor Audit: %s (Flavor: %s) ===\n", dir, report.Flavor)
	fmt.Printf("  Score:       %.1f%%\n", report.Score)
	fmt.Printf("  Passed:      %v\n", report.Passed)
	fmt.Printf("  Templates:   %d/%d present\n", report.TemplatesPresent, report.TemplatesTotal)
	fmt.Printf("  Settings:    %d/%d valid\n", report.SettingsValid, report.SettingsTotal)
	fmt.Printf("  Toolchains:  %d/%d available\n", report.ToolchainsAvailable, report.ToolchainsTotal)

	if len(report.MissingTemplates) > 0 {
		fmt.Println("\nMissing Templates:")
		for _, t := range report.MissingTemplates {
			fmt.Printf("  - %s (%s)\n", t.Path, t.Description)
		}
	}
	if len(report.MissingToolchains) > 0 {
		fmt.Println("\nMissing Toolchains:")
		for _, tc := range report.MissingToolchains {
			fmt.Printf("  - %s : %s (Install: %s)\n", tc.Binary, tc.Purpose, tc.InstallGuide)
		}
	}

	if !report.Passed {
		return fmt.Errorf("flavor audit failed (score: %.1f%%)", report.Score)
	}
	return nil
}

func runFlavorApply(args []string) error {
	fs := flag.NewFlagSet("flavor apply", flag.ContinueOnError)
	targetFlv := fs.String("flavor", "auto", "Target flavor (default: auto-detect)")
	force := fs.Bool("force", false, "Force overwrite existing templates")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	dir := positionalAt(positional, 0, ".")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := flavor.ApplyFlavor(ctx, dir, *targetFlv, *force)
	if err != nil {
		return fmt.Errorf("flavor apply failed: %w", err)
	}

	fmt.Printf("=== Applied Flavor: %s to %s ===\n", report.Flavor, dir)
	fmt.Printf("  Created Templates (%d): %s\n", len(report.CreatedTemplates), strings.Join(report.CreatedTemplates, ", "))
	if len(report.SkippedTemplates) > 0 {
		fmt.Printf("  Skipped Existing  (%d): %s\n", len(report.SkippedTemplates), strings.Join(report.SkippedTemplates, ", "))
	}
	fmt.Printf("  WorkingDir State:     %v\n", report.WorkingDirCreated)

	if len(report.Errors) > 0 {
		fmt.Printf("  Errors (%d):\n", len(report.Errors))
		for _, e := range report.Errors {
			fmt.Printf("    - %s\n", e)
		}
		return fmt.Errorf("flavor apply completed with %d error(s): %s",
			len(report.Errors), strings.Join(report.Errors, "; "))
	}
	return nil
}
