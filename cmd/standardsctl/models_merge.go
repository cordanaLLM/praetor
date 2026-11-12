package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/cordanaLLM/praetor/internal/reviewmerge"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const mergeFileLimit = 4 << 20

type modelMergeFlags struct {
	drafts repeatedStringFlag
	final  *string
	json   *bool
}

func addModelMergeFlags(fs *flag.FlagSet) *modelMergeFlags {
	f := &modelMergeFlags{}
	fs.Var(&f.drafts, "draft", "models merge-check: draft JSON (repeatable)")
	f.final = fs.String("final", "", "models merge-check: final JSON")
	f.json = fs.Bool("json", false, "models merge-check: JSON output")
	return f
}
func modelMergeError(err error) error {
	if _, e := fmt.Fprintln(os.Stderr, err); e != nil {
		return fmt.Errorf("write error: %w", e)
	}
	return exitStatusError{code: 2}
}
func readMergeJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, mergeFileLimit+1))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", path, closeErr)
	}
	if err := strictjson.Decode(data, target, strictjson.Options{MaxBytes: mergeFileLimit, MaxDepth: 16}); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
func handleModelsMerge(f *modelMergeFlags) error {
	if len(f.drafts) == 0 || len(f.drafts) > reviewmerge.MaxDrafts || *f.final == "" {
		return modelMergeError(fmt.Errorf("merge-check requires 1..%d --draft files and --final", reviewmerge.MaxDrafts))
	}
	drafts := make([]reviewmerge.Draft, len(f.drafts))
	for i, path := range f.drafts {
		if err := readMergeJSON(path, &drafts[i]); err != nil {
			return modelMergeError(err)
		}
	}
	var final reviewmerge.Final
	if err := readMergeJSON(*f.final, &final); err != nil {
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

func parseModelAction(fs *flag.FlagSet, args []string) (string, error) {
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		if slices.Contains(args, "merge-check") {
			return "", modelMergeError(err)
		}
		return "", err
	}
	action := positionalAt(positional, 0, "list")
	err = validateMergeAction(fs, action, len(positional))
	if err != nil && action == "merge-check" {
		return "", modelMergeError(err)
	}
	return action, err
}
func validateMergeAction(fs *flag.FlagSet, action string, count int) error {
	if count > 1 {
		return fmt.Errorf("models accepts one action")
	}
	if err := validateModelRouteFlags(fs, action); err != nil {
		return err
	}
	if action != "merge-check" && (flagWasSet(fs, "draft") || flagWasSet(fs, "final") || flagWasSet(fs, "json")) {
		return fmt.Errorf("--draft, --final and --json require models merge-check")
	}
	return nil
}
