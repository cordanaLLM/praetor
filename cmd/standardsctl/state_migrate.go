package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

// stateMigrateTimeout bounds one `state migrate` run: it digests every legacy file and may
// move many registered worktrees, so it gets more than stateCommandTimeout (HISS-02).
const stateMigrateTimeout = 5 * time.Minute

// runStateMigrate moves a legacy .workingdir2 folder into .workingdir (#1233). It is a dry
// run unless --apply is given, and refuses a differing same-name ledger unless --merge is.
func runStateMigrate(args []string) error {
	var apply, merge *bool
	dirFlag, rest, err := stateArgs("state migrate", args, func(fs *flag.FlagSet) {
		apply = fs.Bool("apply", false, "Carry out the migration; without it the command prints the plan and writes nothing")
		merge = fs.Bool("merge", false, "Merge a same-name ledger that differs (template replaced, OPEN.md/BACKLOG.md appended) instead of refusing it")
	})
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)
	ctx, cancel := context.WithTimeout(context.Background(), stateMigrateTimeout)
	defer cancel()
	report, err := state.MigrateLegacyWorkingDir(ctx, dir, state.LegacyMigrationOptions{Apply: *apply, Merge: *merge})
	printLegacyMigration(report, dir, *apply)
	if err != nil {
		return fmt.Errorf("state migrate failed: %w", err)
	}
	if report.Applied {
		return ignoreStateLedger(ctx, dir, true)
	}
	return nil
}

func printLegacyMigration(report *state.LegacyMigrationReport, dir string, apply bool) {
	if report == nil {
		return
	}
	if !report.LegacyPresent {
		fmt.Printf("migrate: no %s/ in %s, nothing to migrate\n", state.LegacyWorkingDirName, dir)
		return
	}
	mode := "dry run, nothing written; rerun with --apply"
	if apply {
		mode = "apply"
	}
	fmt.Printf("migrate: %s/ -> %s/ in %s (%s)\n", state.LegacyWorkingDirName, state.WorkingDirName, dir, mode)
	printMigrationList("move", report.Moved)
	printMigrationList("duplicate (identical, legacy copy dropped)", report.Duplicates)
	for _, merged := range report.Merged {
		fmt.Printf("  merge     %s (%s; merged file shows %d open, %d completed rows)\n",
			merged.Name, merged.Mode, merged.OpenTasks, merged.CompletedTasks)
	}
	printMigrationList("worktree (git worktree move)", report.Worktrees)
	printMigrationList("conflict", report.Conflicts)
	if len(report.TrackedReferences) > 0 {
		fmt.Printf("  tracked files still naming %s: %s\n", state.LegacyWorkingDirName, strings.Join(report.TrackedReferences, ", "))
	}
	if report.LegacyRemoved {
		fmt.Printf("migrate: %s/ removed; run 'praetorctl state sync %s' to record the ledger\n", state.LegacyWorkingDirName, dir)
	}
}

func printMigrationList(label string, paths []string) {
	for _, path := range paths {
		fmt.Printf("  %-9s %s\n", label, path)
	}
}
