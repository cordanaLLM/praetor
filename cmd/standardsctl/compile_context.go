package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

// compileContextTimeout bounds the transpilation and persona projection I/O (HISS-02).
const compileContextTimeout = 2 * time.Minute

// engineBuild describes the running binary for the engine-build check; tests substitute a
// fake stale build.
var engineBuild = workstation.RunningBuild

func runCompileContext(args []string) error {
	fs := flag.NewFlagSet("compile-context", flag.ContinueOnError)
	verify := fs.Bool("verify", false, "Verify target files match AGENTS.md without modifying them")
	source := fs.String("source", "AGENTS.md", "Path to canonical AGENTS.md file")
	targetDir := fs.String("target-dir", ".", "Root directory to write/verify target vendor files")

	if _, err := parseInterspersed(fs, args); err != nil {
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
	// A client wrapper runs this write at every session start with whatever praetorctl is
	// installed; an install older than the engine checkout rewrote the register block and every
	// vendor file with its own stale text (BUG-1004). Verification never writes, so only the
	// write is refused.
	if err := workstation.CheckBuildCurrent(ctx, *targetDir, engineBuild()); err != nil {
		return fmt.Errorf("compile-context wrote nothing: %w", err)
	}
	// The text register block sends agent evidence to config.EvidenceDir beside the source; Git
	// has to ignore it before the rule is rendered (BUG-604).
	return adopt.CompileAgentContext(ctx, os.Stdout, tr, *source, *targetDir)
}
