package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/bugledger"
)

// runBugs audits the bug ledger's recorded locations.
//
// The ledger is the first thing an agent reads when asked what to work on, and nothing has
// ever re-read the locations it records. A row that points past the end of its file has not
// been looked at since the file changed, which is a cheap and certain signal that the row is
// stale even though deciding whether the defect survives needs a human (#158).
func runBugs(args []string) error {
	if len(args) == 0 || args[0] != "audit" {
		return errors.New("usage: praetorctl bugs audit [--path=.] [--strict]")
	}
	fs := flag.NewFlagSet("bugs audit", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Repository root whose bug ledger is audited")
	strict := fs.Bool("strict", false, "Exit non-zero when a recorded location no longer resolves")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	report, err := bugledger.Audit(context.Background(), *repoPath)
	if err != nil {
		return err
	}
	// The counts print on a clean run too: a report that lists only failures cannot be told
	// apart from one that read no rows at all.
	fmt.Printf("=== Bug Ledger Audit: %s ===\n", *repoPath)
	fmt.Printf("  Rows read:            %d\n", report.Rows)
	fmt.Printf("  Open rows:            %d\n", report.Open)
	fmt.Printf("  With a file:line:     %d\n", report.Locatable)
	fmt.Printf("  Unresolvable:         %d\n", len(report.Findings))
	if len(report.Findings) == 0 {
		fmt.Println("[PASS] every open row's recorded location still resolves.")
		return nil
	}
	for i := 0; i < len(report.Findings) && i < 256; i++ {
		fmt.Fprintf(os.Stderr, "  %s\n", report.Findings[i])
	}
	fmt.Fprintf(os.Stderr, "\nA location past the end of its file means the row has not been re-read since\n"+
		"the file changed. That does not prove the defect is fixed; it proves nobody checked.\n")
	if *strict {
		return fmt.Errorf("[FAIL] %d open row(s) record a location that no longer resolves", len(report.Findings))
	}
	return nil
}
