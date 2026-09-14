// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func runContextOptimize(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return contextOptimize(ctx, args, os.Stdout)
}

func contextOptimize(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("context-optimize", flag.ContinueOnError)
	root := flags.String("root", "", "Explicit directory containing selected text sources")
	destination := flags.String("output-dir", "", "Optional NEW directory under an existing parent for a private review candidate; no activation")
	var usageErr error
	flags.Usage = func() {
		_, usageErr = fmt.Fprintln(flags.Output(), "Usage: standardsctl context-optimize --root DIR [--output-dir NEW-DIR] SOURCE...\nSelect 1..64 relative files. Without --output-dir, report metadata only. Savings compare all selected bytes with the candidate, not live per-turn context.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return errors.Join(err, usageErr)
	}
	plan, err := contextopt.Analyze(ctx, contextopt.Options{Root: *root, Sources: flags.Args()})
	if err != nil {
		return err
	}
	if *destination != "" {
		if err := plan.WriteCandidate(ctx, *destination); err != nil {
			return err
		}
	}
	return json.NewEncoder(output).Encode(plan.Metadata())
}
