package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/notebook"
)

func runNotebook(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return notebookCommand(ctx, args, os.Stdout)
}

func notebookCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: notebook prepare|validate --bundle FILE [--result FILE] [--output-dir NEW-DIR]")
	}
	// Matched before flag parsing (BUG-811): args[0] here is the action token
	// (prepare/validate), never a parsed Go flag, so "-h"/"--help"/"help" fell through
	// to "explicit bundle and no extra arguments required" instead of exiting 0.
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(out, "Usage: notebook prepare|validate --bundle FILE [--result FILE] [--output-dir NEW-DIR]")
		return nil
	}
	f := flag.NewFlagSet("notebook "+args[0], flag.ContinueOnError)
	bundlePath := f.String("bundle", "", "Explicit private NotebookLM snapshot")
	resultPath := f.String("result", "", "Generation JSON to validate")
	directory := f.String("output-dir", "", "New private output directory")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *bundlePath == "" {
		return fmt.Errorf("explicit bundle and no extra arguments required")
	}
	if args[0] != "prepare" && args[0] != "validate" {
		return fmt.Errorf("supported notebook actions: prepare, validate")
	}
	raw, err := contextopt.ReadSnapshot(ctx, *bundlePath)
	if err != nil {
		return err
	}
	if args[0] == "validate" {
		return validateNotebookCommand(ctx, raw, *resultPath, *directory, out)
	}
	return prepareNotebookCommand(ctx, raw, *resultPath, *directory, out)
}

func prepareNotebookCommand(ctx context.Context, raw []byte, resultPath, directory string, out io.Writer) error {
	if resultPath != "" {
		return fmt.Errorf("prepare does not accept --result")
	}
	pack, err := notebook.Prepare(ctx, raw)
	if err != nil {
		return err
	}
	if directory != "" {
		if err := contextopt.WriteArtifacts(ctx, directory, pack.Files); err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(pack)
}

func validateNotebookCommand(ctx context.Context, raw []byte, resultPath, directory string, out io.Writer) error {
	if resultPath == "" {
		return fmt.Errorf("validate requires --result")
	}
	data, err := contextopt.ReadSnapshot(ctx, resultPath)
	if err != nil {
		return err
	}
	g, err := notebook.ValidateGeneration(ctx, raw, data)
	if err != nil {
		return err
	}
	if directory != "" {
		files := map[string][]byte{"generation.json": data}
		for key, text := range g.Documents {
			files[key+".md"] = []byte("<!-- Structurally validated draft; semantic review required. -->\n" + text)
		}
		if err := contextopt.WriteArtifacts(ctx, directory, files); err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"structurally_valid": true, "semantic_review_required": true, "bundle_sha256": g.BundleSHA256, "requirements": len(g.Requirements)})
}
