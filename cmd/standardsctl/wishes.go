package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/wishes"
)

const defaultWishesStore = ".workingdir/wishes.json"

type wishesOptions struct {
	subcommand  string
	storePath   string
	requestPath string
	showUsage   bool
}

func runWishes(args []string) error {
	options, err := parseWishesArgs(args)
	if err != nil {
		return err
	}
	if options.showUsage {
		printWishesUsage()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return executeWishes(ctx, options)
}

func parseWishesArgs(args []string) (wishesOptions, error) {
	if len(args) == 0 {
		return wishesOptions{showUsage: true}, nil
	}
	options := wishesOptions{subcommand: args[0]}
	if options.subcommand == "help" || options.subcommand == "-h" || options.subcommand == "--help" {
		options.showUsage = true
		return options, nil
	}
	fs := flag.NewFlagSet("wishes "+options.subcommand, flag.ContinueOnError)
	fs.StringVar(&options.storePath, "store", defaultWishesStore, "Private wish ledger path")
	fs.StringVar(&options.requestPath, "request", "", "Bounded JSON request path")
	if err := fs.Parse(args[1:]); err != nil {
		return wishesOptions{}, err
	}
	if fs.NArg() != 0 {
		return wishesOptions{}, errors.New("wishes accepts flags only")
	}
	return options, nil
}

func executeWishes(ctx context.Context, options wishesOptions) error {
	storePath := filepath.Clean(options.storePath)
	requestPath := filepath.Clean(options.requestPath)
	switch options.subcommand {
	case "status":
		if options.requestPath != "" {
			return errors.New("wishes status does not accept --request")
		}
		ledger, err := wishes.Read(ctx, storePath)
		if err != nil {
			return err
		}
		return writeWishesJSON(ledger)
	case "apply":
		if options.requestPath == "" {
			return errors.New("wishes apply requires --request")
		}
		request, err := readWishRequest(ctx, requestPath)
		if err != nil {
			return err
		}
		ledger, err := wishes.Apply(ctx, storePath, request)
		if err != nil {
			return err
		}
		return writeWishesJSON(ledger)
	default:
		return fmt.Errorf("unknown wishes subcommand: %s", options.subcommand)
	}
}

func printWishesUsage() {
	fmt.Println("Usage: praetorctl wishes <status|apply> [flags]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  status [--store=PATH]                  Read the private wish ledger; never initializes it")
	fmt.Println("  apply --request=PATH [--store=PATH]   Apply one validated request with revision compare-and-swap")
}

func readWishRequest(ctx context.Context, path string) (wishes.Request, error) {
	data, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return wishes.Request{}, fmt.Errorf("read wish request: %w", err)
	}
	if !exists {
		return wishes.Request{}, fmt.Errorf("wish request missing: %s", path)
	}
	return wishes.DecodeRequest(data)
}

func writeWishesJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode wish result: %w", err)
	}
	data = append(data, '\n')
	_, err = os.Stdout.Write(data)
	return err
}
