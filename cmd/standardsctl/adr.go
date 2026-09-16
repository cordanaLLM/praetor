package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/adr"
)

// runADR verifies that the repository does not contradict the decisions it records.
//
// A decision recorded only in prose is enforced by whoever remembers to read it. This command
// replays the machine-checkable clauses a record declares, so a decision that has been
// reintroduced fails a gate instead of waiting for a reviewer to notice.
func runADR(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: praetorctl adr verify [--path=.]")
	}
	fs := flag.NewFlagSet("adr verify", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Repository root to verify")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	report, err := adr.Verify(context.Background(), *repoPath)
	if err != nil {
		return err
	}
	// The count is printed on success too. A verifier that prints only failures cannot be
	// told apart from one that read nothing, which is how a gate goes green while covering
	// zero records.
	fmt.Printf("=== Decision Record Verification: %s ===\n", *repoPath)
	fmt.Printf("  Records read:        %d\n", report.Records)
	fmt.Printf("  Constraints replayed: %d\n", report.Constraints)
	if len(report.Findings) == 0 {
		fmt.Printf("[PASS] no recorded decision is contradicted by this repository.\n")
		return nil
	}
	for i := 0; i < len(report.Findings) && i < 64; i++ {
		fmt.Fprintf(os.Stderr, "  %s\n", report.Findings[i])
	}
	return fmt.Errorf("[FAIL] %d recorded decision(s) contradicted", len(report.Findings))
}
