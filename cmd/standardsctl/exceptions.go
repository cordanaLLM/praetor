// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// runExceptions dispatches the exceptions command and its subcommands (list).
func runExceptions(args []string) error {
	fs := flag.NewFlagSet("exceptions", flag.ContinueOnError)
	manifestPath := fs.String("config", config.ManifestFileName, "Path to .standards.yaml")
	expiringWithin := fs.Int("expiring-within", -1, "Filter entries expiring within this many days")
	todayStr := fs.String("today", "", "The date expiry is judged against (YYYY-MM-DD; default: today in UTC)")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	action := positionalAt(positional, 0, "list")
	if len(positional) > 1 {
		return fmt.Errorf("exceptions accepts at most one action, got %q", positional)
	}

	switch action {
	case "list":
		var expiringWithinPtr *int
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "expiring-within" {
				expiringWithinPtr = expiringWithin
			}
		})
		if expiringWithinPtr != nil && *expiringWithinPtr < 0 {
			return fmt.Errorf("--expiring-within must be non-negative, got %d", *expiringWithinPtr)
		}
		today := config.ExceptionDay(time.Now())
		if *todayStr != "" {
			parsedToday, err := time.Parse(config.ExceptionDateLayout, *todayStr)
			if err != nil {
				return fmt.Errorf("--today %q must be a YYYY-MM-DD date: %w", *todayStr, err)
			}
			today = config.ExceptionDay(parsedToday)
		}
		return handleExceptionsList(*manifestPath, expiringWithinPtr, today)
	default:
		return fmt.Errorf("unknown exceptions action: %s (supported: list)", action)
	}
}

// handleExceptionsList loads the manifest and prints the matching exceptions.
func handleExceptionsList(manifestPath string, expiringWithin *int, today time.Time) error {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest %s: %w", manifestPath, err)
	}

	matching := manifest.Exceptions
	if expiringWithin != nil {
		filtered, err := config.FilterExceptionsExpiringWithin(manifest.Exceptions, today, *expiringWithin)
		if err != nil {
			return fmt.Errorf("filter exceptions: %w", err)
		}
		matching = filtered
	}

	fmt.Printf("=== Declared Exceptions (%s) ===\n", manifestPath)
	if len(matching) == 0 {
		if expiringWithin != nil {
			fmt.Printf("No exceptions expiring within %d days.\n", *expiringWithin)
		} else {
			fmt.Println("No exceptions declared.")
		}
		return nil
	}

	for index := 0; index < len(matching) && index < config.MaxExceptions; index++ {
		printExceptionEntry(matching[index], today)
	}
	return nil
}

// printExceptionEntry prints one exceptions entry with rule, path, reason, and days left.
func printExceptionEntry(entry config.Exception, today time.Time) {
	daysLeft, err := entry.DaysLeft(today)
	daysStr := "unknown"
	if err == nil {
		daysStr = config.FormatDaysLeft(daysLeft)
	}
	if entry.Function != "" {
		fmt.Printf("  - rule: %s, path: %s, function: %s, reason: %s (%s)\n",
			entry.Rule, entry.Target(), entry.Function, entry.Reason, daysStr)
		return
	}
	fmt.Printf("  - rule: %s, path: %s, reason: %s (%s)\n",
		entry.Rule, entry.Target(), entry.Reason, daysStr)
}
