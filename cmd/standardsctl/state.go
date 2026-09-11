package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

func runState(args []string) error {
	if len(args) == 0 {
		printStateUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "init":
		return runStateInit(subArgs)
	case "sync":
		return runStateSync(subArgs)
	case "status":
		return runStateStatus(subArgs)
	case "audit":
		return runStateAudit(subArgs)
	case "bug":
		return runStateBug(subArgs)
	case "question":
		return runStateQuestion(subArgs)
	case "-h", "--help", "help":
		printStateUsage()
		return nil
	default:
		return fmt.Errorf("unknown state subcommand: %s", sub)
	}
}

func printStateUsage() {
	fmt.Println("Usage: praetorctl state <subcommand> [args]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  init [dir]                     Initialize .workingdir/ session state structure")
	fmt.Println("  sync [dir] [--log=\"message\"]     Synchronize git & working state into STATE.md")
	fmt.Println("  status [dir]                   Inspect active session state and pending items")
	fmt.Println("  audit [dir]                    Audit .workingdir/ for required files and P0 blockers")
	fmt.Println("  bug [add|list|resolve] [args]  Manage bugs ledger (BUGS.md)")
	fmt.Println("  question [add|list|decide]     Manage user questions and decisions (QUESTIONS.md)")
}

func runStateInit(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if err := state.InitWorkingDir(dir); err != nil {
		return fmt.Errorf("state init failed: %w", err)
	}
	fmt.Printf("Initialized .workingdir/ in %s\n", dir)
	return nil
}

func runStateSync(args []string) error {
	fs := flag.NewFlagSet("state sync", flag.ContinueOnError)
	logMsg := fs.String("log", "", "Optional log message to append to STATE.md")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := "."
	if len(fs.Args()) > 0 {
		dir = fs.Args()[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap, err := state.SyncState(ctx, dir, *logMsg)
	if err != nil {
		return fmt.Errorf("state sync failed: %w", err)
	}

	statusStr := "clean"
	if !snap.Clean {
		statusStr = fmt.Sprintf("dirty (%d modified)", snap.DirtyCount)
	}
	fmt.Printf("Synchronized state for %s (%s, %s, bugs: %d, questions: %d)\n",
		filepath.Base(snap.RepoPath), snap.Branch, statusStr, snap.OpenBugs, snap.PendingQs)
	return nil
}

func runStateStatus(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap, err := state.SyncState(ctx, dir, "")
	if err != nil {
		return err
	}

	fmt.Printf("=== Praetor Session State: %s ===\n", filepath.Base(snap.RepoPath))
	fmt.Printf("  Branch:            %s\n", snap.Branch)
	fmt.Printf("  Head SHA:          %s\n", snap.HeadSHA)
	fmt.Printf("  Working Tree:      %v (%d dirty)\n", snap.Clean, snap.DirtyCount)
	fmt.Printf("  Open Bugs:         %d\n", snap.OpenBugs)
	fmt.Printf("  Pending Questions: %d\n", snap.PendingQs)
	fmt.Printf("  Last Synced:       %s\n", snap.LastUpdated.Format(time.RFC3339))
	return nil
}

func runStateAudit(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	report, err := state.AuditWorkingDir(dir)
	if err != nil {
		return fmt.Errorf("state audit failed: %w", err)
	}

	fmt.Printf("=== State Audit: %s (Valid: %v) ===\n", dir, report.Valid)
	fmt.Printf("  WorkingDir Exists: %v\n", report.WorkingDirExists)
	fmt.Printf("  Total Bugs:        %d (Open: %d, P0: %d)\n", report.TotalBugs, report.OpenBugs, report.P0Bugs)
	fmt.Printf("  Pending Questions: %d\n", report.PendingQuestions)

	if len(report.MissingFiles) > 0 {
		fmt.Printf("  Missing Files:     %s\n", strings.Join(report.MissingFiles, ", "))
	}
	if len(report.Violations) > 0 {
		fmt.Printf("  Violations (%d):\n", len(report.Violations))
		for _, v := range report.Violations {
			fmt.Printf("    - %s\n", v)
		}
	}
	if !report.Valid {
		return fmt.Errorf("state audit failed with %d violations", len(report.Violations))
	}
	return nil
}

func runStateBug(args []string) error {
	if len(args) == 0 {
		return runStateBugList(".")
	}
	switch args[0] {
	case "add":
		return runStateBugAdd(args[1:])
	case "list":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return runStateBugList(dir)
	case "resolve":
		return runStateBugResolve(args[1:])
	default:
		return fmt.Errorf("unknown bug action: %s", args[0])
	}
}

func runStateBugAdd(args []string) error {
	fs := flag.NewFlagSet("state bug add", flag.ContinueOnError)
	title := fs.String("title", "", "Bug title")
	sev := fs.String("severity", "p2", "Severity (p0, p1, p2, p3)")
	loc := fs.String("location", "core", "File or component location")
	ctxStr := fs.String("context", "", "Additional context")
	dir := fs.String("dir", ".", "Repository directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" {
		return fmt.Errorf("--title is required")
	}

	entry, err := state.AddBug(*dir, state.BugEntry{
		Title:    *title,
		Severity: *sev,
		Location: *loc,
		Context:  *ctxStr,
		Status:   "open",
	})
	if err != nil {
		return err
	}
	fmt.Printf("Added bug [%s] %s (%s, %s)\n", entry.ID, entry.Title, entry.Severity, entry.Location)
	return nil
}

func runStateBugList(dir string) error {
	bugs, err := state.ListBugs(dir, "all")
	if err != nil {
		return err
	}
	fmt.Printf("=== Bug Ledger (%d total) ===\n", len(bugs))
	for _, b := range bugs {
		res := strings.ToUpper(b.Status)
		if b.Resolution != "" {
			res += ": " + b.Resolution
		}
		fmt.Printf("  [%s] %-4s | %-15s | %s [%s]\n", b.ID, b.Severity, b.Location, b.Title, res)
	}
	return nil
}

func runStateBugResolve(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: praetorctl state bug resolve <id> <resolution> [--dir=.]")
	}
	id := args[0]
	res := args[1]
	dir := "."
	if len(args) > 2 {
		dir = args[2]
	}
	if err := state.ResolveBug(dir, id, res); err != nil {
		return err
	}
	fmt.Printf("Resolved bug %s: %s\n", id, res)
	return nil
}

func runStateQuestion(args []string) error {
	if len(args) == 0 {
		return runStateQuestionList(".")
	}
	switch args[0] {
	case "add":
		return runStateQuestionAdd(args[1:])
	case "list":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return runStateQuestionList(dir)
	case "decide":
		return runStateQuestionDecide(args[1:])
	default:
		return fmt.Errorf("unknown question action: %s", args[0])
	}
}

func runStateQuestionAdd(args []string) error {
	fs := flag.NewFlagSet("state question add", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "Question prompt")
	opts := fs.String("options", "", "Comma-separated options")
	ctxStr := fs.String("context", "", "Context for decision")
	dir := fs.String("dir", ".", "Repository directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *prompt == "" {
		return fmt.Errorf("--prompt is required")
	}

	var optList []string
	if *opts != "" {
		for _, o := range strings.Split(*opts, ",") {
			trimmed := strings.TrimSpace(o)
			if trimmed != "" {
				optList = append(optList, trimmed)
			}
		}
	}

	entry, err := state.AddQuestion(*dir, state.QuestionEntry{
		Question: *prompt,
		Options:  optList,
		Context:  *ctxStr,
		Status:   "pending",
	})
	if err != nil {
		return err
	}
	fmt.Printf("Added question [%s] %s\n", entry.ID, entry.Question)
	return nil
}

func runStateQuestionList(dir string) error {
	qs, err := state.ListQuestions(dir, "all")
	if err != nil {
		return err
	}
	fmt.Printf("=== Questions Ledger (%d total) ===\n", len(qs))
	for _, q := range qs {
		status := strings.ToUpper(q.Status)
		if q.SelectedAnswer != "" {
			status += ": " + q.SelectedAnswer
		}
		fmt.Printf("  [%s] %s [%s]\n", q.ID, q.Question, status)
		if len(q.Options) > 0 {
			fmt.Printf("       Options: %s\n", strings.Join(q.Options, " | "))
		}
	}
	return nil
}

func runStateQuestionDecide(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: praetorctl state question decide <id> <decision> [--dir=.]")
	}
	id := args[0]
	dec := args[1]
	dir := "."
	if len(args) > 2 {
		dir = args[2]
	}
	if err := state.DecideQuestion(dir, id, dec); err != nil {
		return err
	}
	fmt.Printf("Recorded decision for question %s: %s\n", id, dec)
	return nil
}
