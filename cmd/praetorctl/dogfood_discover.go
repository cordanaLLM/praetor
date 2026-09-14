package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func runDogfoodDiscovery(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dogfood discover", flag.ContinueOnError)
	var opts dogfood.DiscoveryOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Explicit pinned public discovery cohort; exclusive with --path")
	fs.StringVar(&opts.Path, "path", "", "Explicit local repository tree; exclusive with --config")
	fs.StringVar(&opts.PolicyPath, "policy", ".config/dogfood/discovery-policy.json", "Reviewed version-1 capability discovery rules")
	fs.StringVar(&opts.ArtifactDir, "artifacts", "", "New private evidence directory under an existing parent")
	fs.StringVar(&opts.Stage, "stage", "plan", "plan (declarations only) or observe (bounded source observation)")
	fs.IntVar(&opts.Concurrency, "concurrency", 4, "Concurrent public observations, 1..4")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("dogfood discover accepts flags only")
	}
	opts.AllowRemote = true // Explicit CLI observe stage authorizes only the pinned cohort.
	report, runErr := dogfood.RunDiscovery(ctx, opts)
	if report == nil {
		return runErr
	}
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(report))
}
