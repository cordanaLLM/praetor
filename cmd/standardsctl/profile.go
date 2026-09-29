package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// profileTimeout bounds one profile change, including the git checks of its backups (HISS-02).
const profileTimeout = 2 * time.Minute

// errProfileIncomplete keeps a profile change that recorded errors from returning success.
var errProfileIncomplete = errors.New("profile change completed with errors")

// profileUsage is the one usage text of the profile command.
const profileUsage = "usage: praetorctl profile set [<profile>] [--facets=<id>,...] --lock-source-root=<praetor checkout> " +
	"[--path=.] [--dry-run]"

// runProfile dispatches `praetorctl profile <action>`; set is the one action.
func runProfile(args []string) error {
	if len(args) == 0 {
		return errors.New(profileUsage)
	}
	switch args[0] {
	case "set":
		return runProfileSet(args[1:])
	case "-h", "--help", "help":
		fmt.Println(profileUsage)
		fmt.Println("\nChanges an adopted repository's declared profile or facets, or re-pins them to a newer Praetor")
		fmt.Println("catalog, rewriting only .standards.yaml, .standards.lock and the vendored catalog texts.")
		return nil
	}
	return fmt.Errorf("unknown profile action %q; %s", args[0], profileUsage)
}

// runProfileSet declares a profile or facets in an adopted repository (adopt.SetProfile) and
// prints what it wrote, or in a dry run what it would write.
func runProfileSet(args []string) error {
	opts, err := parseProfileSetOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(profileTimeout)
	defer cancel()
	report, err := adopt.SetProfile(ctx, opts)
	if report != nil {
		printProfileSetReport(report)
	}
	if err != nil {
		return fmt.Errorf("profile set failed: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%w: %d error(s) listed above", errProfileIncomplete, len(report.Errors))
	}
	return nil
}

// parseProfileSetOptions reads `profile set [<profile>]` and its flags. --facets given, even
// empty, replaces the declared facets; omitted, it keeps them. Neither a profile nor --facets
// re-pins the declaration as it stands.
func parseProfileSetOptions(args []string) (adopt.ProfileSetOptions, error) {
	fs := flag.NewFlagSet("profile set", flag.ContinueOnError)
	facets := fs.String("facets", "", "Comma-separated facets that replace the declared ones; --facets= declares none (omitted: keep the declared facets)")
	lockSource := fs.String("lock-source-root", "", "Praetor source bundle the new pins and vendored catalog texts come from (required)")
	path := fs.String("path", ".", "Adopted repository whose declaration changes")
	dryRun := fs.Bool("dry-run", false, "Preview every change as a diff without writing files")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return adopt.ProfileSetOptions{}, err
	}
	if len(positional) > 1 {
		return adopt.ProfileSetOptions{}, fmt.Errorf("profile set takes at most one profile, got %q; %s", positional, profileUsage)
	}
	opts := adopt.ProfileSetOptions{Path: *path, Profile: positionalAt(positional, 0, ""),
		Facets: splitCommaList(*facets), LockSourceRoot: *lockSource, DryRun: *dryRun}
	fs.Visit(func(set *flag.Flag) {
		if set.Name == "facets" {
			opts.SetFacets = true
		}
	})
	return opts, nil
}

// printProfileSetReport prints the declaration a profile change leaves and each file it wrote,
// verified or would write, with the dry-run diffs, in the sections an adoption report uses.
func printProfileSetReport(rep *adopt.AdoptReport) {
	fmt.Println("=== Praetor Profile Change ===")
	if rep.DryRun {
		fmt.Println("[DRY-RUN SIMULATION: No filesystem mutations performed]")
	}
	if rep.Archetype != "" {
		fmt.Printf("Profile:            %s\n", rep.Archetype)
		fmt.Printf("Facets:             %v\n", rep.Facets)
	}
	printAdoptedFiles(rep)
	printAdoptPreviews(rep.Previews)
	printAdoptIssues(rep)
}
