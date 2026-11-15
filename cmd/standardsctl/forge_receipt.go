// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

type pushedReceiptOptions struct {
	path, receipt, head, tree, scope string
}

// runForgeVerifyReceipt adapts the shared pinned verifier for cached pre-push receipts.
// A signed clean-tree record and commit bind the scan to that commit's immutable tree;
// the caller's tree and the snapshot's cleanliness must agree before anything is reused.
func runForgeVerifyReceipt(ctx context.Context, args []string) error {
	opts, err := parsePushedReceiptOptions(args)
	if err != nil {
		return err
	}
	rf, err := lockdown.LoadReceiptFile(opts.receipt)
	if err != nil {
		return err
	}
	pinned, err := lockdown.PinnedPublicKey(ctx, filepath.Join(opts.path, config.ManifestFileName))
	if err != nil {
		return err
	}
	if err := lockdown.VerifyPinnedReceiptFile(rf, pinned); err != nil {
		return fmt.Errorf("cached gate receipt: %w", err)
	}
	if rf.Command != util.GateReceiptCommand || rf.CommitSHA != opts.head {
		return errors.New("cached receipt does not certify the gate command and exact pushed head")
	}
	if err := lockdown.VerifyReceiptCommit(ctx, opts.path, rf.CommitSHA); err != nil {
		return err
	}
	if err := verifyReceiptSnapshot(ctx, opts.path, opts.tree); err != nil {
		return err
	}
	return verifyReceiptRaceScope(rf.GateOutput, opts.scope)
}

// parsePushedReceiptOptions requires every binding; no flag relaxes verification.
func parsePushedReceiptOptions(args []string) (pushedReceiptOptions, error) {
	var opts pushedReceiptOptions
	fs := flag.NewFlagSet("forge verify-receipt", flag.ContinueOnError)
	fs.StringVar(&opts.path, "path", ".", "Pushed-head snapshot directory")
	fs.StringVar(&opts.receipt, "receipt", "", "Preserved gate receipt file")
	fs.StringVar(&opts.head, "head", "", "Exact pushed commit SHA")
	fs.StringVar(&opts.tree, "tree", "", "Tree SHA of the pushed commit")
	fs.StringVar(&opts.scope, "race-packages", "", "Affected Go import paths, ./..., or none")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return opts, err
	}
	if len(rest) != 0 || opts.receipt == "" || opts.head == "" || opts.tree == "" || opts.scope == "" {
		return opts, errors.New("forge verify-receipt requires --receipt, --head, --tree and --race-packages, with no positional arguments")
	}
	return opts, nil
}

// verifyReceiptSnapshot requires the pushed tree and an unchanged exported snapshot.
func verifyReceiptSnapshot(ctx context.Context, path, expectedTree string) error {
	tree, err := util.RunGitProbe(ctx, path, util.MaxCommandOutputBytes, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return fmt.Errorf("resolve receipt snapshot tree: %w", err)
	}
	if strings.TrimSpace(string(tree.Stdout)) != expectedTree {
		return errors.New("cached receipt snapshot tree differs from the pushed tree")
	}
	return gating.VerifyCleanTree(ctx, path)
}

// verifyReceiptRaceScope requires passed race tests for a nonempty push scope, covered by a full or matching run.
// Pinned verification precedes this reuse-only policy; the shared reader validates certified stage evidence.
func verifyReceiptRaceScope(output, scope string) error {
	packages, err := util.ParseRacePackages(scope)
	if err != nil {
		return fmt.Errorf("receipt reuse race scope: %w", err)
	}
	fullScope := "\n" + lockdown.RacePackagesLine([]string{"./..."}) + "\n"
	if strings.Contains(output, "\nrace_packages\t") && !strings.Contains(output, fullScope) &&
		!strings.Contains(output, "\n"+lockdown.RacePackagesLine(packages)+"\n") {
		return errors.New("cached receipt race scope differs from this push's affected Go packages")
	}
	if len(packages) == 0 {
		return nil
	}
	status, err := lockdown.ReceiptRaceStageStatus(output)
	if err != nil {
		return fmt.Errorf("cached receipt race stage: %w", err)
	}
	if status != "passed" {
		return errors.New("cached receipt must certify passed race tests for this push's affected Go packages")
	}
	return nil
}
