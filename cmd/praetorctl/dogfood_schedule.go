package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func runDogfoodSchedule(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "run" && args[0] != "status") {
		return errors.New("dogfood schedule requires run or status")
	}
	fs := flag.NewFlagSet("dogfood schedule "+args[0], flag.ContinueOnError)
	path := fs.String("config", "", "Explicit private version-1 schedule JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return errors.New("dogfood schedule requires --config and no positional arguments")
	}
	var report *dogfood.ScheduleReport
	var err error
	if args[0] == "run" {
		report, err = dogfood.RunSchedule(ctx, *path)
	} else {
		report, err = dogfood.ScheduleStatus(ctx, *path)
	}
	if report != nil {
		return errors.Join(err, json.NewEncoder(os.Stdout).Encode(report))
	}
	return err
}
