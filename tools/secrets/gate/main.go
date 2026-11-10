//go:build secretsgate

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command gate is the emitted standalone secrets history scan gate.
// It executes pinned gitleaks over a commit range or full history with merge commits (-m), reads
// HISS-12 exceptions from SECRETS_EXCEPTIONS, and evaluates them with standard-library rules.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var pullRequestRange = regexp.MustCompile(`^[0-9a-fA-F]{7,40}\.\.[0-9a-fA-F]{7,40}$`)

const (
	scanTimeout   = 5 * time.Minute
	defaultConfig = ".gitleaks.toml"
	maxEventBytes = 4 << 20
	// logOpts reads full history and merge commits (#1237). Order
	// engine-secretscan-foreign-remote-refs-20261025: read only the repository's
	// own refs (branches, tags, origin); --all walked refs of other remotes.
	logOpts = "--full-history --branches --tags --remotes=origin -m"

	exitPass       = 0
	exitIncomplete = 2
	exitFinding    = 3
)

// main runs the gate on the process arguments and exits with its code.
func main() {
	exitCode := runGate(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(exitCode)
}

// runGate parses the flags, resolves the scan scope, runs gitleaks and judges the findings, returning the exit code.
func runGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secrets-gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoPath := fs.String("path", ".", "Path to repository root")
	configPath := fs.String("config", "", "Path to .gitleaks.toml")
	var commitRange string
	fs.Func("range", "Pull request commit range: <base>..<head> (7-40 hex each)", func(value string) error {
		if !pullRequestRange.MatchString(value) {
			return fmt.Errorf("invalid -range %q: expected <base>..<head> with 7-40 hex characters each", value)
		}
		commitRange = value
		return nil
	})
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitPass
		}
		return exitIncomplete
	}
	commitRange, err := resolveCommitRange(commitRange)
	if err != nil {
		fmt.Fprintf(stderr, "secrets gate: %v\n", err)
		return exitIncomplete
	}

	entries, err := loadEnvironmentExceptions(time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "secrets gate: invalid exceptions: %v\n", err)
		return exitIncomplete
	}

	cfg := *configPath
	if cfg == "" {
		cfg = filepath.Join(*repoPath, defaultConfig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	findings, err := executeGitleaks(ctx, *repoPath, cfg, scanLogOptions(commitRange, stdout))
	if err != nil {
		fmt.Fprintf(stderr, "secrets gate: %v\n", err)
		return exitIncomplete
	}

	today := Day(time.Now())
	result := JudgeWithOptions(findings, entries, *repoPath, today, JudgeOptions{DeferUnused: commitRange != ""})
	return reportGateResult(result, stdout, stderr)
}

// resolveCommitRange returns the explicit -range, or on a pull_request event the base..head range read from GITHUB_EVENT_PATH; other events scan full history.
func resolveCommitRange(explicit string) (string, error) {
	if explicit != "" || os.Getenv("GITHUB_EVENT_NAME") != "pull_request" {
		return explicit, nil
	}
	data, err := readEventPayload(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return "", fmt.Errorf("GITHUB_EVENT_PATH: %w", err)
	}
	var event struct {
		PullRequest struct {
			Base struct {
				SHA string `json:"sha"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return "", fmt.Errorf("GITHUB_EVENT_PATH: invalid JSON: %w", err)
	}
	base, head := event.PullRequest.Base.SHA, event.PullRequest.Head.SHA
	if !pullRequestRange.MatchString(base + ".." + base) {
		return "", fmt.Errorf("pull_request.base.sha %q: expected 7-40 hex characters", base)
	}
	if !pullRequestRange.MatchString(head + ".." + head) {
		return "", fmt.Errorf("pull_request.head.sha %q: expected 7-40 hex characters", head)
	}
	return base + ".." + head, nil
}

// readEventPayload reads the event payload, refusing a missing, non-regular or oversized file.
func readEventPayload(path string) (data []byte, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat event file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("event must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open event file: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened event file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("event must be a regular file")
	}
	if info.Size() > maxEventBytes {
		return nil, errors.New("event exceeds 4 MiB limit")
	}
	data, err = io.ReadAll(io.LimitReader(file, maxEventBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read event file: %w", err)
	}
	if len(data) > maxEventBytes {
		return nil, errors.New("event exceeds 4 MiB limit")
	}
	return data, nil
}

// scanLogOptions returns the gitleaks --log-opts value for the scope and prints which scope the gate scans.
func scanLogOptions(commitRange string, stdout io.Writer) string {
	if commitRange == "" {
		fmt.Fprintln(stdout, "secrets gate: scanning full history")
		return logOpts
	}
	base, head, _ := strings.Cut(commitRange, "..")
	fmt.Fprintf(stdout, "secrets gate: scanning pull request range %s..%s\n", base[:min(len(base), 12)], head[:min(len(head), 12)])
	return commitRange + " -m"
}

// loadEnvironmentExceptions decodes and validates the HISS-12 exceptions from SECRETS_EXCEPTIONS.
func loadEnvironmentExceptions(today time.Time) ([]Entry, error) {
	raw := os.Getenv("SECRETS_EXCEPTIONS")
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var entries []Entry
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse SECRETS_EXCEPTIONS JSON: %w", err)
	}
	if len(entries) > MaxJudgedEntries {
		return nil, fmt.Errorf("exceptions count %d exceeds maximum %d", len(entries), MaxJudgedEntries)
	}
	for i := 0; i < len(entries) && i < MaxJudgedEntries; i++ {
		e := entries[i]
		if prob := ValidateHISS12Target(e.Path, e.RuleID, e.Fingerprint, ""); prob != "" {
			return nil, fmt.Errorf("entry[%d]: %s", i, prob)
		}
		if prob := CategoryExpiryProblem(e.Category, e.Entered, e.Expires, today); prob != "" {
			return nil, fmt.Errorf("entry[%d]: %s", i, prob)
		}
	}
	return entries, nil
}

// executeGitleaks runs gitleaks under ctx over the scope and returns the findings of its JSON report.
func executeGitleaks(ctx context.Context, dir, cfg, scanOpts string) (findings []Finding, err error) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		return nil, fmt.Errorf("gitleaks not found on PATH: %w", err)
	}
	reportFile, err := os.CreateTemp("", "gitleaks-gate-*.json")
	if err != nil {
		return nil, fmt.Errorf("create temp report: %w", err)
	}
	reportPath := reportFile.Name()
	if closeErr := reportFile.Close(); closeErr != nil {
		return nil, errors.Join(fmt.Errorf("close temp report: %w", closeErr), os.Remove(reportPath))
	}
	defer func() { err = errors.Join(err, os.Remove(reportPath)) }()

	cmdArgs := []string{
		"detect", "--no-banner", "--redact", "--config", cfg,
		"--log-opts=" + scanOpts, "--report-format", "json", "--report-path", reportPath,
	}
	cmd := exec.CommandContext(ctx, "gitleaks", cmdArgs...)
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()

	data, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("gitleaks execution failed: %w: %s", runErr, out)
		}
		return nil, fmt.Errorf("read gitleaks report: %w", readErr)
	}
	return parseGateReport(data, runErr, out)
}

// parseGateReport decodes a gitleaks report, treating a failed run without a report as incomplete.
func parseGateReport(data []byte, runErr error, out []byte) ([]Finding, error) {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("gitleaks did not complete: %w: %s", runErr, out)
		}
		return nil, nil
	}
	var findings []Finding
	if err := json.Unmarshal(data, &findings); err != nil {
		return nil, fmt.Errorf("unmarshal report JSON: %w", err)
	}
	if len(findings) == 0 && runErr != nil {
		return nil, fmt.Errorf("gitleaks did not complete: %w: %s", runErr, out)
	}
	return findings, nil
}

// reportGateResult prints the judgment and returns the gate exit code.
func reportGateResult(res Result, stdout, stderr io.Writer) int {
	reportUnjudgedEntries(res.Unjudged, stdout)
	for i := 0; i < len(res.Allowed) && i < MaxJudgedFindings; i++ {
		f := res.Allowed[i]
		fmt.Fprintf(stdout, "secrets: allowed: commit %s: %s:%d [%s] (%s)\n", f.Commit, f.File, f.StartLine, f.RuleID, f.Fingerprint)
	}
	if !res.Failed() {
		summary := formatGatePassSummary(len(res.Allowed))
		fmt.Fprintf(stdout, "secrets: PASS: %s\n", summary)
		return exitPass
	}
	for i := 0; i < len(res.Blocking) && i < MaxJudgedFindings; i++ {
		f := res.Blocking[i]
		fmt.Fprintf(stderr, "secrets: finding: commit %s: %s:%d [%s] (%s)\n", f.Commit, f.File, f.StartLine, f.RuleID, f.Fingerprint)
	}
	for i := 0; i < len(res.Stale) && i < MaxJudgedEntries; i++ {
		s := res.Stale[i]
		fmt.Fprintf(stderr, "secrets: exceptions entry (path %s, rule %s): excuses no finding: remove it\n", s.Path, s.RuleID)
	}
	for i := 0; i < len(res.Expired) && i < MaxJudgedEntries; i++ {
		e := res.Expired[i]
		fmt.Fprintf(stderr, "secrets: exceptions entry (path %s, rule %s): expired on %s: remove or update it\n", e.Path, e.RuleID, e.Expires)
	}
	fmt.Fprintf(stderr, "secrets: FAIL: %s\n", formatGateFailSummary(res))
	return exitFinding
}

// reportUnjudgedEntries prints each exception a range scan leaves to the full-history scan.
func reportUnjudgedEntries(entries []Entry, stdout io.Writer) {
	for i := 0; i < len(entries) && i < MaxJudgedEntries; i++ {
		e := entries[i]
		fmt.Fprintf(stdout, "secrets: exceptions entry (path %s, rule %s): not judged in a pull request range; the full-history scan on push, merge_group and schedule judges it\n", e.Path, e.RuleID)
	}
}

// formatGatePassSummary renders the passing summary line.
func formatGatePassSummary(allowedCount int) string {
	if allowedCount == 0 {
		return "zero secret findings in history"
	}
	return fmt.Sprintf("zero blocking secret findings (%d allowed by unexpired exceptions)", allowedCount)
}

// formatGateFailSummary renders the failing summary line from the judgment counts.
func formatGateFailSummary(res Result) string {
	var parts []string
	if len(res.Blocking) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocking secret finding(s)", len(res.Blocking)))
	}
	if len(res.Stale) > 0 {
		parts = append(parts, fmt.Sprintf("%d unused exception(s)", len(res.Stale)))
	}
	if len(res.Expired) > 0 {
		parts = append(parts, fmt.Sprintf("%d expired exception(s)", len(res.Expired)))
	}
	return strings.Join(parts, ", ")
}
