package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
		return compiler.VerifyCompiledContext(ctx, os.Stdout, tr, *source, *targetDir)
	}
	return compiler.CompileContextProjections(ctx, os.Stdout, tr, *source, *targetDir)
}
