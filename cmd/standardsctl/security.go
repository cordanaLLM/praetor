// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// securityUsage is the security command's synopsis.
	securityUsage = "usage: praetorctl security govuln [--path=.] [-- scanner command...]"
	// govulnFindingExit is the exit status of a scan with an advisory that fails the gate.
	govulnFindingExit = 1
	// govulnIncompleteExit is the exit status of a scan or an OpenVEX document that gave no
	// verdict: never 0, and distinct from a finding.
	govulnIncompleteExit = 2
)

// runSecurity is `praetorctl security govuln`: the Go vulnerability gate (internal/govuln). It is
// the one implementation the gate run's security stage, the pre-push job adoption writes and
// this repository's security workflow run; the CLI exists so the hook and the workflow call the
// same code the gate stage calls instead of a second script.
func runSecurity(args []string) error {
	if len(args) == 0 || args[0] != "govuln" {
		return errors.New(securityUsage)
	}
	return runSecurityGovuln(args[1:], util.RunCommand, os.Stdout, os.Stderr)
}

// runSecurityGovuln scans the module at --path with the scanner command after "--" (govulncheck
// from PATH when none is given) through run, and reports the verdicts: covered advisories and
// unused statements on stdout, failing advisories on stderr. It exits 1 on a failing advisory and
// 2 when the scan or the OpenVEX document gave no verdict.
func runSecurityGovuln(args []string, run govuln.Runner, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("security govuln", flag.ContinueOnError)
	repoPath := fs.String("path", ".", "Root of the Go module to scan; its .standards.yaml names the OpenVEX document (security.go_vex)")
	scanner, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	// The scan bounds itself by govuln.ScanTimeout; the margin covers reading the document.
	ctx, cancel := commandContext(govuln.ScanTimeout + time.Minute)
	defer cancel()
	report, err := govuln.Check(ctx, govuln.Options{Dir: *repoPath, Scanner: scanner, Run: run})
	return reportGovuln(report, err, stdout, stderr)
}

// reportGovuln prints one Check outcome and returns the exit status it calls for: covered
// advisories, unused statements and a pass on stdout, failing advisories and the reason for no
// verdict on stderr.
func reportGovuln(report *govuln.Report, err error, stdout, stderr io.Writer) error {
	if err != nil {
		return writeWithStatus(stderr, "govuln: no verdict: "+err.Error()+"\n", govulnIncompleteExit)
	}
	notes := prefixedLines("govuln: ", report.Lines(true)) + prefixedLines("govuln: note: ", report.UnusedLines())
	if !report.Failed() {
		return writeWithStatus(stdout, notes+"govuln: PASS: "+report.Summary()+"\n", 0)
	}
	if err := writeWithStatus(stdout, notes, 0); err != nil {
		return err
	}
	failures := prefixedLines("govuln: ", report.Lines(false))
	return writeWithStatus(stderr, failures+"govuln: FAIL: "+report.Summary()+"\n", govulnFindingExit)
}

// writeWithStatus writes text to out and returns the exit status code as an exitStatusError, nil
// for 0. A failed write keeps the status, which is then the only report left.
func writeWithStatus(out io.Writer, text string, code int) error {
	var status error
	if code != 0 {
		status = exitStatusError{code: code}
	}
	if _, err := io.WriteString(out, text); err != nil {
		return errors.Join(status, fmt.Errorf("write the govuln report: %w", err))
	}
	return status
}

// prefixedLines renders each line behind prefix, newline-terminated.
func prefixedLines(prefix string, lines []string) string {
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		b.WriteString(prefix + lines[i] + "\n")
	}
	return b.String()
}
