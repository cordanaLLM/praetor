// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/mergecheck"
)

// mergeCheckTimeout bounds the entire merge-check command (HISS-02).
const mergeCheckTimeout = 2 * time.Minute

// runMergeCheck executes praetorctl merge-check [--range <a>..<b>].
// Order: engine-dropped-hunk-gate-20261027.
func runMergeCheck(args []string) error {
	fs := flag.NewFlagSet("merge-check", flag.ContinueOnError)
	commitRange := fs.String("range", "", "Commit range to inspect (default origin default branch..HEAD)")
	repoDir := fs.String("dir", ".", "Repository root directory")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 && *commitRange == "" {
		*commitRange = rest[0]
	} else if len(rest) > 0 {
		return fmt.Errorf("merge-check accepts at most one range argument, got %q", rest)
	}

	ctx, cancel := context.WithTimeout(context.Background(), mergeCheckTimeout)
	defer cancel()

	opts := mergecheck.Options{
		RepoDir: *repoDir,
		Range:   *commitRange,
	}
	res, err := mergecheck.Check(ctx, opts)
	if err != nil {
		return fmt.Errorf("merge-check: %w", err)
	}

	return emitMergeCheckResult(res)
}

func emitMergeCheckResult(res *mergecheck.Result) error {
	if res.MergesCount == 0 {
		fmt.Printf("merge-check: no merge commits in range %s\n", res.Range)
		return nil
	}
	if len(res.Findings) == 0 {
		fmt.Printf("[PASS] merge-check: %d merge commit(s) verified in %s: clean\n", res.MergesCount, res.Range)
		return nil
	}
	for i := 0; i < len(res.Findings) && i < mergecheck.DefaultMaxMerges; i++ {
		fmt.Println(mergecheck.FormatFinding(res.Findings[i]))
	}
	fmt.Fprintln(os.Stderr, "Fix: resolve inside conflict markers, re-merge.")
	return exitStatusError{code: 1}
}
