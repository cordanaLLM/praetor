package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/cordanaLLM/standards/internal/worktree"
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

	mgr := worktree.NewManager(".")

	switch sub {
	case "create":
		return handleWorktreeCreate(ctx, mgr, subArgs)
	case "list":
		return handleWorktreeList(ctx, mgr)
	case "remove":
		return handleWorktreeRemove(ctx, mgr, subArgs)
	case "prune":
		if err := mgr.Prune(ctx); err != nil {
			return fmt.Errorf("failed pruning worktrees: %w", err)
		}
		fmt.Println("[OK] Orphaned worktrees pruned successfully.")
		return nil
	default:
		return fmt.Errorf("unknown worktree subcommand: %s", sub)
	}
}

func printWorktreeUsage() {
	fmt.Println("Usage: standardsctl worktree <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  create <task-id> [base-branch]  Create isolated ephemeral git worktree")
	fmt.Println("  list                           List all active git worktrees")
	fmt.Println("  remove <task-id> [--force]      Remove worktree and ephemeral branch")
	fmt.Println("  prune                          Prune orphaned worktree references")
}

func handleWorktreeCreate(ctx context.Context, mgr *worktree.Manager, subArgs []string) error {
	if len(subArgs) < 1 {
		return fmt.Errorf("task-id required: standardsctl worktree create <task-id> [base-branch]")
	}
	taskID := subArgs[0]
	baseBranch := "main"
	if len(subArgs) > 1 {
		baseBranch = subArgs[1]
	}
	wt, err := mgr.Create(ctx, taskID, baseBranch)
	if err != nil {
		return fmt.Errorf("failed creating worktree: %w", err)
	}
	fmt.Printf("[OK] Ephemeral worktree created:\n  Task ID: %s\n  Branch:  %s\n  Path:    %s\n",
		wt.TaskID, wt.Branch, wt.Path)
	return nil
}

func handleWorktreeList(ctx context.Context, mgr *worktree.Manager) error {
	trees, err := mgr.List(ctx)
	if err != nil {
		return fmt.Errorf("failed listing worktrees: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tHEAD\tSTATUS\tPATH")
	for _, wt := range trees {
		status := "clean"
		if wt.Locked {
			status = "locked: " + wt.LockReason
		} else if wt.Prunable {
			status = "prunable"
		}
		branch := wt.Branch
		if wt.Detached {
			branch = "(detached)"
		}
		head := wt.HEAD
		if len(head) > 8 {
			head = head[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", branch, head, status, wt.Path)
	}
	return w.Flush()
}

func handleWorktreeRemove(ctx context.Context, mgr *worktree.Manager, subArgs []string) error {
	fs := flag.NewFlagSet("worktree remove", flag.ContinueOnError)
	force := fs.Bool("force", false, "Force removal even if uncommitted or locked")
	if err := fs.Parse(subArgs); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("task-id required: standardsctl worktree remove <task-id> [--force]")
	}
	taskID := fs.Arg(0)
	if err := mgr.Remove(ctx, taskID, *force); err != nil {
		return fmt.Errorf("failed removing worktree: %w", err)
	}
	fmt.Printf("[OK] Worktree and branch for task %s removed.\n", taskID)
	return nil
}
