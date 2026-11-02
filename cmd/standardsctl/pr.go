package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/prwait"
)

const prUsage = "usage: praetorctl pr wait --repo owner/name [--pr N ...] [--timeout 50m] [--interval 60s] [--json]"

// runPR dispatches pr subcommands using the default GitHub review reader.
func runPR(args []string) error {
	return runPRWithReader(args, forge.GHReviewReader{}, os.Stdout)
}

// runPRWithReader executes the pr wait subcommand with the provided reader and writer.
func runPRWithReader(args []string, reader forge.ReviewReader, output io.Writer) error {
	if len(args) > 0 && isHelpToken(args[0]) {
		if _, err := fmt.Fprintln(output, prUsage); err != nil {
			return fmt.Errorf("write pr usage: %w", err)
		}
		return flag.ErrHelp
	}
	if len(args) == 0 || args[0] != "wait" {
		return writePRWaitResult(output, prWaitJSONRequested(args), prwait.Result{Code: 2, Items: []string{prUsage}})
	}
	fs := flag.NewFlagSet("pr wait", flag.ContinueOnError)
	options := prwait.Options{}
	fs.StringVar(&options.Repository, "repo", "", "Repository owner/name")
	fs.DurationVar(&options.Timeout, "timeout", 50*time.Minute, "Overall wait timeout")
	fs.DurationVar(&options.Interval, "interval", time.Minute, "Polling interval")
	jsonOutput := fs.Bool("json", false, "Emit exit_code and items as JSON")
	fs.Func("pr", "Pull request number (repeatable; default: own open PRs)", func(raw string) error {
		number, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("parse --pr: %w", err)
		}
		options.Numbers = append(options.Numbers, number)
		return nil
	})
	rest, err := parseInterspersed(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return writePRWaitResult(output, prWaitJSONRequested(args), prwait.Result{Code: 2, Items: []string{err.Error()}})
	}
	if len(rest) > 0 {
		return writePRWaitResult(output, *jsonOutput, prwait.Result{Code: 2, Items: []string{"pr wait accepts no positional arguments"}})
	}
	return writePRWaitResult(output, *jsonOutput, prwait.Wait(rootContext(), reader, options))
}

// prWaitValueFlags names the flags of pr wait that consume a separate value argument.
var prWaitValueFlags = map[string]bool{
	"repo":     true,
	"timeout":  true,
	"interval": true,
	"pr":       true,
}

// isPRWaitValueFlag reports whether flag name accepts a value and is not boolean.
func isPRWaitValueFlag(name string) bool {
	return prWaitValueFlags[name]
}

// parseJSONFlagValue extracts a boolean value from a --json=... argument if present.
func parseJSONFlagValue(arg string) (bool, bool) {
	for _, prefix := range []string{"--json=", "-json="} {
		if strings.HasPrefix(arg, prefix) {
			if parsed, err := strconv.ParseBool(strings.TrimPrefix(arg, prefix)); err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

// prWaitJSONRequested inspects command-line arguments to determine whether JSON output was requested.
func prWaitJSONRequested(args []string) bool {
	requested := false
	for i := 0; i < len(args) && i < maxCLIArgs; i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "-json" {
			requested = true
			continue
		}
		if parsed, ok := parseJSONFlagValue(arg); ok {
			requested = parsed
			continue
		}
		if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
			if isPRWaitValueFlag(strings.TrimLeft(arg, "-")) {
				i++
			}
		}
	}
	return requested
}

// writePRWaitResult emits the wait result in JSON or line-delimited plain text format.
func writePRWaitResult(output io.Writer, asJSON bool, result prwait.Result) error {
	if asJSON {
		if err := json.NewEncoder(output).Encode(result); err != nil {
			return fmt.Errorf("write pr wait JSON: %w", err)
		}
	} else {
		for _, item := range result.Items {
			if _, err := fmt.Fprintln(output, item); err != nil {
				return fmt.Errorf("write pr wait item: %w", err)
			}
		}
	}
	if result.Code != 0 {
		return exitStatusError{code: result.Code}
	}
	return nil
}
