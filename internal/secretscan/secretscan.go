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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxReportBytes bounds the gitleaks JSON report the scan reads (HISS-02).
const maxReportBytes = 256 << 20

const (
	// ScanTimeout bounds one gitleaks run (HISS-02).
	ScanTimeout = 5 * time.Minute
	// DefaultScanner is the default binary Check invokes.
	DefaultScanner = "gitleaks"
	// DefaultConfig is the repository-relative gitleaks configuration file.
	DefaultConfig = ".gitleaks.toml"
	// BaselineFile is the repository-relative HISS-13 baseline file (order engine-hiss-unenforced-checks-20261027).
	BaselineFile = ".standards-baseline.json"
	// BaselineRuleID is the rule identifier recorded for secret findings in the HISS-13 baseline.
	BaselineRuleID = baseline.BaselineRuleID
	// LogOpts are the log options reading full history and merge commits (#1237).
	// Order engine-secretscan-foreign-remote-refs-20261025: read only the repository's
	// own refs (branches, tags, origin); --all walked refs of other remotes.
	LogOpts = "--full-history --branches --tags --remotes=origin -m"
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
	Allowed        []Finding
	Baselined      []Finding
	Blocking       []Finding
	Unused         []config.Exception
	StaleBaseline  []baseline.Infraction
	BaselineAbsent bool
	Total          int
}

// RecordOptions selects how RecordBaseline records HISS-12 findings into .standards-baseline.json.
type RecordOptions struct {
	Options
	Reason     string
	Repository string
	CommitSHA  string
}

// RecordResult holds the outcome of RecordBaseline.
type RecordResult struct {
	Path      string
	Previous  *baseline.Baseline
	Next      *baseline.Baseline
	Recorded  int
	Unchanged bool
}

// Failed reports whether the scan failed the gate: any unexcused finding or unused exception.
func (r *Report) Failed() bool {
	return len(r.Blocking) > 0 || len(r.Unused) > 0
}

// Summary returns a human-readable one-line summary of the verdicts.
func (r *Report) Summary() string {
	if !r.Failed() {
		return r.passSummary()
	}
	return r.failSummary()
}

func (r *Report) passSummary() string {
	baselined := len(r.Baselined)
	excAllowed := max(0, len(r.Allowed)-baselined)
	var base string
	switch {
	case excAllowed == 0 && baselined == 0:
		base = "zero secret findings in history"
	case excAllowed > 0 && baselined == 0:
		base = fmt.Sprintf("zero blocking secret findings (%d allowed by unexpired exceptions)", excAllowed)
	case excAllowed == 0 && baselined > 0:
		base = fmt.Sprintf("zero blocking secret findings (%d baselined)", baselined)
	default:
		base = fmt.Sprintf("zero blocking secret findings (%d allowed by unexpired exceptions, %d baselined)", excAllowed, baselined)
	}
	return r.appendBaselineNotes(base)
}

func (r *Report) failSummary() string {
	parts := make([]string, 0, 3)
	if len(r.Blocking) > 0 {
		parts = append(parts, fmt.Sprintf("%d blocking secret finding(s)", len(r.Blocking)))
	}
	if len(r.Unused) > 0 {
		parts = append(parts, fmt.Sprintf("%d unused exception(s)", len(r.Unused)))
	}
	if len(r.Baselined) > 0 {
		parts = append(parts, fmt.Sprintf("%d baselined", len(r.Baselined)))
	}
	return r.appendBaselineNotes(strings.Join(parts, ", "))
}

func (r *Report) appendBaselineNotes(base string) string {
	if len(r.StaleBaseline) > 0 {
		base += fmt.Sprintf("; %d stale baselined entry(ies)", len(r.StaleBaseline))
	}
	if r.BaselineAbsent {
		base += "; no baselined entries (baseline file absent)"
	}
	return base
}

// whichTimeout bounds a single mise which query (HISS-02).
const whichTimeout = 30 * time.Second

// ResolveScanner returns the executable path Check should run for DefaultScanner.
//
// In isolated execution environments (such as the public dogfood adoption loop or the discovery
// sandbox), the child context sets an isolated HOME and PATH (for example, HOME=<run>/home,
// PATH=/usr/bin:/bin). When gitleaks on the parent PATH is a mise shim (a symlink resolving to
// mise), running the shim under an isolated HOME causes mise to walk parent directories for
// configuration and fail because ~/.config/mise/config.toml is untrusted.
//
// When gitleaks on PATH is a mise shim, ResolveScanner resolves the real binary path by running
// "mise which gitleaks" in os.TempDir() with the ambient environment restored via
// util.InheritedEnvironment, validating that the output points to an existing regular executable
// binary before returning it. For non-shim installations, the path found by exec.LookPath is
// returned unchanged.
func ResolveScanner(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("secretscan: context cannot be nil")
	}
	path, err := exec.LookPath(DefaultScanner)
	if err != nil {
		return "", fmt.Errorf("locate %s on PATH: %w", DefaultScanner, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s symlink: %w", path, err)
	}
	if !isMiseExecutable(filepath.Base(resolved)) {
		return path, nil
	}
	return resolveMiseShim(ctx, path, resolved)
}

// resolveMiseShim asks mise for the executable behind the shim, first with the ambient
// environment. A process that moved HOME after start (a test harness isolating the user
// configuration) leaves mise without its install tree, so a failed first query is retried once
// with the home the shim itself lives under (inferMiseHome); both failures are reported.
func resolveMiseShim(ctx context.Context, path, resolved string) (string, error) {
	out, err := runMiseWhich(ctx, resolved, nil)
	if err == nil {
		return validateMiseOutput(path, out)
	}
	home := inferMiseHome(path)
	if home == "" {
		return "", fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %w", DefaultScanner, path, DefaultScanner, err)
	}
	out, retryErr := runMiseWhich(ctx, resolved, []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"MISE_CONFIG_DIR=" + filepath.Join(home, ".config", "mise"),
		"MISE_DATA_DIR=" + filepath.Join(home, ".local", "share", "mise"),
	})
	if retryErr != nil {
		return "", fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %w (retry with the shim's home %s: %w)",
			DefaultScanner, path, DefaultScanner, err, home, retryErr)
	}
	return validateMiseOutput(path, out)
}

// runMiseWhich runs `mise which gitleaks` in a neutral directory, never the scanned checkout, with
// the ambient environment plus overrides.
func runMiseWhich(ctx context.Context, mise string, overrides []string) (string, error) {
	whichCtx, cancel := context.WithTimeout(ctx, whichTimeout)
	defer cancel()
	envCtx, err := util.WithCommandEnvironment(whichCtx, util.InheritedEnvironment(overrides))
	if err != nil {
		return "", fmt.Errorf("configure the mise environment: %w", err)
	}
	return util.RunCommand(envCtx, os.TempDir(), mise, "which", DefaultScanner)
}

func inferMiseHome(path string) string {
	for _, candidate := range []string{path, evalLink(path)} {
		if candidate == "" {
			continue
		}
		prefix, _, found := strings.Cut(candidate, string(filepath.Separator)+".local")
		if found && prefix != "" {
			miseData := filepath.Join(prefix, ".local", "share", "mise")
			if fi, err := os.Stat(miseData); err == nil && fi.IsDir() {
				return prefix
			}
		}
	}
	return ""
}

func evalLink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		return filepath.Join(filepath.Dir(path), target)
	}
	return target
}

func validateMiseOutput(path, out string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(out), "\r\n", "\n"), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: expected exactly one line, got %d lines: %q",
			DefaultScanner, path, DefaultScanner, len(lines), out)
	}
	realPath := strings.TrimSpace(lines[0])
	if !filepath.IsAbs(realPath) {
		return "", fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: path %q is not absolute",
			DefaultScanner, path, DefaultScanner, realPath)
	}
	expectedBase := executableName(DefaultScanner)
	if !isMatchingExecutableName(filepath.Base(realPath), expectedBase) {
		return "", fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: base name is %q, expected %q",
			DefaultScanner, path, DefaultScanner, filepath.Base(realPath), expectedBase)
	}
	if err := validateExecutableFile(path, realPath); err != nil {
		return "", err
	}
	return realPath, nil
}

func validateExecutableFile(path, realPath string) error {
	fi, err := os.Stat(realPath)
	if err != nil {
		return fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %w",
			DefaultScanner, path, DefaultScanner, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %s is not a regular file",
			DefaultScanner, path, DefaultScanner, realPath)
	}
	if !isExecutableRegularFile(realPath, fi) {
		return fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %s is not executable",
			DefaultScanner, path, DefaultScanner, realPath)
	}
	realResolved, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		return fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: %w",
			DefaultScanner, path, DefaultScanner, err)
	}
	if isMiseExecutable(filepath.Base(realResolved)) {
		return fmt.Errorf("%s on PATH (%s) is a mise shim; resolving its executable with \"mise which %s\" failed: resolved path %s is also a mise shim",
			DefaultScanner, path, DefaultScanner, realResolved)
	}
	return nil
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isMatchingExecutableName(base, expected string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(base, expected)
	}
	return base == expected
}

func isMiseExecutable(name string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, "mise.exe") || strings.EqualFold(name, "mise")
	}
	return name == "mise"
}

func isExecutableRegularFile(path string, fi os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(path))
		return ext == ".exe" || ext == ".com"
	}
	return fi.Mode().Perm()&0o111 != 0
}

// Check runs gitleaks in opts.Dir and judges its findings against the manifest's HISS-12 exceptions
// and the HISS-13 baseline (.standards-baseline.json).
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
	base, err := baseline.LoadBaseline(filepath.Join(opts.Dir, BaselineFile))
	if err != nil {
		return nil, fmt.Errorf("load baseline: %w", err)
	}
	findings, err := runGitleaks(ctx, opts)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	return judgeFindings(findings, entries, base, opts.Dir, config.ExceptionDay(now())), nil
}

// secretFindingsToInfractions converts gitleaks findings into HISS-13 baseline infractions.
func secretFindingsToInfractions(findings []Finding, dir string) []baseline.Infraction {
	out := make([]baseline.Infraction, len(findings))
	for i := 0; i < len(findings); i++ {
		f := findings[i]
		out[i] = baseline.Infraction{
			RuleID:      BaselineRuleID,
			FilePath:    baseline.NormalizePath(rules.NormalizeFindingPath(f.File, dir)),
			LineNumber:  f.StartLine,
			Message:     f.RuleID,
			Fingerprint: f.Fingerprint,
		}
	}
	return out
}

// UnexcusedSecretInfractions scans opts.Dir with gitleaks, filters out findings excused by live
// HISS-12 exceptions in the manifest, and converts the remaining findings into HISS-13 baseline infractions.
func UnexcusedSecretInfractions(ctx context.Context, opts Options) ([]baseline.Infraction, error) {
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
	excReport := judgeFindings(findings, entries, nil, opts.Dir, config.ExceptionDay(now()))
	return secretFindingsToInfractions(excReport.Blocking, opts.Dir), nil
}

// RecordBaseline scans history with gitleaks and writes unexcused findings as HISS-12 entries in
// .standards-baseline.json, keeping every non-HISS-12 entry and enforcing the HISS-13 ratchet.
func RecordBaseline(ctx context.Context, opts RecordOptions) (*RecordResult, error) {
	if ctx == nil {
		return nil, errors.New("secretscan: context cannot be nil")
	}
	if opts.Dir == "" || opts.Run == nil {
		return nil, errors.New("secretscan: directory and runner are required")
	}
	baselinePath := filepath.Join(opts.Dir, BaselineFile)
	previous, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("load baseline: %w", err)
	}
	infractions, err := UnexcusedSecretInfractions(ctx, opts.Options)
	if err != nil {
		return nil, err
	}
	return writeRecordedBaseline(baselinePath, previous, infractions, opts)
}

func writeRecordedBaseline(path string, previous *baseline.Baseline, infractions []baseline.Infraction, opts RecordOptions) (*RecordResult, error) {
	combined := mergeSecretInfractions(previous.Infractions, infractions)
	reason := strings.TrimSpace(opts.Reason)
	recBase := previous
	if previous.Absent {
		recBase = &baseline.Baseline{
			Version:          1,
			Repository:       opts.Repository,
			CommitSHA:        opts.CommitSHA,
			TotalInfractions: len(combined),
			Infractions:      []baseline.Infraction{},
		}
	}
	next, err := baseline.Record(recBase, combined, baseline.RecordOptions{
		AllowIncrease: reason != "",
		Rationale:     reason,
		Repository:    opts.Repository,
		CommitSHA:     opts.CommitSHA,
		OwnedRules:    []string{BaselineRuleID},
	})
	if err != nil {
		return nil, fmt.Errorf("refusing to record %s: %w (pass --reason=<why> to record a deliberate increase)", path, err)
	}
	if previous.Absent && reason != "" && len(combined) > 0 {
		next.IncreaseRationale = reason
	}
	if !previous.Absent && previous.SameDebt(next) {
		return &RecordResult{Path: path, Previous: previous, Next: previous, Recorded: len(infractions), Unchanged: true}, nil
	}
	if err := baseline.SaveBaseline(path, next); err != nil {
		return nil, err
	}
	return &RecordResult{Path: path, Previous: previous, Next: next, Recorded: len(infractions)}, nil
}

func mergeSecretInfractions(existing, newInfractions []baseline.Infraction) []baseline.Infraction {
	combined := make([]baseline.Infraction, 0, len(existing)+len(newInfractions))
	for i := 0; i < len(existing); i++ {
		if existing[i].RuleID != BaselineRuleID {
			combined = append(combined, existing[i])
		}
	}
	combined = append(combined, newInfractions...)
	return combined
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
	data, readErr := util.ReadFileLimited(reportPath, maxReportBytes)
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
		if _, err := os.Stat(filepath.Join(opts.Dir, DefaultConfig)); err == nil {
			cfg = DefaultConfig
		}
	}
	scanner := opts.Scanner
	if len(scanner) == 0 {
		scanner = []string{DefaultScanner}
	}
	cmdName := scanner[0]
	cmdArgs := append([]string{}, scanner[1:]...)
	cmdArgs = append(cmdArgs, "detect", "--no-banner", "--redact")
	if cfg != "" {
		cmdArgs = append(cmdArgs, "--config", cfg)
	}
	cmdArgs = append(cmdArgs, "--log-opts="+LogOpts, "--report-format", "json", "--report-path", reportPath)
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

func judgeFindings(findings []Finding, entries []config.Exception, base *baseline.Baseline, dir string, today time.Time) *Report {
	ruleEntries := make([]rules.Entry, len(entries))
	for i := 0; i < len(entries); i++ {
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
	for i := 0; i < len(res.StaleIdx); i++ {
		report.Unused = append(report.Unused, entries[res.StaleIdx[i]])
	}
	applyBaseline(report, base)
	return report
}

func applyBaseline(report *Report, base *baseline.Baseline) {
	if base == nil || base.Absent {
		report.BaselineAbsent = true
		return
	}
	matched := indexBaselineFingerprints(base.Infractions)
	remaining := make([]Finding, 0, len(report.Blocking))
	for i := 0; i < len(report.Blocking); i++ {
		f := report.Blocking[i]
		if _, ok := matched[f.Fingerprint]; ok && f.Fingerprint != "" {
			matched[f.Fingerprint] = true
			report.Baselined = append(report.Baselined, f)
			report.Allowed = append(report.Allowed, f)
			continue
		}
		remaining = append(remaining, f)
	}
	report.Blocking = remaining
	report.StaleBaseline = collectStaleBaseline(base.Infractions, matched)
}

func indexBaselineFingerprints(infractions []baseline.Infraction) map[string]bool {
	matched := make(map[string]bool, len(infractions))
	for i := 0; i < len(infractions); i++ {
		inf := infractions[i]
		if inf.RuleID == BaselineRuleID && inf.Fingerprint != "" {
			matched[inf.Fingerprint] = false
		}
	}
	return matched
}

func collectStaleBaseline(infractions []baseline.Infraction, matched map[string]bool) []baseline.Infraction {
	var stale []baseline.Infraction
	for i := 0; i < len(infractions); i++ {
		inf := infractions[i]
		if inf.RuleID == BaselineRuleID && !matched[inf.Fingerprint] {
			stale = append(stale, inf)
		}
	}
	return stale
}
