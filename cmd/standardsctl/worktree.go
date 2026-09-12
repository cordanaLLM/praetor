package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/cordanaLLM/praetor/internal/worktree"
)

func runWorktree(args []string) error {
	if len(args) < 1 {
		printWorktreeUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printWorktreeUsage()
		return nil
	case "create":
		return handleWorktreeCreate(ctx, subArgs)
	case "list":
		return handleWorktreeList(ctx, subArgs)
	case "remove":
		return handleWorktreeRemove(ctx, subArgs)
	case "prune":
		return handleWorktreePrune(ctx, subArgs)
	default:
		return fmt.Errorf("unknown worktree subcommand: %s", sub)
	}
}

func printWorktreeUsage() {
	fmt.Println("Usage: standardsctl worktree <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  create <task-id> [base-branch] [--path=.] Create isolated ephemeral git worktree")
	fmt.Println("  list [--path=.]                 List all active git worktrees")
	fmt.Println("  remove <task-id> [--force] [--path=.] Remove worktree and ephemeral branch")
	fmt.Println("  prune [--path=.]                Prune orphaned worktree references")
}

// worktreeManager parses the repository path shared by every worktree subcommand and
// returns a manager bound to it, together with the remaining positional arguments. The
// --path flag is honoured by every subcommand instead of being silently ignored.
func worktreeManager(name string, args []string, extra func(*flag.FlagSet)) (*worktree.Manager, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("path", ".", "Repository path owning the worktrees")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(reorderArgs(args, boolFlagNames(fs))); err != nil {
		return nil, nil, err
	}
	return worktree.NewManager(*path), fs.Args(), nil
}

func handleWorktreeCreate(ctx context.Context, subArgs []string) error {
	mgr, rest, err := worktreeManager("worktree create", subArgs, nil)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("task-id required: standardsctl worktree create <task-id> [base-branch]")
	}
	taskID := rest[0]
	baseBranch := "main"
	if len(rest) > 1 {
		baseBranch = rest[1]
	}
	wt, err := mgr.Create(ctx, taskID, baseBranch)
	if err != nil {
		return fmt.Errorf("failed creating worktree: %w", err)
	}
	fmt.Printf("[OK] Ephemeral worktree created:\n  Task ID: %s\n  Branch:  %s\n  Path:    %s\n",
		wt.TaskID, wt.Branch, wt.Path)
	return nil
}

func handleWorktreeList(ctx context.Context, subArgs []string) error {
	mgr, rest, err := worktreeManager("worktree list", subArgs, nil)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("worktree list takes no positional arguments, got %q", rest[0])
	}
	trees, err := mgr.List(ctx)
	if err != nil {
		return fmt.Errorf("failed listing worktrees: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(w, "BRANCH\tHEAD\tSTATUS\tPATH"); err != nil {
		return fmt.Errorf("write worktree table header: %w", err)
	}
	for _, wt := range trees {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", worktreeBranch(wt), shortHead(wt.HEAD), worktreeStatus(wt), wt.Path); err != nil {
			return fmt.Errorf("write worktree row %q: %w", wt.Path, err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush worktree table: %w", err)
	}
	return nil
}

// worktreeStatus renders the lock and prune state of one worktree.
func worktreeStatus(wt worktree.WorktreeInfo) string {
	if wt.Locked {
		return "locked: " + wt.LockReason
	}
	if wt.Prunable {
		return "prunable"
	}
	return "clean"
}

// worktreeBranch renders the branch column, marking detached heads.
func worktreeBranch(wt worktree.WorktreeInfo) string {
	if wt.Detached {
		return "(detached)"
	}
	return wt.Branch
}

// shortHead truncates a commit SHA to its display length.
func shortHead(head string) string {
	if len(head) > 8 {
		return head[:8]
	}
	return head
}

func handleWorktreeRemove(ctx context.Context, subArgs []string) error {
	var force *bool
	mgr, rest, err := worktreeManager("worktree remove", subArgs, func(fs *flag.FlagSet) {
		force = fs.Bool("force", false, "Force removal even if uncommitted or locked")
	})
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("task-id required: standardsctl worktree remove <task-id> [--force]")
	}
	taskID := rest[0]
	if err := mgr.Remove(ctx, taskID, *force); err != nil {
		return fmt.Errorf("failed removing worktree: %w", err)
	}
	fmt.Printf("[OK] Worktree and branch for task %s removed.\n", taskID)
	return nil
}

func handleWorktreePrune(ctx context.Context, subArgs []string) error {
	mgr, rest, err := worktreeManager("worktree prune", subArgs, nil)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("worktree prune takes no positional arguments, got %q", rest[0])
	}
	if err := mgr.Prune(ctx); err != nil {
		return fmt.Errorf("failed pruning worktrees: %w", err)
	}
	fmt.Println("[OK] Orphaned worktrees pruned successfully.")
	return nil
}
