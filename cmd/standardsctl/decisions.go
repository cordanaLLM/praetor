package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/decisions"
)

func runDecisions(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		return errors.New("usage: praetorctl decisions <export|pin-check|serve> [options]")
	}
	switch args[0] {
	case "export":
		return runDecisionExport(args[1:])
	case "pin-check":
		return runDecisionPinCheck(args[1:])
	case "serve":
		return runDecisionServe(args[1:])
	default:
		return fmt.Errorf("unknown decisions command %q", args[0])
	}
}

func runDecisionExport(args []string) error {
	flags := flag.NewFlagSet("decisions export", flag.ContinueOnError)
	repoPath := flags.String("path", ".", "Repository whose docs/adr records are exported")
	outDir := flags.String("out", "", "Explicit output directory")
	if _, err := parseInterspersed(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("decisions export accepts flags only")
	}
	ctx, cancel := context.WithTimeout(rootContext(), 2*time.Minute)
	defer cancel()
	root, err := filepath.Abs(*repoPath)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	report, err := decisions.Export(ctx, root, *outDir)
	if err != nil {
		return err
	}
	fmt.Printf("Exported %d ADR decision records; skipped %d scaffold files.\n", len(report.Written), len(report.Skipped))
	return nil
}

func runDecisionPinCheck(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: praetorctl decisions pin-check")
	}
	ctx, cancel := context.WithTimeout(rootContext(), 2*time.Minute)
	defer cancel()
	if err := decisions.PinCheck(ctx); err != nil {
		return err
	}
	fmt.Printf("decided-mcp %s matches the pinned SHA-256.\n", decisions.Version)
	return nil
}

func runDecisionServe(args []string) error {
	flags := flag.NewFlagSet("decisions serve", flag.ContinueOnError)
	repoPath := flags.String("path", ".", "Repository whose docs/adr records are served")
	outDir := flags.String("out", "", "Explicit corpus output directory")
	if _, err := parseInterspersed(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("decisions serve accepts flags only")
	}
	ctx, cancel := context.WithTimeout(rootContext(), 24*time.Hour)
	defer cancel()
	root, err := filepath.Abs(*repoPath)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	return childExitStatus(decisions.Serve(ctx, root, *outDir))
}

// childExitStatus returns a child process's own exit status silently, as praetorctl's: the child
// already wrote its diagnostic to the stderr it shares with this process (decided-mcp under
// `decisions serve`, the admitted step under `admit`). Every other error keeps its text.
func childExitStatus(err error) error {
	var child *exec.ExitError
	if errors.As(err, &child) && child.ExitCode() > 0 {
		return exitStatusError{code: child.ExitCode()}
	}
	return err
}
