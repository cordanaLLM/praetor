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
	// govulnIncompleteExit is the exit status of a scan, an OpenVEX document or a command line
	// that gave no verdict: never 0, and distinct from a finding.
	govulnIncompleteExit = 2
)

// runSecurity is `praetorctl security govuln`: the Go vulnerability gate (internal/govuln). It is
// the one implementation the gate run's security stage, the pre-push job adoption writes and
// this repository's security workflow run; the CLI exists so the hook and the workflow call the
// same code the gate stage calls instead of a second script.
func runSecurity(args []string) error {
	return dispatchSecurity(args, util.RunCommand, os.Stdout, os.Stderr)
}

// dispatchSecurity runs the security subcommand args name. A missing or unknown one is a usage
// error, which exits 2 like every command line the gate cannot run (securityUsageError).
func dispatchSecurity(args []string, run govuln.Runner, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "govuln" {
		return securityUsageError(stderr, errors.New("the security command runs one subcommand, govuln"))
	}
	return runSecurityGovuln(args[1:], run, stdout, stderr)
}

// securityUsageError reports a command line the gate cannot run on stderr, with the synopsis, and
// returns exit status 2: no verdict, never 1, which a caller reads as a failing advisory, so a
// misspelled flag in a CI step cannot pass for a vulnerability. flag.ErrHelp is returned as it is:
// the flag set already printed the help, and main exits 0 on it.
func securityUsageError(stderr io.Writer, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return writeWithStatus(stderr, "govuln: no verdict: "+err.Error()+"\n"+securityUsage+"\n", govulnIncompleteExit)
}

// runSecurityGovuln scans the module at --path with the scanner command after "--" (govulncheck
// from PATH when none is given) through run, and reports the verdicts: covered advisories and
// unused statements on stdout, failing advisories on stderr. It exits 1 on a failing advisory and
// 2 when the scan or the OpenVEX document gave no verdict or the command line does not parse.
func runSecurityGovuln(args []string, run govuln.Runner, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("security govuln", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoPath := fs.String("path", ".", "Root of the Go module to scan; its .standards.yaml names the OpenVEX document (security.go_vex)")
	scanner, err := parseInterspersed(fs, args)
	if err != nil {
		return securityUsageError(stderr, err)
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
