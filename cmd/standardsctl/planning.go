package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/planning"
)

func runPlanning(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return planningCommand(ctx, args, os.Stdout)
}

func planningCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: planning prepare --input FILE [--output-dir NEW-DIR]")
	}
	if args[0] != "prepare" {
		return fmt.Errorf("supported planning action: prepare")
	}
	flags := flag.NewFlagSet("planning prepare", flag.ContinueOnError)
	input := flags.String("input", "", "Strict planning draft JSON")
	directory := flags.String("output-dir", "", "Optional new private artifact directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return fmt.Errorf("exactly one nonempty --input and no extra arguments required")
	}
	raw, err := contextopt.ReadSnapshot(ctx, *input)
	if err != nil {
		return fmt.Errorf("read planning input: %w", err)
	}
	result, err := planning.Compile(ctx, raw)
	if err != nil {
		return err
	}
	written := *directory != ""
	if written {
		if err := contextopt.WriteArtifacts(ctx, *directory, result.Files); err != nil {
			return fmt.Errorf("write planning artifacts: %w", err)
		}
	}
	return encodePlanningCLIReport(output, result, written)
}

func encodePlanningCLIReport(output io.Writer, result *planning.Result, written bool) error {
	if err := json.NewEncoder(output).Encode(result.Report(written)); err != nil {
		return fmt.Errorf("encode planning metadata: %w", err)
	}
	return nil
}
