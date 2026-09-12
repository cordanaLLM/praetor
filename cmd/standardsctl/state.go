package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
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
	case "task":
		return runStateTask(subArgs)
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
	fmt.Println("  init [dir|--dir=.]             Initialize .workingdir/ session state structure")
	fmt.Println("  sync [dir|--dir=.] [--log=\"message\"] Synchronize git & working state into STATE.md")
	fmt.Println("  status [dir|--dir=.]           Inspect active session state (read-only; never writes)")
	fmt.Println("  audit [dir|--dir=.]            Audit .workingdir/ for required files and P0 blockers")
	fmt.Println("  task [add|complete|list|archive] Manage active tasks in OPEN.md & BACKLOG.md")
	fmt.Println("  bug [add|list|resolve] [args]  Manage bugs ledger (BUGS.md)")
	fmt.Println("  question [add|list|decide]     Manage user questions and decisions (QUESTIONS.md)")
}

// stateArgs parses a state subcommand argument list. Flags may appear before or after the
// positional arguments (Go's flag package alone stops at the first positional, which is
// what silently dropped `state sync <dir> --log=...`). It returns the value of --dir and
// the remaining positional arguments.
func stateArgs(name string, args []string, extra func(*flag.FlagSet)) (dirFlag string, rest []string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dir := fs.String("dir", "", "Repository directory (defaults to the positional argument, else .)")
	if extra != nil {
		extra(fs)
	}
	if parseErr := fs.Parse(reorderArgs(args, boolFlagNames(fs))); parseErr != nil {
		return "", nil, parseErr
	}
	return *dir, fs.Args(), nil
}

// stateDir picks the repository directory: --dir wins, then the positional argument at
// index, then the current directory.
func stateDir(dirFlag string, rest []string, index int) string {
	if dirFlag != "" {
		return dirFlag
	}
	if len(rest) > index && rest[index] != "" {
		return rest[index]
	}
	return "."
}

func runStateInit(args []string) error {
	dirFlag, rest, err := stateArgs("state init", args, nil)
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)
	if err := state.InitWorkingDir(dir); err != nil {
		return fmt.Errorf("state init failed: %w", err)
	}
	fmt.Printf("Initialized %s/ in %s\n", state.WorkingDirName, dir)
	return nil
}

func runStateSync(args []string) error {
	var logMsg *string
	dirFlag, rest, err := stateArgs("state sync", args, func(fs *flag.FlagSet) {
		logMsg = fs.String("log", "", "Optional log message to append to STATE.md")
	})
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)

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
	dirFlag, rest, err := stateArgs("state status", args, nil)
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)

	if !util.DirExists(filepath.Join(dir, state.WorkingDirName)) {
		return fmt.Errorf("state status: %s/ does not exist in %s (run 'praetorctl state init %s' first)",
			state.WorkingDirName, dir, dir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap, err := inspectState(ctx, dir)
	if err != nil {
		return fmt.Errorf("state status failed: %w", err)
	}

	fmt.Printf("=== Praetor Session State: %s ===\n", filepath.Base(snap.RepoPath))
	fmt.Printf("  Branch:            %s\n", snap.Branch)
	fmt.Printf("  Head SHA:          %s\n", snap.HeadSHA)
	fmt.Printf("  Working Tree:      %v (%d dirty)\n", snap.Clean, snap.DirtyCount)
	fmt.Printf("  Tasks:             %d open, %d completed\n", snap.OpenTasks, snap.CompletedTasks)
	fmt.Printf("  Open Bugs:         %d\n", snap.OpenBugs)
	fmt.Printf("  Pending Questions: %d\n", snap.PendingQs)
	fmt.Printf("  Last Synced:       %s\n", snap.LastUpdated.Format(time.RFC3339))
	return nil
}

// inspectState builds a read-only snapshot of the session ledger. Unlike state.SyncState
// it neither scaffolds .workingdir/ nor appends an entry to STATE.md, so `state status`
// cannot mutate the ledger it reports on.
func inspectState(ctx context.Context, dir string) (*state.StateSnapshot, error) {
	snap := &state.StateSnapshot{RepoPath: dir, LastUpdated: time.Now().UTC()}
	snap.Branch = gitValue(ctx, dir, "(not a git worktree)", "branch", "--show-current")
	snap.HeadSHA = gitValue(ctx, dir, "(unknown)", "rev-parse", "--short", "HEAD")

	statusOut, statusErr := util.RunGit(ctx, dir, "status", "--porcelain")
	snap.Clean = statusErr == nil && strings.TrimSpace(statusOut) == ""
	if statusErr == nil && !snap.Clean {
		snap.DirtyCount = len(strings.Split(strings.TrimSpace(statusOut), "\n"))
	}

	tasks, err := state.ListTasks(dir)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	for _, t := range tasks {
		if t.Completed {
			snap.CompletedTasks++
			continue
		}
		snap.OpenTasks++
	}

	bugs, err := state.ListBugs(dir, "open")
	if err != nil {
		return nil, fmt.Errorf("list bugs: %w", err)
	}
	snap.OpenBugs = len(bugs)

	questions, err := state.ListQuestions(dir, "pending")
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	snap.PendingQs = len(questions)
	return snap, nil
}

// gitValue returns the trimmed output of a read-only git query, or fallback when the
// directory is not a git worktree. `state status` is an inspection command and must still
// report the ledger outside a repository, so the failure is rendered, never discarded.
func gitValue(ctx context.Context, dir, fallback string, args ...string) string {
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil || out == "" {
		return fallback
	}
	return out
}

func runStateAudit(args []string) error {
	dirFlag, rest, err := stateArgs("state audit", args, nil)
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)

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
		dirFlag, rest, err := stateArgs("state bug list", args[1:], nil)
		if err != nil {
			return err
		}
		return runStateBugList(stateDir(dirFlag, rest, 0))
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
	dirFlag, rest, err := stateArgs("state bug resolve", args, nil)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: praetorctl state bug resolve <id> <resolution> [--dir=.]")
	}
	id := rest[0]
	res := rest[1]
	dir := stateDir(dirFlag, rest, 2)
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
		dirFlag, rest, err := stateArgs("state question list", args[1:], nil)
		if err != nil {
			return err
		}
		return runStateQuestionList(stateDir(dirFlag, rest, 0))
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
	dirFlag, rest, err := stateArgs("state question decide", args, nil)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: praetorctl state question decide <id> <decision> [--dir=.]")
	}
	id := rest[0]
	dec := rest[1]
	dir := stateDir(dirFlag, rest, 2)
	if err := state.DecideQuestion(dir, id, dec); err != nil {
		return err
	}
	fmt.Printf("Recorded decision for question %s: %s\n", id, dec)
	return nil
}

func runStateTask(args []string) error {
	if len(args) == 0 {
		return printTaskUsage()
	}

	action := args[0]
	subArgs := args[1:]

	switch action {
	case "add":
		return runTaskAdd(subArgs)
	case "complete", "done":
		return runTaskComplete(subArgs)
	case "list":
		return runTaskList(subArgs)
	case "archive":
		return runTaskArchive(subArgs)
	default:
		return fmt.Errorf("unknown task action: %s", action)
	}
}

func printTaskUsage() error {
	fmt.Println("Usage: praetorctl state task <action> [args]")
	fmt.Println("\nActions:")
	fmt.Println("  add <description> [--dir=.]     Add a pending task to OPEN.md")
	fmt.Println("  complete <index|text> [--dir=.] Mark a task as completed in OPEN.md")
	fmt.Println("  list [--dir=.]                  List all tasks from OPEN.md")
	fmt.Println("  archive [--dir=.]               Move completed tasks to BACKLOG.md")
	return nil
}

func runTaskAdd(args []string) error {
	dirFlag, rest, err := stateArgs("state task add", args, nil)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: praetorctl state task add <description> [--dir=.]")
	}
	desc := rest[0]
	dir := stateDir(dirFlag, rest, 1)
	if err := state.AddTask(dir, desc); err != nil {
		return err
	}
	fmt.Printf("[PASS] Task added to %s: %s\n", filepath.Join(dir, state.WorkingDirName, "OPEN.md"), desc)
	return nil
}

func runTaskComplete(args []string) error {
	dirFlag, rest, err := stateArgs("state task complete", args, nil)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: praetorctl state task complete <index|text> [--dir=.]")
	}
	selector := rest[0]
	dir := stateDir(dirFlag, rest, 1)
	if err := state.CompleteTask(dir, selector); err != nil {
		return err
	}
	fmt.Printf("[PASS] Task marked completed in %s: %s\n", filepath.Join(dir, state.WorkingDirName, "OPEN.md"), selector)
	return nil
}

func runTaskList(args []string) error {
	dirFlag, rest, err := stateArgs("state task list", args, nil)
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)
	tasks, err := state.ListTasks(dir)
	if err != nil {
		return err
	}
	fmt.Printf("=== Tasks in %s: %d total ===\n", filepath.Join(dir, state.WorkingDirName, "OPEN.md"), len(tasks))
	for _, t := range tasks {
		status := "[ ]"
		if t.Completed {
			status = "[x]"
		}
		fmt.Printf("  %d. %s %s\n", t.Index, status, t.Description)
	}
	return nil
}

func runTaskArchive(args []string) error {
	dirFlag, rest, err := stateArgs("state task archive", args, nil)
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)
	count, err := state.ArchiveCompletedTasks(dir, "")
	if err != nil {
		return err
	}
	fmt.Printf("[PASS] Archived %d completed tasks from OPEN.md to BACKLOG.md\n", count)
	return nil
}
