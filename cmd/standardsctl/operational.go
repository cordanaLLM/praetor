package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/operationalsync"
)

func runOperational(args []string) error {
	if len(args) < 2 || args[0] != "sync" {
		return errors.New("usage: operational sync plan|prepare --owner-path PATH --source-path PATH --owner-sha SHA --base-sha SHA --source-sha SHA [--destination NEW_PATH]")
	}
	return runOperationalSync(context.Background(), args[1], args[2:])
}

func runOperationalSync(ctx context.Context, stage string, args []string) error {
	fs := flag.NewFlagSet("operational sync "+stage, flag.ContinueOnError)
	var opts operationalsync.Options
	fs.StringVar(&opts.OwnerPath, "owner-path", "", "Clean existing operational checkout (read only)")
	fs.StringVar(&opts.SourcePath, "source-path", "", "Local public source checkout containing reviewed commits (read only)")
	fs.StringVar(&opts.OwnerSHA, "owner-sha", "", "Exact reviewed owner HEAD commit")
	fs.StringVar(&opts.BaseSHA, "base-sha", "", "Public commit already incorporated into owner history")
	fs.StringVar(&opts.SourceSHA, "source-sha", "", "Exact reviewed new public commit, descending from base")
	fs.StringVar(&opts.Destination, "destination", "", "New external clone for prepare; must not exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("operational sync accepts no positional arguments after stage")
	}
	report, runErr := operationalsync.Run(ctx, stage, opts)
	if report != nil {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return errors.Join(runErr, fmt.Errorf("encode operational report: %w", err))
		}
	}
	return runErr
}
