// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/docdistill"
)

// docsCommandTimeout bounds the whole docs command (HISS-02).
const docsCommandTimeout = 10 * time.Minute

func runDocs(args []string) error {
	if len(args) == 0 {
		printDocsUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	// HISS-02: docs sync fetches one documentation sheet per declared dependency, so the
	// command carries an explicit deadline like its sibling commands.
	ctx, cancel := context.WithTimeout(context.Background(), docsCommandTimeout)
	defer cancel()

	switch sub {
	case "sync":
		return runDocsSync(ctx, subArgs)
	case "audit":
		return runDocsAudit(ctx, subArgs)
	case "lookup":
		return runDocsLookup(subArgs)
	case "-h", "--help", "help":
		printDocsUsage()
		return nil
	default:
		printDocsUsage()
		return fmt.Errorf("unknown docs subcommand: %s", sub)
	}
}

func printDocsUsage() {
	fmt.Println("Usage: standardsctl docs <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  sync [path] [--force] [--offline]   Harvest and compress docs for declared dependencies")
	fmt.Println("  audit [path]                        Audit documentation coverage for declared packages")
	fmt.Println("  lookup <package> [path]             Retrieve and print distilled documentation")
}

func runDocsSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docs sync", flag.ContinueOnError)
	force := fs.Bool("force", false, "Force re-harvesting of all documentation")
	offline := fs.Bool("offline", false, "Run in offline mode using local doc caches only")
	transitive := fs.Bool("transitive", false, "Include transitive dependencies")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	repoPath := positionalAt(positional, 0, ".")

	opts := docdistill.DefaultDistillOptions()
	opts.ForceRefresh = *force
	opts.OfflineOnly = *offline
	opts.IncludeTransitive = *transitive

	fmt.Printf("=== Syncing Distilled Documentation: %s ===\n", repoPath)
	cat, err := docdistill.SyncRepositoryDocs(ctx, repoPath, opts)
	if err != nil {
		return err
	}

	fmt.Printf("Successfully synchronized %d package documentation sheets into .workingdir/docs/distilled/.\n", len(cat.Packages))
	for _, doc := range cat.Packages {
		fmt.Printf("  - [%s] %s@%s (%d tokens)\n", doc.Kind, doc.PackageName, doc.Version, doc.TokenCount)
	}
	return nil
}

func runDocsAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docs audit", flag.ContinueOnError)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("docs audit accepts at most one repository path, got %d", len(positional))
	}
	repoPath := positionalAt(positional, 0, ".")

	result, err := docdistill.AuditDocumentationCoverage(ctx, repoPath)
	if err != nil {
		return err
	}

	fmt.Printf("=== Documentation Coverage Audit: %s ===\n", repoPath)
	if result.Status == "not_applicable" {
		fmt.Println("  Coverage:   not applicable (no declared dependencies)")
	} else {
		fmt.Printf("  Coverage:   %.1f%%\n", result.CoverageScore)
	}
	fmt.Printf("  Documented: %d / %d declared packages\n", result.Documented, result.TotalDeclared)
	fmt.Printf("  Passed:     %t\n", result.Passed)
	fmt.Printf("  Status:     %s\n", result.Status)

	if len(result.Missing) > 0 {
		fmt.Println("\nMissing Distilled Documentation:")
		for _, m := range result.Missing {
			fmt.Printf("  - %s@%s (%s in %s)\n", m.Name, m.Version, m.Kind, m.Manifest)
		}
		fmt.Println("\nRun 'standardsctl docs sync' to harvest missing documentation.")
		return fmt.Errorf("documentation audit failed: %d missing packages", len(result.Missing))
	}

	if !result.Passed {
		return fmt.Errorf("documentation audit did not pass (status: %s)", result.Status)
	}
	return nil
}

func runDocsLookup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: standardsctl docs lookup <package> [path]")
	}
	pkg := args[0]
	repoPath := "."
	if len(args) > 1 {
		repoPath = args[1]
	}

	cat, err := docdistill.LoadCatalog(repoPath)
	if err != nil {
		return err
	}

	for _, doc := range cat.Packages {
		if doc.PackageName == pkg {
			fmt.Println(doc.RawMarkdown)
			return nil
		}
	}

	// Fallback to searching distilled files
	fmt.Fprintf(os.Stderr, "Package '%s' not found in catalog. Run 'standardsctl docs sync' to harvest.\n", pkg)
	return fmt.Errorf("package not found: %s", pkg)
}
