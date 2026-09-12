package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// compileContextTimeout bounds the transpilation and persona projection I/O (HISS-02).
const compileContextTimeout = 2 * time.Minute

func runCompileContext(args []string) error {
	fs := flag.NewFlagSet("compile-context", flag.ContinueOnError)
	verify := fs.Bool("verify", false, "Verify target files match AGENTS.md without modifying them")
	source := fs.String("source", "AGENTS.md", "Path to canonical AGENTS.md file")
	targetDir := fs.String("target-dir", ".", "Root directory to write/verify target vendor files")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("compile-context accepts no positional arguments, got %q", fs.Args())
	}

	ctx, cancel := context.WithTimeout(context.Background(), compileContextTimeout)
	defer cancel()

	tr := compiler.NewTranspiler()
	if *verify {
		return verifyCompiledContext(tr, *source, *targetDir)
	}
	return compileContext(ctx, tr, *source, *targetDir)
}

// verifyCompiledContext checks the six transpiled vendor files and every persona
// projection without writing anything.
func verifyCompiledContext(tr *compiler.Transpiler, source, targetDir string) error {
	fmt.Printf("Verifying agent context synchronization against %s...\n", source)
	if err := tr.Verify(source, targetDir); err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	verified, err := verifyAgentProjections(targetDir)
	if err != nil {
		return fmt.Errorf("agent persona verification failed: %w", err)
	}
	fmt.Printf("All agent context targets are 100%% in sync with canonical AGENTS.md (%d persona projections verified).\n", verified)
	return nil
}

// compileContext writes the vendor context files and every persona projection; any
// projection failure is an error, never a silently skipped success line.
func compileContext(ctx context.Context, tr *compiler.Transpiler, source, targetDir string) error {
	fmt.Printf("Compiling agent context from canonical %s...\n", source)
	res, err := tr.Compile(source)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if err := tr.WriteOutputs(res, targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	for _, f := range res.Files {
		fmt.Printf("  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, compiler.MaxLineBudget)
	}

	agentsSrc := filepath.Join(targetDir, filepath.FromSlash(canonicalAgentsRel))
	agentFiles, err := compiler.CompileAgents(ctx, agentsSrc, targetDir)
	if err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if len(agentFiles) > 0 {
		fmt.Printf("  [COMPILED] %d autonomous agent vendor projections.\n", len(agentFiles))
	}
	pluginFiles, err := projectPluginAgents(targetDir)
	if err != nil {
		return fmt.Errorf("plugin agent projection failed: %w", err)
	}
	if pluginFiles > 0 {
		fmt.Printf("  [COMPILED] %d plugin agent projections (%s).\n", pluginFiles, pluginAgentsRel)
	}

	fmt.Println("Cross-agent context transpilation completed successfully.")
	return nil
}
