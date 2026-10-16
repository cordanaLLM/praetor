// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/generated"
)

// exceptionsGate names the step on every line it prints.
const exceptionsGate = "Exceptions"

// auditExceptionsExpiry warns when any declared exception in the manifest expires within
// config.ExceptionExpiryWarningDays (14 days) of today (Issue #798), and fails every entry whose
// path or glob names no file in the repository at rootDir, the same way a gate fails an expired
// entry (#1259): a stale entry would otherwise outlive the file it was written for.
func auditExceptionsExpiry(ctx context.Context, manifest *config.Manifest, rootDir string, today time.Time) error {
	if manifest == nil {
		return nil
	}
	warnExceptionsExpiring(manifest.Exceptions, today)
	return auditExceptionTargets(ctx, manifest.Exceptions, rootDir, today)
}

// warnExceptionsExpiring prints a warning for each entry expiring within
// config.ExceptionExpiryWarningDays.
func warnExceptionsExpiring(entries []config.Exception, today time.Time) {
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		entry := entries[index]
		if entry.ExpiringWithin(today, config.ExceptionExpiryWarningDays) {
			daysLeft, err := entry.DaysLeft(today)
			if err != nil {
				continue
			}
			fmt.Printf("[WARN] Exceptions: the exceptions entry (rule %s, %s) expires on %s (%s): %s\n",
				entry.Rule, entry.Target(), entry.Expires, config.FormatDaysLeft(daysLeft), entry.Reason)
		}
	}
}

// exceptionTargets reads, once each and only when an entry needs it, what the existence check
// compares against: the repository's files for glob entries and the release workflow a HISS-11
// entry may name before it exists.
type exceptionTargets struct {
	ctx           context.Context
	root          string
	files         []string
	listed        bool
	planned       string
	plannedLoaded bool
}

// auditExceptionTargets fails every path entry whose file does not exist below rootDir and every
// glob entry that matches no repository file, naming the entry, the missing target and, when the
// entry has also expired, its expiry. A tool entry names no file and is not checked. The one
// planned file is a live HISS-11 entry naming the release workflow adoption records while no
// workflow writes provenance (adopt.PlannedReleaseWorkflow): it is not stale, so a live one
// passes with a line saying so and an expired one is left to the HISS-11 gate, which fails it by
// its expiry. Once a workflow writes provenance it fails like any stale entry.
func auditExceptionTargets(ctx context.Context, entries []config.Exception, rootDir string, today time.Time) error {
	targets := &exceptionTargets{ctx: ctx, root: rootDir}
	var failures []string
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		line, err := targets.judge(entries[index], today)
		if err != nil {
			return fmt.Errorf("[FAIL] %s: %w", exceptionsGate, err)
		}
		if line != "" {
			failures = append(failures, line)
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

// judge returns the [FAIL] line for an entry naming no file, or "" for one that names a file,
// a planned file or no file at all (a tool entry).
func (t *exceptionTargets) judge(entry config.Exception, today time.Time) (string, error) {
	found, err := t.names(entry)
	if err != nil || found {
		return "", err
	}
	expired := entry.Expired(today)
	if entry.Rule == config.ExceptionRuleSupplyChain {
		planned, err := t.plannedReleaseWorkflow()
		if err != nil {
			return "", err
		}
		if planned != "" && entry.Path == planned {
			// A planned file is not stale; its expiry stays the HISS-11 gate's to judge.
			if !expired {
				fmt.Printf("[INFO] %s: the exceptions entry (rule %s, %s) names a planned file, not stale: the release "+
					"workflow still to be added while no workflow writes provenance; it holds until %s.\n",
					exceptionsGate, entry.Rule, entry.Path, entry.Expires)
			}
			return "", nil
		}
	}
	return missingTargetEntry(entry, expired), nil
}

// names reports whether the entry names an existing file: its path exists below the root, or its
// glob matches a repository file. A tool entry reports true, since it names no file to check.
func (t *exceptionTargets) names(entry config.Exception) (bool, error) {
	switch {
	case entry.Path != "":
		_, err := os.Lstat(filepath.Join(t.root, filepath.FromSlash(entry.Path)))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("check the path of the exceptions entry (rule %s, %s): %w", entry.Rule, entry.Path, err)
		}
		return true, nil
	case entry.Glob != "":
		files, err := t.repositoryFiles()
		if err != nil {
			return false, err
		}
		return entry.MatchesAny(files), nil
	}
	return true, nil
}

// repositoryFiles lists the repository's files once, through the same git listing the generated
// artefact checks read (generated.DirTree).
func (t *exceptionTargets) repositoryFiles() ([]string, error) {
	if !t.listed {
		files, err := generated.DirTree(t.root).Files(t.ctx)
		if err != nil {
			return nil, fmt.Errorf("list the repository files a glob entry is matched against: %w", err)
		}
		t.files, t.listed = files, true
	}
	return t.files, nil
}

// plannedReleaseWorkflow asks adoption, once, which release workflow a HISS-11 entry may name
// before it exists ("" once a workflow writes provenance).
func (t *exceptionTargets) plannedReleaseWorkflow() (string, error) {
	if !t.plannedLoaded {
		planned, err := adopt.PlannedReleaseWorkflow(t.ctx, t.root)
		if err != nil {
			return "", err
		}
		t.planned, t.plannedLoaded = planned, true
	}
	return t.planned, nil
}

// missingTargetEntry is the [FAIL] line for an entry whose path or glob names no file, with the
// expiry when the entry has also expired.
func missingTargetEntry(entry config.Exception, expired bool) string {
	kind := "path"
	if entry.Path == "" {
		kind = "glob"
	}
	line := fmt.Sprintf("[FAIL] %s: the exceptions entry (rule %s, %s) names the %s %s, which matches no file in the "+
		"repository, so it excuses nothing; remove the entry, or restore the file it was written for.",
		exceptionsGate, entry.Rule, entry.Target(), kind, entry.Target())
	if expired {
		line += fmt.Sprintf(" The entry also expired on %s.", entry.Expires)
	}
	return line
}
