package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/promptopt"
)

func runPromptOptimize(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return promptOptimizeCommand(ctx, args, os.Stdout, time.Now())
}

func promptOptimizeCommand(ctx context.Context, args []string, out io.Writer, now time.Time) error {
	f := flag.NewFlagSet("prompt-optimize", flag.ContinueOnError)
	path := f.String("experiment", "", "Measured held-out experiment JSON; selection only")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *path == "" || f.NArg() != 0 {
		return fmt.Errorf("--experiment FILE required, with no positional arguments")
	}
	raw, err := contextopt.ReadSnapshot(ctx, *path)
	if err != nil {
		return err
	}
	selection, err := promptopt.Select(ctx, raw, now)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(selection)
}
