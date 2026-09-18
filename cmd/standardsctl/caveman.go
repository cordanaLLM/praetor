package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	cavemanUsage = "usage: praetorctl caveman check [--surface=<name>] [--root=.] <file|dir|-> [...]\n" +
		"       praetorctl caveman estimate <file|dir|-> [...]"
	// maxCavemanFiles bounds the files one invocation reads, directories expanded (HISS-02).
	maxCavemanFiles = 4096
	// maxPrintedFindings bounds the findings printed per file; the count line says how many
	// more exist.
	maxPrintedFindings = 200
)

// cavemanInput is one text to measure: its display name and content.
type cavemanInput struct {
	name string
	text string
}

// runCaveman lints agent-facing text (check) or measures its token cost (estimate).
func runCaveman(args []string) error {
	return cavemanCommand(context.Background(), args, os.Stdin, os.Stdout)
}

func cavemanCommand(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(cavemanUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	switch args[0] {
	case "check":
		return cavemanCheck(ctx, args[1:], stdin, out)
	case "estimate":
		return cavemanEstimate(ctx, args[1:], stdin, out)
	}
	return fmt.Errorf("unknown caveman subcommand %q\n%s", args[0], cavemanUsage)
}

// cavemanCheck prints one summary line per input and its findings, and fails when any
// input breaks a rule. With --surface it first resolves the register of that surface from
// the repository at --root and skips the lint when the surface is not internal.
func cavemanCheck(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fset := flag.NewFlagSet("caveman check", flag.ContinueOnError)
	surface := fset.String("surface", "", "Register surface whose manifest setting decides whether the lint applies")
	root := fset.String("root", ".", "Repository root whose .standards.yaml resolves --surface")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if *surface != "" {
		enforced, err := cavemanSurfaceEnforced(ctx, out, *root, config.RegisterSurface(*surface))
		if err != nil || !enforced {
			return err
		}
	}
	inputs, err := readCavemanInputs(ctx, fset.Args(), stdin)
	if err != nil {
		return err
	}
	var text strings.Builder
	failed := 0
	for _, input := range inputs {
		if !formatCavemanReport(&text, input.name, caveman.Check(input.text, caveman.Options{})) {
			failed++
		}
	}
	if _, err := io.WriteString(out, text.String()); err != nil {
		return fmt.Errorf("caveman check: write report: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("caveman check: %d of %d input(s) failed", failed, len(inputs))
	}
	return nil
}

// cavemanSurfaceEnforced reads the register policy through the loader compile-context uses,
// so the lint and the rendered block can never read two different manifests.
func cavemanSurfaceEnforced(ctx context.Context, out io.Writer, root string, surface config.RegisterSurface) (bool, error) {
	policy, _, err := compiler.LoadRegisterBlock(ctx, root)
	if err != nil {
		return false, err
	}
	enforced, err := policy.LintEnforced(surface)
	if err != nil {
		return false, err
	}
	if enforced {
		return true, nil
	}
	resolution := policy.Resolve(surface, "")
	_, err = fmt.Fprintf(out, "caveman check: skip, %s = %s (lint applies to internal only)\n", resolution.Source, resolution.Register)
	return false, err
}

// formatCavemanReport appends the summary line and the bounded findings; it returns whether
// the input passed.
func formatCavemanReport(out *strings.Builder, name string, report caveman.Report) bool {
	verdict := "PASS"
	if !report.Passed() {
		verdict = "FAIL"
	}
	fmt.Fprintf(out, "%s: %s prose_words=%d articles=%d density=%.1f/100 limit=%.1f off_regions=%d findings=%d\n",
		name, verdict, report.ProseWords, report.Articles, report.Density(), caveman.DefaultMaxArticleDensity,
		report.OffRegions, len(report.Findings))
	for i := 0; i < len(report.Findings) && i < maxPrintedFindings; i++ {
		f := report.Findings[i]
		fmt.Fprintf(out, "%s:%d %s: %s\n", name, f.Line, f.Rule, f.Excerpt)
	}
	if extra := len(report.Findings) - maxPrintedFindings; extra > 0 {
		fmt.Fprintf(out, "%s: (+%d more findings)\n", name, extra)
	}
	return report.Passed()
}

// cavemanEstimate prints bytes, lines and estimated tokens per input and in total.
func cavemanEstimate(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	inputs, err := readCavemanInputs(ctx, args, stdin)
	if err != nil {
		return err
	}
	var text strings.Builder
	var bytesTotal, tokensTotal int
	for _, input := range inputs {
		tokens := caveman.EstimateTokens(input.text)
		fmt.Fprintf(&text, "%s: bytes=%d lines=%d tokens_est=%d\n", input.name, len(input.text), countLines(input.text), tokens)
		bytesTotal += len(input.text)
		tokensTotal += tokens
	}
	fmt.Fprintf(&text, "total: inputs=%d bytes=%d tokens_est=%d\n", len(inputs), bytesTotal, tokensTotal)
	_, err = io.WriteString(out, text.String())
	return err
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// readCavemanInputs reads every named file, the Markdown files below every named directory
// and, for "-", standard input. Files go through the bounded snapshot reader (1 MiB, UTF-8,
// symlink-resistant) that compile-context uses.
func readCavemanInputs(ctx context.Context, args []string, stdin io.Reader) ([]cavemanInput, error) {
	if len(args) == 0 {
		return nil, errors.New(cavemanUsage)
	}
	paths, err := expandCavemanPaths(ctx, args)
	if err != nil {
		return nil, err
	}
	inputs := make([]cavemanInput, 0, len(paths))
	for _, path := range paths {
		input, err := readCavemanInput(ctx, path, stdin)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func readCavemanInput(ctx context.Context, path string, stdin io.Reader) (cavemanInput, error) {
	if path == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, contextopt.MaxSourceBytes+1))
		if err != nil {
			return cavemanInput{}, fmt.Errorf("read standard input: %w", err)
		}
		if len(data) > contextopt.MaxSourceBytes {
			return cavemanInput{}, fmt.Errorf("standard input exceeds %d bytes", contextopt.MaxSourceBytes)
		}
		return cavemanInput{name: "-", text: string(data)}, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return cavemanInput{}, fmt.Errorf("read %s: %w", path, err)
	}
	return cavemanInput{name: filepath.ToSlash(path), text: string(data)}, nil
}

// expandCavemanPaths replaces each directory with the .md files below it, in lexical order.
func expandCavemanPaths(ctx context.Context, args []string) ([]string, error) {
	var paths []string
	for _, arg := range args {
		if arg == "-" {
			paths = append(paths, arg)
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", arg, err)
		}
		if !info.IsDir() {
			paths = append(paths, arg)
		} else if paths, err = appendMarkdownFiles(ctx, paths, arg); err != nil {
			return nil, err
		}
		if len(paths) > maxCavemanFiles {
			return nil, fmt.Errorf("caveman: more than %d input files", maxCavemanFiles)
		}
	}
	return paths, nil
}

func appendMarkdownFiles(ctx context.Context, paths []string, dir string) ([]string, error) {
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(path), ".md") {
			paths = append(paths, path)
		}
		if len(paths) > maxCavemanFiles {
			return fmt.Errorf("caveman: more than %d input files", maxCavemanFiles)
		}
		return nil
	})
	return paths, err
}
