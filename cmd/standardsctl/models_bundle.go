// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/reviewbundle"
)

const modelBundleUsage = "standardsctl models bundle --repo <dir> --target <ref> --head <ref> [--max-file-bytes N] [--max-files N] [--json]"

// isModelsBundle selects the bundle parser, whose --target is independent of outcome's target.
func isModelsBundle(args []string) bool {
	return len(args) > 0 && args[0] == "bundle"
}

// runModelsBundle reports argument/reference failures with status two and conflicts with success.
func runModelsBundle(args []string) error {
	if err := handleModelsBundle(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		if _, writeErr := fmt.Fprintf(os.Stderr, "models bundle: %v\n", err); writeErr != nil {
			return fmt.Errorf("bundle diagnostic: %w", writeErr)
		}
		return exitStatusError{code: 2}
	}
	return nil
}

// handleModelsBundle parses isolated bundle flags without loading routing configuration.
func handleModelsBundle(args []string) error {
	fs := flag.NewFlagSet(modelBundleUsage, flag.ContinueOnError)
	repo := fs.String("repo", ".", "Local Git repository")
	target := fs.String("target", "", "Current target ref")
	head := fs.String("head", "", "Head ref to review")
	maxFile := fs.Int("max-file-bytes", 64<<10, "Maximum retained bytes per file")
	maxFiles := fs.Int("max-files", 200, "Maximum included files")
	asJSON := fs.Bool("json", false, "Emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("bundle flags: %w", err)
	}
	if err := validateModelBundleArgs(fs, *repo, *target, *head); err != nil {
		return err
	}
	if *maxFile <= 0 || *maxFiles <= 0 {
		return fmt.Errorf("--max-file-bytes and --max-files must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
	defer cancel()
	b, err := reviewbundle.Build(ctx, *repo, *target, *head, reviewbundle.Options{MaxFileBytes: *maxFile, MaxFiles: *maxFiles})
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(b); err != nil {
			return fmt.Errorf("bundle JSON: %w", err)
		}
		return nil
	}
	return writeModelBundle(os.Stdout, b)
}

// validateModelBundleArgs requires two refs, a repository and no trailing positional arguments.
func validateModelBundleArgs(fs *flag.FlagSet, repo, target, head string) error {
	if fs.NArg() != 0 || repo == "" || target == "" || head == "" {
		return fmt.Errorf("%s", modelBundleUsage)
	}
	return nil
}

// writeModelBundle names every conflict, file truncation state and omitted path.
func writeModelBundle(w io.Writer, b reviewbundle.Bundle) error {
	if _, err := fmt.Fprintf(w, "Target: %s\nHead: %s\nMerge base: %s\nPredicted tree: %s\n", b.Target, b.Head, b.MergeBase, b.Tree); err != nil {
		return fmt.Errorf("bundle header: %w", err)
	}
	for i := 0; i < len(b.Conflicts); i++ {
		c := b.Conflicts[i]
		if _, err := fmt.Fprintf(w, "CONFLICT: %q (%s)\n", c.Path, c.Kind); err != nil {
			return fmt.Errorf("bundle conflict: %w", err)
		}
	}
	for i := 0; i < len(b.Files); i++ {
		f := b.Files[i]
		mark := "no"
		if f.Truncated {
			mark = "yes"
		}
		if _, err := fmt.Fprintf(w, "%q TRUNCATED: %s (omitted bytes: %d; deleted: %t; binary: %t)\n", f.Path, mark, f.TruncatedBytes, f.Deleted, f.Binary); err != nil {
			return fmt.Errorf("bundle file: %w", err)
		}
	}
	for i := 0; i < len(b.Cut); i++ {
		if _, err := fmt.Fprintf(w, "CUT: %q TRUNCATED: yes\n", b.Cut[i]); err != nil {
			return fmt.Errorf("bundle cut: %w", err)
		}
	}
	return nil
}
