package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/editor"
)

// maxReportedEditorFiles is the scalar upper bound (HISS-02) on the per-file lines the
// editors report prints.
const maxReportedEditorFiles = 512

func runEditors(args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: praetorctl editors <generate|verify> [--path=.] [--editors=a,b]")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	fs := flag.NewFlagSet("editors "+sub, flag.ContinueOnError)
	path := fs.String("path", ".", "Workspace root directory")
	editorsFlag := fs.String("editors", "",
		"Comma-separated editor ids/aliases to target (default: every supported editor)")
	positional, err := parseInterspersed(fs, subArgs)
	if err != nil {
		return err
	}
	root := positionalAt(positional, 0, *path)

	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = root
	if ids := splitCommaList(*editorsFlag); len(ids) > 0 {
		opts.Editors = ids
	}

	switch sub {
	case "generate":
		return runEditorsGenerate(opts, root)
	case "verify":
		return runEditorsVerify(opts, root)
	default:
		return fmt.Errorf("unknown editors command: %s", sub)
	}
}

func runEditorsGenerate(opts editor.Options, root string) error {
	set, err := editor.Synthesize(opts)
	if err != nil {
		return fmt.Errorf("failed synthesizing editor configurations: %w", err)
	}
	// The outcome of each file is the decision editor.WriteWithReport made before
	// writing, not a later comparison of workspace bytes against a template: a merged
	// JSON file legitimately differs from its template.
	report, err := editor.WriteWithReport(set, root)
	if err != nil {
		return fmt.Errorf("failed writing editor configurations: %w", err)
	}
	counts := reportEditorFiles(report)
	fmt.Printf("[OK] Editor requirements: %d created, %d merged, %d already present, %d rewritten across: %v\n",
		counts[editor.WriteCreated], counts[editor.WriteMerged], counts[editor.WritePresent],
		counts[editor.WriteRewritten], set.Editors)
	if preserved := counts[editor.WritePreserved]; preserved > 0 {
		fmt.Printf("[UNVERIFIED] %d existing human-owned file(s) preserved without verification.\n", preserved)
	}
	return nil
}

func runEditorsVerify(opts editor.Options, root string) error {
	set, err := editor.Synthesize(opts)
	if err != nil {
		return fmt.Errorf("failed synthesizing editor configurations: %w", err)
	}
	report, err := editor.VerifyWithReport(set, root)
	if err != nil {
		return fmt.Errorf("[FAIL] Editor configurations out of sync: %w", err)
	}
	fmt.Printf("[PASS] Managed requirements verified in %d editor configuration file(s).\n", len(report.Verified))
	if len(report.PreservedUnverified) > 0 {
		fmt.Printf("[UNVERIFIED] Preserved %d non-JSON file(s) without semantic verification: %v\n",
			len(report.PreservedUnverified), report.PreservedUnverified)
	}
	return nil
}

// reportEditorFiles prints the per-file outcome of a generate run and counts each outcome.
func reportEditorFiles(report editor.WriteReport) map[editor.WriteOutcome]int {
	counts := make(map[editor.WriteOutcome]int)
	for i := 0; i < len(report.Files) && i < maxReportedEditorFiles; i++ {
		f := report.Files[i]
		counts[f.Outcome]++
		fmt.Printf("  - [%s] [%s] %s\n", f.Outcome, f.Editor, f.Path)
	}
	return counts
}
