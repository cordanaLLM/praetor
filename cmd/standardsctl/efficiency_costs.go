// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/cordanaLLM/praetor/internal/efficiency/costledger"
)

type efficiencyCostsFlags struct {
	format string
	input  string
}

func parseEfficiencyCostsFlags(args []string) (*efficiencyCostsFlags, error) {
	fs := flag.NewFlagSet("efficiency costs", flag.ContinueOnError)
	format := fs.String("format", "table", "Output format: table or json")
	jsonFlag := fs.Bool("json", false, "Alias for --format=json")
	input := fs.String("input", "", "Path to cost ledger JSON file")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	if len(positional) > 0 {
		return nil, fmt.Errorf("efficiency costs accepts no positional arguments, got %q", positional)
	}
	if *input == "" {
		return nil, errors.New("efficiency costs requires --input <file>")
	}

	outFormat := strings.ToLower(strings.TrimSpace(*format))
	if *jsonFlag {
		outFormat = "json"
	}
	if outFormat != "table" && outFormat != "json" {
		return nil, fmt.Errorf("invalid format %q: must be 'table' or 'json'", outFormat)
	}

	return &efficiencyCostsFlags{
		format: outFormat,
		input:  *input,
	}, nil
}

func runEfficiencyCostsTo(args []string, out io.Writer) error {
	fl, err := parseEfficiencyCostsFlags(args)
	if err != nil {
		return err
	}
	ledger, err := costledger.Load(fl.input)
	if err != nil {
		return err
	}
	report, err := costledger.ComputeReport(ledger)
	if err != nil {
		return err
	}
	if fl.format == "json" {
		return costledger.RenderJSON(report, out)
	}
	return costledger.RenderTable(report, out)
}
