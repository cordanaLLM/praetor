package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// compileFrameworkAssetsTimeout bounds the kit asset writes (HISS-02).
const compileFrameworkAssetsTimeout = time.Minute

// runCompileFrameworkAssets is the entry point ADR-0007 clause 5 names for the framework
// kit asset compiler (BUG-493, BUG-694). It reads one kit config and writes llms.txt,
// llms-full.txt, .agents/rules/<kit_name>.md and the starter templates under --output.
// --output has no default: the assets carry fixed names such as llms.txt, and writing
// them into the working directory by accident would replace a repository's own files.
func runCompileFrameworkAssets(args []string) error {
	fs := flag.NewFlagSet("compile-framework-assets", flag.ContinueOnError)
	configPath := fs.String("config", "", "Framework kit config: one YAML document with kit_name, language, version, description, rules, skills, components (required)")
	output := fs.String("output", "", "Directory to write the kit assets into (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("compile-framework-assets accepts no positional arguments, got %q", fs.Args())
	}
	if strings.TrimSpace(*configPath) == "" {
		return errors.New("compile-framework-assets requires --config")
	}
	if strings.TrimSpace(*output) == "" {
		return errors.New("compile-framework-assets requires --output")
	}

	kit, err := compiler.LoadFrameworkKitConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), compileFrameworkAssetsTimeout)
	defer cancel()
	res, err := compiler.CompileFrameworkAssets(ctx, kit, *output)
	if err != nil {
		return fmt.Errorf("compile framework assets for %s: %w", kit.KitName, err)
	}

	fmt.Printf("Compiled framework kit %s into %s:\n", res.KitName, *output)
	fmt.Printf("  %s\n  %s\n  %s\n", res.LLMsTxtPath, res.LLMsFullTxtPath, res.AgentRulePath)
	fmt.Printf("  %s\n  %s\n", res.Templates[".framework-build.yaml"], res.Templates["README.md"])
	return nil
}
