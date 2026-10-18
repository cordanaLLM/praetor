// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/secretscan"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

const (
	// securityUsage is the security command's synopsis.
	securityUsage = "usage: praetorctl security <govuln|secrets> [--path=.] [-- scanner command...]"
	// govulnFindingExit is the exit status of a scan with an advisory that fails the gate.
	govulnFindingExit = 1
	// govulnIncompleteExit is the exit status of a scan, an OpenVEX document or a command line
	// that gave no verdict: never 0, and distinct from a finding.
	govulnIncompleteExit = 2
	// secretsFindingExit is the exit status of a secret scan finding that is not excused.
	secretsFindingExit = 3
	// secretsIncompleteExit is the exit status of an incomplete secret scan or invalid options.
	secretsIncompleteExit = 2
)

// runSecurity is `praetorctl security`: the security gates (govuln, secrets).
func runSecurity(args []string) error {
	return dispatchSecurity(args, util.RunCommand, os.Stdout, os.Stderr)
}

// dispatchSecurity runs the security subcommand args name. A missing or unknown one is a usage
// error, which exits 2 like every command line the gate cannot run (securityUsageError).
func dispatchSecurity(args []string, run govuln.Runner, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return securityUsageError(stderr, errors.New("the security command runs govuln or secrets"))
	}
	switch args[0] {
	case "govuln":
		return runSecurityGovuln(args[1:], run, stdout, stderr)
	case "secrets":
		return runSecuritySecrets(args[1:], run, stdout, stderr)
	default:
		return securityUsageError(stderr, fmt.Errorf("unknown security subcommand %q", args[0]))
	}
}

// securityUsageError reports a command line the gate cannot run on stderr, with the synopsis, and
// returns exit status 2: no verdict, never 1, which a caller reads as a failing advisory, so a
// misspelled flag in a CI step cannot pass for a vulnerability. flag.ErrHelp is returned as it is:
// the flag set already printed the help, and main exits 0 on it.
func securityUsageError(stderr io.Writer, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return writeWithStatus(stderr, "security: no verdict: "+err.Error()+"\n"+securityUsage+"\n", govulnIncompleteExit)
}

// runSecuritySecrets scans history with gitleaks, reading merge commits and judging findings against
// HISS-12 exceptions. It exits 3 on an unexcused finding and 2 on an incomplete scan.
func runSecuritySecrets(args []string, run govuln.Runner, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("security secrets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoPath := fs.String("path", ".", "Root of the repository to scan; its .standards.yaml names the exceptions")
	configPath := fs.String("config", "", "Path to .gitleaks.toml configuration (default is <path>/.gitleaks.toml)")
	scanner, err := parseInterspersed(fs, args)
	if err != nil {
		return securitySecretsUsageError(stderr, err)
	}
	ctx, cancel := commandContext(secretscan.ScanTimeout + time.Minute)
	defer cancel()
	report, err := secretscan.Check(ctx, secretscan.Options{
		Dir:        *repoPath,
		ConfigPath: *configPath,
		Scanner:    scanner,
		Run:        secretscan.Runner(run),
	})
	if err != nil {
		return writeWithStatus(stderr, fmt.Sprintf("secrets: no verdict: %v\n", err), secretsIncompleteExit)
	}
	return reportSecrets(report, stdout, stderr)
}

func securitySecretsUsageError(stderr io.Writer, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return writeWithStatus(stderr, "secrets: no verdict: "+err.Error()+"\n"+securityUsage+"\n", secretsIncompleteExit)
}

func reportSecrets(report *secretscan.Report, stdout, stderr io.Writer) error {
	for i := 0; i < len(report.Allowed); i++ {
		f := report.Allowed[i]
		msg := fmt.Sprintf("secrets: allowed: commit %s: %s:%d [%s] (%s)\n", f.Commit, f.File, f.StartLine, f.RuleID, f.Fingerprint)
		if writeErr := writeWithStatus(stdout, msg, 0); writeErr != nil {
			return writeErr
		}
	}
	if report.Failed() {
		for i := 0; i < len(report.Blocking); i++ {
			f := report.Blocking[i]
			msg := fmt.Sprintf("secrets: finding: commit %s: %s:%d [%s] (%s)\n", f.Commit, f.File, f.StartLine, f.RuleID, f.Fingerprint)
			if writeErr := writeWithStatus(stderr, msg, 0); writeErr != nil {
				return writeErr
			}
		}
		for i := 0; i < len(report.Unused); i++ {
			e := report.Unused[i]
			msg := fmt.Sprintf("secrets: exceptions entry (rule %s, %s, tool %s): excuses no finding: remove it\n", e.Rule, e.Path, e.Tool)
			if writeErr := writeWithStatus(stderr, msg, 0); writeErr != nil {
				return writeErr
			}
		}
		msg := fmt.Sprintf("secrets: FAIL: %s\n", report.Summary())
		return writeWithStatus(stderr, msg, secretsFindingExit)
	}
	msg := fmt.Sprintf("secrets: PASS: %s\n", report.Summary())
	return writeWithStatus(stdout, msg, 0)
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
	modules, err := securityModules(ctx, *repoPath)
	if err != nil {
		return writeWithStatus(stderr, "govuln: no verdict: "+err.Error()+"\n", govulnIncompleteExit)
	}
	if len(modules) == 0 {
		return writeWithStatus(stdout, "govuln: no tracked Go modules, skipping\n", 0)
	}
	return reportGovulnModules(ctx, *repoPath, modules, scanner, run, stdout, stderr)
}

func securityModules(ctx context.Context, repoPath string) ([]string, error) {
	modules, err := apiassets.TrackedModuleFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if len(modules) > apiassets.MaxTrackedModules {
		return nil, fmt.Errorf("%w: found %d, maximum %d", apiassets.ErrTooManyModules,
			len(modules), apiassets.MaxTrackedModules)
	}
	return modules, nil
}

func reportGovulnModules(ctx context.Context, root string, modules []string, scanner []string,
	run govuln.Runner, stdout, stderr io.Writer) error {
	status := 0
	for index := 0; index < len(modules) && index < apiassets.MaxTrackedModules; index++ {
		code, err := reportGovulnModule(ctx, root, modules[index], scanner, run, stdout, stderr)
		if err != nil {
			return err
		}
		status = max(status, code)
	}
	return statusError(status)
}

func reportGovulnModule(ctx context.Context, root, module string, scanner []string,
	run govuln.Runner, stdout, stderr io.Writer) (int, error) {
	start := time.Now()
	dir := filepath.Join(root, filepath.Dir(filepath.FromSlash(module)))
	count, err := gating.ModulePackageCount(ctx, run, dir)
	if err != nil {
		writeErr := writeWithStatus(stderr, fmt.Sprintf("govuln: %s: no verdict: %s\n", module, err), 0)
		return govulnIncompleteExit, writeErr
	}
	if count == 0 {
		return 0, writeWithStatus(stdout, fmt.Sprintf("govuln: SKIP: %s (module: %s)\n", gating.NoPackagesNote, module), 0)
	}
	report, err := govuln.Check(ctx, govuln.Options{Dir: dir, VEXRoot: root, Scanner: scanner, Run: run})
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		writeErr := writeWithStatus(stderr, fmt.Sprintf("govuln: %s: no verdict: %s (%v)\n", module, err, elapsed), 0)
		return govulnIncompleteExit, writeErr
	}
	if report.Failed() {
		if writeErr := writeWithStatus(stdout, prefixedLines("govuln: ", report.Lines(true)), 0); writeErr != nil {
			return govulnFindingExit, writeErr
		}
		text := prefixedLines("govuln: ", report.Lines(false)) +
			fmt.Sprintf("govuln: FAIL: %s (module: %s, elapsed: %v)\n", report.Summary(), module, elapsed)
		return govulnFindingExit, writeWithStatus(stderr, text, 0)
	}
	notes := prefixedLines("govuln: ", report.Lines(true)) + prefixedLines("govuln: note: ", report.UnusedLines())
	text := notes + fmt.Sprintf("govuln: PASS: %s (module: %s, elapsed: %v)\n", report.Summary(), module, elapsed)
	return 0, writeWithStatus(stdout, text, 0)
}

func statusError(code int) error {
	if code == 0 {
		return nil
	}
	return exitStatusError{code: code}
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
