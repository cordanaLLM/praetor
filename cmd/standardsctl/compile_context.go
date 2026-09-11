package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

func runCompileContext(args []string) error {
	fs := flag.NewFlagSet("compile-context", flag.ContinueOnError)
	verify := fs.Bool("verify", false, "Verify target files match AGENTS.md without modifying them")
	source := fs.String("source", "AGENTS.md", "Path to canonical AGENTS.md file")
	targetDir := fs.String("target-dir", ".", "Root directory to write/verify target vendor files")

	if err := fs.Parse(args); err != nil {
		return err
	}

	tr := compiler.NewTranspiler()

	if *verify {
		fmt.Printf("Verifying agent context synchronization against %s...\n", *source)
		if err := tr.Verify(*source, *targetDir); err != nil {
			return fmt.Errorf("context verification failed: %w", err)
		}
		fmt.Println("All agent context targets are 100% in sync with canonical AGENTS.md.")
		return nil
	}

	fmt.Printf("Compiling agent context from canonical %s...\n", *source)
	res, err := tr.Compile(*source)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}

	if err := tr.WriteOutputs(res, *targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}

	for _, f := range res.Files {
		fmt.Printf("  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, compiler.MaxLineBudget)
	}

	agentsSrc := filepath.Join(*targetDir, ".agents", "agents")
	agentFiles, aErr := compiler.CompileAgents(context.Background(), agentsSrc, *targetDir)
	if aErr == nil && len(agentFiles) > 0 {
		fmt.Printf("  [COMPILED] %d autonomous agent vendor projections.\n", len(agentFiles))
	}

	fmt.Println("Cross-agent context transpilation completed successfully.")
	return nil
}
