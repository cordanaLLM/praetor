// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secretscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

const (
	// ScanTimeout bounds one gitleaks run (HISS-02).
	ScanTimeout = 5 * time.Minute
	// DefaultScanner is the default binary Check invokes.
	DefaultScanner = "gitleaks"
	// DefaultConfig is the repository-relative gitleaks configuration file.
	DefaultConfig = ".gitleaks.toml"
	// LogOpts are the log options reading full history and merge commits (#1237).
	LogOpts = "--full-history --all -m"
)

// Runner runs one command in dir and returns its standard output; a failure's standard error
// travels in the error. util.RunCommand is the production implementation.
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

// Finding represents one secret finding in the gitleaks JSON report.
type Finding = rules.Finding

// Options selects what Check scans and how.
type Options struct {
	Dir        string
	ConfigPath string
	Scanner    []string
	Run        Runner
	Now        func() time.Time
}

// Report holds the outcome of Check.
type Report struct {
	Allowed  []Finding
	Blocking []Finding
	Unused   []config.Exception
	Total    int
}

// Failed reports whether the scan failed the gate: any unexcused finding or unused exception.
func (r *Report) Failed() bool {
	return len(r.Blocking) > 0 || len(r.Unused) > 0
}

// Summary returns a human-readable one-line summary of the verdicts.
func (r *Report) Summary() string {
	if !r.Failed() {
		if len(r.Allowed) == 0 {
			return "zero secret findings in history"
		}
		return fmt.Sprintf("zero blocking secret findings (%d allowed by unexpired exceptions)", len(r.Allowed))
	}
	parts := make([]string, 0, 2)
	if len(r.Blocking) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocking secret finding(s)", len(r.Blocking)))
	}
	if len(r.Unused) > 0 {
		parts = append(parts, fmt.Sprintf("%d unused exception(s)", len(r.Unused)))
	}
	return strings.Join(parts, ", ")
}

// Check runs gitleaks in opts.Dir and judges its findings against the manifest's HISS-12 exceptions.
func Check(ctx context.Context, opts Options) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("secretscan: context cannot be nil")
	}
	if opts.Dir == "" || opts.Run == nil {
		return nil, errors.New("secretscan: directory and runner are required")
	}
	entries, err := config.LoadExceptionsFor(ctx, opts.Dir, config.ExceptionRuleSecrets)
	if err != nil {
		return nil, fmt.Errorf("load exceptions: %w", err)
	}
	findings, err := runGitleaks(ctx, opts)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	return judgeFindings(findings, entries, opts.Dir, config.ExceptionDay(now())), nil
}

func createTempReportPath() (string, error) {
	reportFile, err := os.CreateTemp("", "gitleaks-report-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp report: %w", err)
	}
	reportPath := reportFile.Name()
	if closeErr := reportFile.Close(); closeErr != nil {
		if rmErr := os.Remove(reportPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return "", errors.Join(closeErr, rmErr)
		}
		return "", fmt.Errorf("close temp report: %w", closeErr)
	}
	return reportPath, nil
}

func runGitleaks(ctx context.Context, opts Options) ([]Finding, error) {
	reportPath, err := createTempReportPath()
	if err != nil {
		return nil, err
	}
	defer func() {
		if rmErr := os.Remove(reportPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return
		}
	}()

	cmdName, cmdArgs := buildGitleaksArgs(opts, reportPath)
	scanCtx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()

	out, runErr := opts.Run(scanCtx, opts.Dir, cmdName, cmdArgs...)
	data, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("gitleaks scan failed: %w: %s", runErr, out)
		}
		return nil, fmt.Errorf("read gitleaks report: %w", readErr)
	}
	return parseReport(data, runErr, out)
}

func buildGitleaksArgs(opts Options, reportPath string) (string, []string) {
	cfg := opts.ConfigPath
	if cfg == "" {
		cfg = DefaultConfig
	}
	scanner := opts.Scanner
	if len(scanner) == 0 {
		scanner = []string{DefaultScanner}
	}
	cmdName := scanner[0]
	cmdArgs := append([]string{}, scanner[1:]...)
	cmdArgs = append(cmdArgs, "detect", "--no-banner", "--redact", "--config", cfg,
		"--log-opts="+LogOpts, "--report-format", "json", "--report-path", reportPath)
	return cmdName, cmdArgs
}

func parseReport(data []byte, runErr error, out string) ([]Finding, error) {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("gitleaks did not complete: %w: %s", runErr, out)
		}
		return nil, nil
	}
	var findings []Finding
	if err := json.Unmarshal(data, &findings); err != nil {
		return nil, fmt.Errorf("unmarshal gitleaks report: %w", err)
	}
	if len(findings) == 0 && runErr != nil {
		return nil, fmt.Errorf("gitleaks did not complete: %w: %s", runErr, out)
	}
	return findings, nil
}

func judgeFindings(findings []Finding, entries []config.Exception, dir string, today time.Time) *Report {
	ruleEntries := make([]rules.Entry, len(entries))
	for i := range entries {
		ruleEntries[i] = rules.Entry{
			Path:        entries[i].Path,
			RuleID:      entries[i].Tool,
			Fingerprint: entries[i].Fingerprint,
			Reason:      entries[i].Reason,
			Expires:     entries[i].Expires,
			Category:    entries[i].Category,
			Entered:     entries[i].Entered,
		}
	}
	res := rules.Judge(findings, ruleEntries, dir, today)
	report := &Report{
		Allowed:  res.Allowed,
		Blocking: res.Blocking,
		Total:    len(findings),
	}
	for _, idx := range res.StaleIdx {
		report.Unused = append(report.Unused, entries[idx])
	}
	return report
}
