// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package govuln is the Go vulnerability gate: govulncheck at symbol level, judged against the
// repository's OpenVEX document (#778).
//
// govulncheck reports an advisory at up to three levels: a module of it is required, a package of
// it is imported, a vulnerable symbol of it is called. Check runs govulncheck with
// -scan symbol -format json and judges each advisory by its deepest finding:
//
//   - a called symbol fails, and no statement can cover it;
//   - a package or module finding fails unless the OpenVEX document holds a not_affected statement
//     for the advisory, with a justification and an impact statement, reviewed within
//     MaxStatementAge;
//   - a statement justified as component_not_present or vulnerable_code_not_present covers a
//     module finding only: once a package of the advisory is imported, the claim is false.
//
// A scan that did not finish (govulncheck exited non-zero, printed no configuration, or scanned at
// another level) and an OpenVEX document that does not validate are errors, never a verdict: the
// CLI exits 2 on them, 1 on a failing advisory and 0 otherwise. No document at the declared path
// covers nothing, so every present advisory fails. The gate run's security stage
// (internal/gating), the pre-push job adoption writes (internal/adopt) and `praetorctl security
// govuln` all run Check.
package govuln

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	// DefaultScanner is the scanner command Check runs when Options names none: govulncheck from
	// PATH.
	DefaultScanner = "govulncheck"
	// ScanTimeout bounds one govulncheck run (HISS-02). It downloads the module graph and the
	// vulnerability database before it builds the call graph, so it is minutes, not seconds.
	ScanTimeout = 15 * time.Minute
	// MaxStatementAge is how long a not_affected statement covers an advisory after its last
	// review: the review date is the statement's last_updated, else its timestamp, else the
	// document's. It is the 90-day cap the npm audit exceptions carry (scripts/npm_audit_gate.py),
	// so every waiver in the repository is reviewed on the same cadence.
	MaxStatementAge = 90 * 24 * time.Hour
	// maxScannerWords bounds the scanner command a caller passes (HISS-02).
	maxScannerWords = 32
)

var (
	// ErrScanIncomplete reports a govulncheck run that produced no verdict. It is never a pass.
	ErrScanIncomplete = errors.New("the govulncheck scan did not complete")
	// ErrInvalidVEX reports an OpenVEX document, or a manifest naming one, that the gate cannot
	// read. A document the gate cannot read covers nothing and is never ignored.
	ErrInvalidVEX = errors.New("the OpenVEX document is invalid")
)

// Runner runs one command in dir and returns its standard output; a failure's standard error
// travels in the error. util.RunCommand is the production implementation.
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

// Options selects what Check scans and how.
type Options struct {
	// Dir is the root of the Go module to scan. Its manifest names the OpenVEX document
	// (config.RepositoryGoVEXPath).
	Dir string
	// Scanner is the command that starts govulncheck, such as
	// ["go", "tool", "-modfile=tools/go/go.mod", "govulncheck"]; ScanArgs follow it. Empty runs
	// DefaultScanner.
	Scanner []string
	// Run runs the scanner.
	Run Runner
	// Now is the clock statements expire against; nil is time.Now.
	Now func() time.Time
}

// ScanArgs are the arguments Check appends to the scanner command: a symbol-level scan of every
// package of the module, as govulncheck's JSON stream.
func ScanArgs() []string {
	return []string{"-scan", "symbol", "-format", "json", "./..."}
}

// Check runs the scanner in opts.Dir and judges its findings against the repository's OpenVEX
// document. The error wraps ErrScanIncomplete or ErrInvalidVEX when no verdict was reached; a
// report is returned only for a scan that completed.
func Check(ctx context.Context, opts Options) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("govuln: context cannot be nil")
	}
	if opts.Dir == "" || opts.Run == nil {
		return nil, errors.New("govuln: a module directory and a runner are required")
	}
	scanner, err := scannerCommand(opts.Scanner)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	vexPath, err := config.RepositoryGoVEXPath(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %s does not say which document to read: %w", ErrInvalidVEX, config.ManifestFileName, err)
	}
	// The document is read before the scan: a broken one fails in a second, not after minutes.
	vex, err := loadVEX(opts.Dir, vexPath, now())
	if err != nil {
		return nil, err
	}
	scan, err := runScan(ctx, opts.Dir, scanner, opts.Run)
	if err != nil {
		return nil, err
	}
	return judge(scan, vex, now()), nil
}

// scannerCommand returns the scanner command, DefaultScanner when none is given.
func scannerCommand(words []string) ([]string, error) {
	if len(words) == 0 {
		return []string{DefaultScanner}, nil
	}
	if len(words) > maxScannerWords {
		return nil, fmt.Errorf("govuln: the scanner command has %d words, at most %d allowed", len(words), maxScannerWords)
	}
	if slices.Contains(words, "") {
		return nil, errors.New("govuln: the scanner command holds an empty word")
	}
	return words, nil
}

// runScan runs the scanner under ScanTimeout and parses its stream. Any exit but 0 is no
// verdict: govulncheck exits 0 in JSON mode whatever it finds, so a non-zero exit means the scan
// itself failed, even when it printed its configuration first.
func runScan(ctx context.Context, dir string, scanner []string, run Runner) (*scanResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	args := append(slices.Clone(scanner[1:]), ScanArgs()...)
	out, err := run(scanCtx, dir, scanner[0], args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s failed, which is no verdict: %w", ErrScanIncomplete, strings.Join(scanner, " "), err)
	}
	return parseStream(out)
}
