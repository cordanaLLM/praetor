package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/util"
)

// runNeedsContract dispatches `needs contract <action>`; export is the only action.
func runNeedsContract(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return errors.New("usage: praetorctl needs contract export --language=<lang> --out=<file> [--framework=<dir>] " +
			"[--fleet-config=<path>] [--workstation-config=<path>] [--manifest=<path>]")
	}
	return runNeedsContractExport(ctx, args[1:])
}

// contractExportFlags carries the parsed `needs contract export` command line.
type contractExportFlags struct {
	language, framework, out string
	frameworkSet             bool
	settings                 *operatorSettingsFlags
}

// runNeedsContractExport snapshots the framework a language's target resolves to as a
// version-1 capability contract (ADR-0014 §2). The file can be configured as
// framework.targets.<language>.contract, and serves CI runs that have no checkout.
func runNeedsContractExport(ctx context.Context, args []string) error {
	f, err := parseContractExportFlags(args)
	if err != nil {
		return err
	}
	selection, err := loadNeedsSelection(ctx, f.settings)
	if err != nil {
		return fmt.Errorf("needs contract export: %w", err)
	}
	export, err := needs.ExportFrameworkContract(ctx, f.language, selection.frameworkSource(f.framework, f.frameworkSet), selection.targets)
	if err != nil {
		return fmt.Errorf("needs contract export: %w", err)
	}
	if err := util.WriteFileConfined(filepath.Dir(f.out), filepath.Base(f.out), export.Data, util.SecureFilePerm); err != nil {
		return fmt.Errorf("needs contract export: write %s: %w", f.out, err)
	}
	for _, skipped := range export.Skipped {
		fmt.Printf("[SKIP] %s\n", skipped)
	}
	fmt.Printf("[PASS] Exported the %s framework %s (%d packages) to %s\n", f.language, export.Framework, export.Packages, f.out)
	return nil
}

// parseContractExportFlags parses and checks the export command line: a known language and
// an output file are required, and --framework selects a checkout of the go framework only.
func parseContractExportFlags(args []string) (contractExportFlags, error) {
	fs := flag.NewFlagSet("needs contract export", flag.ContinueOnError)
	var f contractExportFlags
	fs.StringVar(&f.language, "language", "", "Framework language to export: go, typescript, python, rust or native")
	fs.StringVar(&f.framework, "framework", "", "Go framework checkout "+frameworkUsageDefault)
	fs.StringVar(&f.out, "out", "", "Contract file to write")
	f.settings = registerOperatorSettingsFlags(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return contractExportFlags{}, err
	}
	if len(positional) != 0 {
		return contractExportFlags{}, fmt.Errorf("needs contract export accepts no positional arguments (got %q)", positional)
	}
	if _, err := config.ParseFrameworkLanguage(f.language); err != nil {
		return contractExportFlags{}, fmt.Errorf("needs contract export --language: %w", err)
	}
	if f.out == "" {
		return contractExportFlags{}, errors.New("needs contract export requires --out=<file>")
	}
	f.frameworkSet = flagWasSet(fs, "framework")
	if f.frameworkSet && f.language != "go" {
		return contractExportFlags{}, fmt.Errorf("--framework selects a go framework checkout; the %s framework comes from its target", f.language)
	}
	return f, nil
}
