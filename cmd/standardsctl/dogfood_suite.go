package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func runDogfoodSuite(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dogfood suite", flag.ContinueOnError)
	var opts dogfood.SuiteOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Explicit version-1 JSON suite configuration")
	fs.StringVar(&opts.ArtifactDir, "artifacts", "", "New private evidence directory under an existing parent")
	fs.StringVar(&opts.SourceRoot, "source-root", ".", "Validated Praetor source bundle for public cases")
	fs.StringVar(&opts.Stage, "stage", "plan", "plan (declarations only) or verify (execute and replay)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || opts.ConfigPath == "" || opts.ArtifactDir == "" {
		return errors.New("dogfood suite requires --config and --artifacts and no positional arguments")
	}
	opts.AllowRemote = true // Explicit CLI verify stage authorizes its declared pinned GitHub sources.
	report, runErr := dogfood.RunSuite(ctx, opts)
	if report != nil {
		return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(report))
	}
	return runErr
}
