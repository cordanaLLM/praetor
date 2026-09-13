package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/repairrun"
)

func runDogfoodRepairAction(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "run" && args[0] != "status") {
		return errors.New("dogfood repairs requires run or status")
	}
	fs := flag.NewFlagSet("dogfood repairs "+args[0], flag.ContinueOnError)
	config := fs.String("config", "", "Explicit private repair execution configuration")
	reportPath := fs.String("report", "", "Retained completed dogfood suite report")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *config == "" || *reportPath == "" || fs.NArg() != 0 {
		return errors.New("dogfood repairs run/status requires --config, --report and no positional arguments")
	}
	configPath, err := filepath.Abs(*config)
	if err != nil {
		return err
	}
	suitePath, err := filepath.Abs(*reportPath)
	if err != nil {
		return err
	}
	return invokeDogfoodRepairAction(ctx, args[0], configPath, suitePath)
}

func invokeDogfoodRepairAction(ctx context.Context, action, configPath, suitePath string) error {
	ctx, cancel := context.WithTimeout(ctx, repairrun.MaxDuration)
	defer cancel()
	var report *repairrun.Report
	var err error
	if action == "run" {
		report, err = repairrun.Run(ctx, configPath, suitePath)
	} else {
		report, err = repairrun.Status(ctx, configPath, suitePath)
	}
	if report == nil {
		return err
	}
	return errors.Join(err, json.NewEncoder(os.Stdout).Encode(report))
}
