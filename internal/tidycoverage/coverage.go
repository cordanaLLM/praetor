// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tidycoverage is the clang-tidy translation-unit coverage gate: every tracked C, C++,
// CUDA, HIP or Objective-C++ translation unit must be read by at least one clang-tidy lane the
// manifest declares (config.ClangTidyPolicy), or be excused by a live entry of the manifest's
// exceptions list under config.ExceptionRuleClangTidyCoverage. A lint configuration that never
// sees a file passes over it silently; this gate names each such file instead.
package tidycoverage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Rule is the exceptions-list rule this gate reads.
const Rule = config.ExceptionRuleClangTidyCoverage

// Bounds of the gate's inputs (HISS-02).
const (
	// maxTrackedListingBytes bounds the git ls-files answer: the largest cap a git command
	// accepts (util.RunGitBytes).
	maxTrackedListingBytes = 16 << 20
	// maxTrackedFiles bounds the tracked paths the gate reads.
	maxTrackedFiles = 1 << 20
	// listingTimeout bounds the git ls-files call; its cost grows with the repository.
	listingTimeout = time.Minute
)

// unitSuffixes are the file suffixes of a translation unit clang-tidy reads, compared without
// case: C (.c), C++ (.cc, .cpp, .cxx, .c++, and .cppm module interfaces), CUDA (.cu), HIP (.hip)
// and Objective-C++ (.mm). Headers are read through the units that include them.
var unitSuffixes = []string{".c", ".cc", ".cpp", ".cxx", ".c++", ".cppm", ".cu", ".hip", ".mm"}

// unitLanguages names the languages unitSuffixes cover, for the skip reason.
const unitLanguages = "C, C++, CUDA, HIP or Objective-C++"

// Options are the inputs of one gate run.
type Options struct {
	// Root is the top of the repository's git work tree.
	Root string
	// Policy is the manifest's clang_tidy section; nil declares no lane.
	Policy *config.ClangTidyPolicy
	// Exceptions is the manifest's exceptions list; the gate reads the entries of Rule.
	Exceptions []config.Exception
	// Today is the day expiry is judged against.
	Today time.Time
	// SkipUnbuilt makes a compile_database lane whose database does not exist skip the run,
	// with the lane named, instead of failing it. A compile database is a build output, and
	// the audit runs in hooks before any build; coverage is judged only with every lane read.
	// Every other unreadable lane input still fails. `praetorctl ci tidy-coverage` leaves it
	// false and fails closed.
	SkipUnbuilt bool
}

// Lane is what one declared lane read.
type Lane struct {
	Name   string
	Source string
	// Units counts the tracked translation units the lane reads.
	Units int
}

// Report is the outcome of one gate run.
type Report struct {
	// Skipped is the reason the gate did not run, or "" when it ran.
	Skipped string
	// Units counts the tracked translation units.
	Units int
	// Lanes is what each declared lane read, in declaration order.
	Lanes []Lane
	// Excepted lists the units a live exception excuses.
	Excepted []string
	// Findings lists each failure, one line per file or entry.
	Findings []string
}

// Passed reports whether the gate found nothing to fail on; a skipped run finds nothing.
func (r Report) Passed() bool {
	return len(r.Findings) == 0
}

// Check runs the gate. An error means the gate could not run: a declaration that is invalid,
// a git listing that failed, or a lane whose input cannot be read. A skipped run returns a
// Report whose Skipped names the reason: no unit is tracked, or, with SkipUnbuilt, a lane's
// compile database is not written yet. Findings are in the Report.
func Check(ctx context.Context, opts Options) (Report, error) {
	if err := errors.Join(config.ValidateClangTidy(opts.Policy), config.ValidateExceptions(opts.Exceptions, opts.Today)); err != nil {
		return Report{}, fmt.Errorf("clang-tidy coverage declarations: %w", err)
	}
	units, err := trackedUnits(ctx, opts.Root)
	if err != nil {
		return Report{}, err
	}
	if len(units) == 0 {
		return Report{Skipped: fmt.Sprintf("the repository tracks no %s translation unit (%s)",
			unitLanguages, strings.Join(unitSuffixes, " "))}, nil
	}
	reading, err := readLanes(ctx, opts, units)
	if err != nil {
		return Report{}, err
	}
	if len(reading.unbuilt) > 0 {
		return Report{Skipped: fmt.Sprintf("no compile database written in this checkout for lane %s; "+
			"praetorctl ci tidy-coverage judges coverage where a build writes it", strings.Join(reading.unbuilt, ", "))}, nil
	}
	report := Report{Units: len(units), Lanes: reading.lanes}
	judge(&report, units, reading.read, config.ExceptionsFor(opts.Exceptions, Rule), opts.Today)
	return report, nil
}

// trackedUnits lists the translation units git tracks below root, sorted.
func trackedUnits(ctx context.Context, root string) ([]string, error) {
	result, err := util.RunGitProbeWithin(ctx, root, maxTrackedListingBytes, listingTimeout, "ls-files", "-z", "--cached", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("list the tracked files of %s: %w", root, err)
	}
	entries := bytes.Split(bytes.TrimSuffix(result.Stdout, []byte{0}), []byte{0})
	if len(entries) > maxTrackedFiles {
		return nil, fmt.Errorf("%s tracks more than %d files", root, maxTrackedFiles)
	}
	units := make([]string, 0)
	for index := 0; index < len(entries); index++ {
		if rel := string(entries[index]); isUnit(rel) {
			units = append(units, rel)
		}
	}
	slices.Sort(units)
	return slices.Compact(units), nil
}

// isUnit reports whether the repository path rel names a translation unit by its suffix.
func isUnit(rel string) bool {
	return slices.Contains(unitSuffixes, strings.ToLower(path.Ext(rel)))
}

// judge records, for every unit no lane reads, the live exception that excuses it or the
// finding that names it, and a finding for every entry of the rule that excuses nothing.
func judge(report *Report, units []string, read map[string]bool, entries []config.Exception, today time.Time) {
	used := make([]bool, len(entries))
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if read[unit] {
			continue
		}
		finding := judgeUnit(unit, entries, used, today)
		if finding == "" {
			report.Excepted = append(report.Excepted, unit)
			continue
		}
		report.Findings = append(report.Findings, finding)
	}
	for index := 0; index < len(entries); index++ {
		if !used[index] {
			report.Findings = append(report.Findings, fmt.Sprintf(
				"exceptions entry %s (%s): excuses no translation unit that every lane leaves unread; remove the entry",
				entries[index].Target(), Rule))
		}
	}
}

// judgeUnit returns "" when a live entry excuses unit, and otherwise the finding that names
// it. Every entry that matches unit is marked used, expired or not, so an expired entry is
// reported once, through its unit, rather than also as stale (config.ExceptionFor).
func judgeUnit(unit string, entries []config.Exception, used []bool, today time.Time) string {
	live, expired := config.ExceptionFor(entries, unit, today, used)
	switch {
	case live != nil:
		return ""
	case expired != nil:
		return fmt.Sprintf("%s: read by no clang-tidy lane; its exception expired on %s", unit, expired.Expires)
	default:
		return fmt.Sprintf("%s: read by no clang-tidy lane and named by no exceptions entry", unit)
	}
}
