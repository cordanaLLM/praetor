// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// runLinters manages per-file linter exception rules and configurations.
func runLinters(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printLintersUsage()
		return nil
	}
	if args[0] != "exceptions" {
		return fmt.Errorf("unknown linters command: %s; available commands: exceptions", args[0])
	}
	return runLintersExceptions(args[1:])
}

func printLintersUsage() {
	fmt.Println("Usage: praetorctl linters exceptions <strip-config|generate> [flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  strip-config    Write .golangci.yml without generated exclusions and with relative-path-mode: gitroot")
	fmt.Println("  generate        Read golangci-lint JSON report and generate or ratchet exclusions")
}

func runLintersExceptions(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printLintersUsage()
		return nil
	}
	switch args[0] {
	case "strip-config":
		return runStripConfig(args[1:])
	case "generate":
		return runGenerateExceptions(args[1:])
	default:
		return fmt.Errorf("unknown linters exceptions command: %s; available: strip-config, generate", args[0])
	}
}

func runStripConfig(args []string) error {
	fs := flag.NewFlagSet("linters exceptions strip-config", flag.ContinueOnError)
	out := fs.String("out", "", "Output path for the stripped configuration")
	cfg := fs.String("config", ".golangci.yml", "Path to source .golangci.yml")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("strip-config: --out is required")
	}
	return adopt.StripGolangciConfig(*cfg, *out)
}

func parseGenerateDate(todayStr string) (time.Time, error) {
	if todayStr == "" {
		return time.Now(), nil
	}
	t, err := time.Parse(config.ExceptionDateLayout, todayStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("--today %q must be a YYYY-MM-DD date: %w", todayStr, err)
	}
	return t, nil
}

func runGenerateExceptions(args []string) error {
	fs := flag.NewFlagSet("linters exceptions generate", flag.ContinueOnError)
	report := fs.String("report", "", "Path to golangci-lint JSON report")
	issue := fs.Int("issue", 0, "Issue number justifying the exceptions (e.g. 1030)")
	category := fs.String("category", "C", "Exception category (A, B, C or D)")
	todayStr := fs.String("today", "", "Evaluation date (YYYY-MM-DD, defaults to today)")
	write := fs.Bool("write", false, "Write updated exceptions block into configuration")
	cfg := fs.String("config", ".golangci.yml", "Path to .golangci.yml")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *report == "" {
		return errors.New("generate: --report is required")
	}
	if *issue <= 0 {
		return errors.New("generate: --issue is required and must be positive")
	}
	today, err := parseGenerateDate(*todayStr)
	if err != nil {
		return err
	}
	res, err := adopt.GenerateExceptions(adopt.GenerateExceptionsOptions{
		ConfigPath: *cfg,
		ReportPath: *report,
		Issue:      *issue,
		Category:   *category,
		Today:      today,
		Write:      *write,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return exitStatusError{code: res.ExitCode}
	}
	return nil
}
