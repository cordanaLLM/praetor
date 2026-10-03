package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
)

// maxReportedEditorFiles is the scalar upper bound (HISS-02) on the per-file lines the
// editors report prints.
const maxReportedEditorFiles = 512

// editorsResolveTimeout bounds reading the editor selection and the complexity policy an
// editors command projects (HISS-02).
const editorsResolveTimeout = 30 * time.Second

func runEditors(args []string) error {
	if len(args) < 1 {
		printEditorsUsage()
		return nil
	}

	sub := args[0]
	// Matched before any flag parsing (BUG-811): sub is the raw first token, not a Go
	// flag, so "-h"/"--help"/"help" never reaches flag.ErrHelp handling. Without this,
	// `editors --help` fell through to "unknown editors command: --help".
	if isHelpToken(sub) {
		printEditorsUsage()
		return nil
	}
	subArgs := args[1:]
	if sub == "reference" {
		return runEditorsReference(subArgs)
	}

	fs := flag.NewFlagSet("editors "+sub, flag.ContinueOnError)
	path := fs.String("path", ".", "Workspace root directory")
	editorsFlag := fs.String("editors", "",
		"Comma-separated editor ids/aliases to target (default: editors in .standards.yaml, else every supported editor)")
	positional, err := parseInterspersed(fs, subArgs)
	if err != nil {
		return err
	}
	root := positionalAt(positional, 0, *path)
	if sub != "generate" && sub != "verify" {
		return fmt.Errorf("unknown editors command: %s", sub)
	}

	opts, selection, err := resolveEditorsOptions(root, *editorsFlag)
	if err != nil {
		return err
	}
	reportNotApplicableEditors(selection)
	if len(selection.Editors) == 0 {
		return nil
	}
	if sub == "generate" {
		return runEditorsGenerate(opts, root)
	}
	return runEditorsVerify(opts, root)
}

// resolveEditorsOptions builds the synthesis options for root. The editor set is --editors
// when given, else the editors list in root's .standards.yaml, else every supported editor
// (#202).
func resolveEditorsOptions(root, editorsFlag string) (editor.Options, editor.Selection, error) {
	ctx, cancel := commandContext(editorsResolveTimeout)
	defer cancel()
	ids, err := requestedEditors(ctx, root, editorsFlag)
	if err != nil {
		return editor.Options{}, editor.Selection{}, err
	}
	selection, err := editor.SelectEditors(ids)
	if err != nil {
		return editor.Options{}, editor.Selection{}, err
	}
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = root
	opts.Editors = selection.Editors
	// Editor projections must state the ceilings `praetorctl audit` enforces in this very
	// repository. This command never resolved the manifest at all, so it wrote a 75-line
	// function limit into every workspace regardless of policy (issue #360). A policy that
	// cannot be resolved yet, such as the lock `praetorctl init` writes, falls back to the
	// HISS-04 ceiling with a warning rather than failing a command that worked before.
	complexity, warning, err := config.ResolveRepositoryComplexity(ctx, root)
	if err != nil {
		return editor.Options{}, editor.Selection{}, fmt.Errorf("resolve complexity policy for %s: %w", root, err)
	}
	if warning != "" {
		fmt.Printf("[WARN] %s\n", warning)
	}
	opts.Complexity = complexity
	return opts, selection, nil
}

// requestedEditors returns the --editors ids when the flag names any. Otherwise it returns the
// manifest's declaration, nil when the key is absent. An unreadable manifest fails here
// rather than falling back to every editor: that fallback would recreate the files a
// declared selection excludes.
func requestedEditors(ctx context.Context, root, editorsFlag string) ([]string, error) {
	if flagged := splitCommaList(editorsFlag); len(flagged) > 0 {
		return flagged, nil
	}
	declared, err := config.LoadDeclaredTooling(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("read the editors selection of %s (pass --editors to override): %w", root, err)
	}
	return declared.Editors, nil
}

// reportNotApplicableEditors names the supported editors the selection leaves out: their
// files are neither generated nor verified, so one the repository deleted stays deleted.
func reportNotApplicableEditors(selection editor.Selection) {
	if len(selection.NotApplicable) > 0 {
		fmt.Printf("[NOT_APPLICABLE] Editors not selected: %s\n", strings.Join(selection.NotApplicable, ", "))
	}
	if len(selection.Editors) == 0 {
		fmt.Println("[NOT_APPLICABLE] No editor is selected; nothing to generate or verify.")
	}
}

// printEditorsUsage is shared by the no-args and explicit-help paths so the two never
// drift apart (HISS-19).
func printEditorsUsage() {
	fmt.Println("Usage: praetorctl editors <generate|verify> [--path=.] [--editors=a,b]")
	fmt.Println("       praetorctl editors reference [--path=.] [--verify]")
}

// runEditorsReference writes the engine's reference editor integrations under <path>/editors
// (editor.ReferenceSet), or with --verify checks the tracked copies against the generator.
// They are generator output, so a hand edit is drift that `make verify-all` fails on.
func runEditorsReference(args []string) error {
	fs := flag.NewFlagSet("editors reference", flag.ContinueOnError)
	path := fs.String("path", ".", "Engine checkout root that holds editors/")
	verify := fs.Bool("verify", false, "Check the tracked reference files instead of writing them")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	root := positionalAt(positional, 0, *path)
	set := editor.ReferenceSet()
	if *verify {
		report, err := editor.VerifyWithReport(set, root)
		if err != nil {
			return fmt.Errorf("[FAIL] reference editor integrations out of sync; run `praetorctl editors reference`: %w", err)
		}
		fmt.Printf("[PASS] %d reference editor integration file(s) match the generator.\n", len(report.Verified))
		return nil
	}
	report, err := editor.WriteWithReport(set, root)
	if err != nil {
		return fmt.Errorf("failed writing reference editor integrations: %w", err)
	}
	counts := reportEditorFiles(report)
	fmt.Printf("[OK] Reference editor integrations: %d created, %d rewritten, %d already current.\n",
		counts[editor.WriteCreated], counts[editor.WriteRewritten], counts[editor.WritePresent])
	return nil
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
	printPreservedEditorFiles(counts)
	return nil
}

// printPreservedEditorFiles states how many existing human-owned files an editor write left
// unverified. `editors generate` and `harvest onboard` share it, so both say it alike.
func printPreservedEditorFiles(counts map[editor.WriteOutcome]int) {
	if preserved := counts[editor.WritePreserved]; preserved > 0 {
		fmt.Printf("[UNVERIFIED] %d existing human-owned file(s) preserved without verification.\n", preserved)
	}
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
