package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/reviewmerge"
)

// mergeFileLimit bounds the size of each review input.
const mergeFileLimit = contextopt.MaxSourceBytes

// mergeInputLimit caps retained input across all draft and final documents.
const mergeInputLimit = 4 * mergeFileLimit

// modelMergeFlags holds the merge-check command options.
type modelMergeFlags struct {
	drafts repeatedStringFlag
	final  *string
	json   *bool
}

// addModelMergeFlags registers the merge-check options.
func addModelMergeFlags(fs *flag.FlagSet) *modelMergeFlags {
	f := &modelMergeFlags{}
	fs.Var(&f.drafts, "draft", "models merge-check and replay: review draft JSON file (repeatable)")
	f.final = fs.String("final", "", "models merge-check: final JSON")
	f.json = fs.Bool("json", false, "models merge-check and replay: JSON output")
	return f
}

// modelMergeError reports an input or usage error.
func modelMergeError(err error) error {
	message := strconv.Quote(err.Error())
	if _, e := fmt.Fprintln(os.Stderr, message[1:len(message)-1]); e != nil {
		return fmt.Errorf("write error: %w", e)
	}
	return exitStatusError{code: 2}
}

// handleModelsMerge derives the invocation deadline before reading any review files.
func handleModelsMerge(f *modelMergeFlags) error {
	ctx, cancel := commandContext(modelsTimeout)
	defer cancel()
	return handleModelsMergeContext(ctx, f)
}

// handleModelsMergeContext validates bounded inputs and selects the documented exit status.
func handleModelsMergeContext(ctx context.Context, f *modelMergeFlags) error {
	if err := validateMergeInputs(f); err != nil {
		return modelMergeError(err)
	}
	drafts, final, err := loadMergeInputs(ctx, f, mergeInputLimit)
	if err != nil {
		return modelMergeError(err)
	}
	problems := reviewmerge.Validate(drafts, final)
	if err := printMergeProblems(problems, *f.json); err != nil {
		return modelMergeError(err)
	}
	if len(problems) > 0 {
		return exitStatusError{code: 1}
	}
	return nil
}

// validateMergeInputs refuses invalid draft counts and missing final paths before I/O.
func validateMergeInputs(f *modelMergeFlags) error {
	if len(f.drafts) == 0 || len(f.drafts) > reviewmerge.MaxDrafts || *f.final == "" {
		return fmt.Errorf("merge-check requires 1..%d --draft files and --final", reviewmerge.MaxDrafts)
	}
	return nil
}

// loadMergeInputs shares one byte budget across the bounded draft list and the final review.
func loadMergeInputs(ctx context.Context, f *modelMergeFlags, remaining int) ([]reviewmerge.Draft, reviewmerge.Final, error) {
	drafts := make([]reviewmerge.Draft, len(f.drafts))
	var final reviewmerge.Final
	for i, path := range f.drafts {
		if err := readMergeJSON(ctx, path, &drafts[i], &remaining); err != nil {
			return nil, final, err
		}
	}
	if err := readMergeJSON(ctx, *f.final, &final, &remaining); err != nil {
		return nil, final, err
	}
	return drafts, final, nil
}

// printMergeProblems writes violations in the requested format.
func printMergeProblems(problems []reviewmerge.Problem, asJSON bool) error {
	if asJSON {
		if problems == nil {
			problems = []reviewmerge.Problem{}
		}
		return printJSON(problems)
	}
	for _, p := range problems {
		if _, err := fmt.Fprintln(os.Stdout, p.String()); err != nil {
			return fmt.Errorf("write problems: %w", err)
		}
	}
	return nil
}

// parseModelAction preserves help and distinguishes action tokens from flag values.
func parseModelAction(fs *flag.FlagSet, args []string) (string, error) {
	selected := selectedModelAction(fs, args)
	positional, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return "", err
	}
	if err != nil {
		if selected == "merge-check" {
			return "", modelMergeError(err)
		}
		return "", err
	}
	action := positionalAt(positional, 0, "list")
	err = validateMergeAction(fs, action, len(positional))
	if err != nil && action == "replay" {
		return "", wrapActionUsage(action, err)
	}
	if err != nil && (action == "merge-check" || mergeOnlyFlagsSet(fs)) {
		return "", modelMergeError(err)
	}
	return action, err
}

// selectedModelAction uses the shared parser's flag/value boundary rules without parsing twice.
func selectedModelAction(fs *flag.FlagSet, args []string) string {
	if len(args) > maxCLIArgs {
		args = args[:maxCLIArgs]
	}
	end, terminated := flagRunEnd(args, boolFlagNames(fs))
	if terminated {
		end++
	}
	if end >= len(args) {
		return "list"
	}
	return args[end]
}

// mergeOnlyFlagsSet reports explicit use of options reserved for merge-check.
func mergeOnlyFlagsSet(fs *flag.FlagSet) bool {
	return flagWasSet(fs, "draft") || flagWasSet(fs, "final") || flagWasSet(fs, "json")
}

// validateMergeAction checks arguments and flags for the selected action.
func validateMergeAction(fs *flag.FlagSet, action string, count int) error {
	if count > 1 {
		return fmt.Errorf("models accepts one action")
	}
	if err := validateModelRouteFlags(fs, action); err != nil {
		return err
	}
	if action != "merge-check" && flagWasSet(fs, "final") {
		return fmt.Errorf("--final requires models merge-check")
	}
	if action != "merge-check" && action != "replay" && mergeOnlyFlagsSet(fs) {
		return fmt.Errorf("--draft, --final and --json require models merge-check (--draft and --json also models replay)")
	}
	return nil
}
