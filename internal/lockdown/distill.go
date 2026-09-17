package lockdown

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxDistillLines and MaxDistillTokens alias the default inline evidence bound of the
	// text register policy: the distilled summary is the oldest instance of that rule, so
	// the two numbers share one definition.
	MaxDistillLines  = config.EvidenceInlineMaxLinesDefault
	MaxDistillTokens = config.EvidenceInlineMaxTokensDefault
	MaxLoopLimit     = 1000
	// ContextRadius is the number of source lines shown above and below a diagnostic.
	ContextRadius = 2
	// MaxSourceScanLines bounds the line scan of a source file (HISS-02). It is a sanity
	// cap on pathological input, not a limit on which diagnostics can be rendered.
	MaxSourceScanLines = 1 << 20
	// MaxSourceLineBytes bounds the length of a single scanned source line.
	MaxSourceLineBytes = 1 << 20
	// MaxContextLineChars caps how much of one source line is copied into the summary.
	MaxContextLineChars = 200
	// LevelError, LevelWarning and LevelNote are the SARIF result levels praetor ranks.
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNote    = "note"
)

var (
	// ErrEmptyArtifactURI is returned for a SARIF location without a usable URI.
	ErrEmptyArtifactURI = errors.New("sarif artifactLocation.uri is empty")
	// ErrUnconfinedSourceRoot is returned when source context is requested without a root
	// to confine the artifact URI to.
	ErrUnconfinedSourceRoot = errors.New("sarif source root is required to resolve artifact URIs")
	// ErrAbsoluteArtifactURI is returned for an absolute SARIF artifact URI, which could
	// otherwise pull arbitrary file content into the distilled summary.
	ErrAbsoluteArtifactURI = errors.New("sarif artifactLocation.uri must be relative to the source root")
)

// SarifLog represents the top-level SARIF 2.1.0 log structure.
type SarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema,omitempty"`
	Runs    []SarifRun `json:"runs"`
}

// SarifRun describes a single static analysis run.
type SarifRun struct {
	Tool    SarifTool     `json:"tool"`
	Results []SarifResult `json:"results"`
}

// SarifTool contains metadata about the analysis tool.
type SarifTool struct {
	Driver SarifDriver `json:"driver"`
}

// SarifDriver describes the tool driver name, version and rule descriptors.
type SarifDriver struct {
	Name    string                     `json:"name"`
	Version string                     `json:"version,omitempty"`
	Rules   []SarifReportingDescriptor `json:"rules,omitempty"`
}

// SarifReportingDescriptor is a rule descriptor (SARIF 2.1.0 3.49).
type SarifReportingDescriptor struct {
	ID                   string                      `json:"id"`
	DefaultConfiguration SarifReportingConfiguration `json:"defaultConfiguration,omitempty"`
}

// SarifReportingConfiguration carries a rule's default severity (SARIF 2.1.0 3.50).
type SarifReportingConfiguration struct {
	Level string `json:"level,omitempty"`
}

// SarifResult represents an individual diagnostic emitted by an analyzer.
type SarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level,omitempty"`
	Message   SarifMessage    `json:"message"`
	Locations []SarifLocation `json:"locations,omitempty"`
}

// SarifMessage contains the diagnostic message text.
type SarifMessage struct {
	Text string `json:"text"`
}

// SarifLocation indicates the location of a diagnostic.
type SarifLocation struct {
	PhysicalLocation SarifPhysicalLocation `json:"physicalLocation"`
}

// SarifPhysicalLocation describes the file and code region.
type SarifPhysicalLocation struct {
	ArtifactLocation SarifArtifactLocation `json:"artifactLocation"`
	Region           SarifRegion           `json:"region"`
}

// SarifArtifactLocation specifies the relative or absolute URI of the file.
type SarifArtifactLocation struct {
	URI string `json:"uri"`
}

// SarifRegion specifies the 1-based text coordinates of the diagnostic.
type SarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// DiagnosticPointer holds extracted diagnostic metadata with source context.
type DiagnosticPointer struct {
	RuleID       string   `json:"rule_id"`
	Level        string   `json:"level"`
	Message      string   `json:"message"`
	URI          string   `json:"uri"`
	StartLine    int      `json:"start_line"`
	StartColumn  int      `json:"start_column"`
	ContextLines []string `json:"context_lines,omitempty"`
}

// DistillResult contains the capped distillation summary and ephemeral storage pointer.
type DistillResult struct {
	Summary        string              `json:"summary"`
	FullReportPath string              `json:"full_report_path"`
	TotalResults   int                 `json:"total_results"`
	TotalErrors    int                 `json:"total_errors"`
	Categories     map[string]int      `json:"categories"`
	TopFailures    []DiagnosticPointer `json:"top_failures"`
	LineCount      int                 `json:"line_count"`
	TokenEstimate  int                 `json:"token_estimate"`
}

// readContextLines renders +/-ContextRadius lines of context around targetLine. Unlike a
// whole-file buffer it streams straight to the window, so a diagnostic at any line number
// is rendered, not just the first MaxLoopLimit lines of the file.
func readContextLines(filePath string, targetLine int) ([]string, int) {
	if targetLine < 1 || targetLine > MaxSourceScanLines {
		return []string{"[source line out of range]"}, targetLine
	}

	window, start, err := readLineWindow(filePath, targetLine)
	if err != nil {
		return []string{"[source context unavailable]"}, targetLine
	}
	if len(window) == 0 || start+len(window)-1 < targetLine {
		return []string{"[source line out of range]"}, targetLine
	}

	snippet := make([]string, 0, len(window))
	for i := 0; i < len(window); i++ {
		lineNum := start + i
		prefix := "  "
		if lineNum == targetLine {
			prefix = "> "
		}
		snippet = append(snippet, fmt.Sprintf("%s%4d | %s", prefix, lineNum, clampLine(window[i])))
	}
	return snippet, start
}

// readLineWindow streams filePath and returns the lines in
// [targetLine-ContextRadius, targetLine+ContextRadius] plus the first line number kept.
func readLineWindow(filePath string, targetLine int) (window []string, start int, err error) {
	// #nosec G304 -- callers resolve filePath through resolveDiagnosticPath, which
	// confines every SARIF artifact URI under the source root via util.ConfinePath.
	file, openErr := os.Open(filePath)
	if openErr != nil {
		return nil, 0, fmt.Errorf("open source %s: %w", filePath, openErr)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close source %s: %w", filePath, cerr)
		}
	}()

	first := targetLine - ContextRadius
	if first < 1 {
		first = 1
	}
	last := targetLine + ContextRadius

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxSourceLineBytes)
	window = make([]string, 0, 2*ContextRadius+1)
	for lineNum := 1; lineNum <= last && lineNum <= MaxSourceScanLines && scanner.Scan(); lineNum++ {
		if lineNum >= first {
			window = append(window, scanner.Text())
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, 0, fmt.Errorf("scan source %s: %w", filePath, scanErr)
	}
	return window, first, nil
}

// clampLine caps a single source line copied into the distilled summary.
func clampLine(line string) string {
	runes := []rune(line)
	if len(runes) <= MaxContextLineChars {
		return line
	}
	return string(runes[:MaxContextLineChars]) + "..."
}

// resolveDiagnosticPath maps a SARIF artifactLocation URI onto a filesystem path that
// provably stays inside sourceRoot. Absolute URIs and paths escaping the root are
// rejected so a hostile or misconfigured SARIF log cannot pull unrelated file content
// (for example a private key) into the distilled summary.
func resolveDiagnosticPath(sourceRoot, uri string) (string, error) {
	raw := strings.TrimSpace(uri)
	raw = strings.TrimPrefix(raw, "file://")
	if raw == "" {
		return "", ErrEmptyArtifactURI
	}
	if sourceRoot == "" {
		return "", ErrUnconfinedSourceRoot
	}
	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("%w: %q", ErrAbsoluteArtifactURI, uri)
	}
	confined, err := util.ConfinePath(sourceRoot, raw)
	if err != nil {
		return "", fmt.Errorf("sarif artifact %q: %w", uri, err)
	}
	return confined, nil
}

// writeEphemeralSARIF writes full raw SARIF JSON to an ephemeral storage file.
func writeEphemeralSARIF(dir string, data []byte) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := util.MkdirSecure(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create ephemeral dir: %w", err)
	}

	hash := sha256.Sum256(data)
	fileName := fmt.Sprintf("sarif-%d-%s.sarif", time.Now().UnixNano(), hex.EncodeToString(hash[:6]))
	fullPath := filepath.Join(dir, fileName)

	if err := util.WriteFileSecure(fullPath, data, 0o600); err != nil {
		return "", fmt.Errorf("failed writing ephemeral SARIF to %s: %w", fullPath, err)
	}
	return fullPath, nil
}

// sarifEvidencePointer renders the one pointer line that stands in for the full log.
func sarifEvidencePointer(path string, data []byte) (string, error) {
	lines := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	digest := sha256.Sum256(data)
	pointer, err := config.EvidencePointer(path, hex.EncodeToString(digest[:]), lines)
	if err != nil {
		return "", fmt.Errorf("full SARIF log at %q: %w", path, err)
	}
	return pointer, nil
}

// estimateTokens returns an approximate token count based on whitespace and punctuation.
func estimateTokens(s string) int {
	words := strings.Fields(s)
	// Heuristic: words * 1.3 roughly approximates BPE token count for code
	return int(float64(len(words)) * 1.3)
}

// levelRank orders SARIF levels by severity: error first, then warning, note and any
// other level. Results carry a resolved level by the time they reach this function.
func levelRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case LevelError:
		return 0
	case LevelWarning:
		return 1
	case LevelNote:
		return 2
	default:
		return 3
	}
}

// rankResults returns results ordered by descending severity, preserving the original
// order within a severity so the selection is deterministic.
func rankResults(results []SarifResult) []SarifResult {
	ranked := make([]SarifResult, len(results))
	copy(ranked, results)
	sort.SliceStable(ranked, func(i, j int) bool {
		return levelRank(ranked[i].Level) < levelRank(ranked[j].Level)
	})
	return ranked
}

// extractTopFailures extracts up to maxCount root-cause failure pointers with context,
// selecting the most severe diagnostics rather than the first ones in file order.
func extractTopFailures(results []SarifResult, sourceRoot string, maxCount int) []DiagnosticPointer {
	ranked := rankResults(results)
	failures := make([]DiagnosticPointer, 0, maxCount)
	for i := 0; i < len(ranked) && len(failures) < maxCount && i < MaxLoopLimit; i++ {
		failures = append(failures, buildPointer(ranked[i], sourceRoot))
	}
	return failures
}

// buildPointer converts a single SARIF result into a diagnostic pointer, attaching
// confined source context when the result carries a location.
func buildPointer(res SarifResult, sourceRoot string) DiagnosticPointer {
	dp := DiagnosticPointer{
		RuleID:  res.RuleID,
		Level:   res.Level,
		Message: res.Message.Text,
	}
	if len(res.Locations) == 0 {
		return dp
	}

	loc := res.Locations[0].PhysicalLocation
	dp.URI = loc.ArtifactLocation.URI
	dp.StartLine = loc.Region.StartLine
	dp.StartColumn = loc.Region.StartColumn

	targetFile, err := resolveDiagnosticPath(sourceRoot, dp.URI)
	if err != nil {
		dp.ContextLines = []string{"[source context unavailable]"}
		return dp
	}
	snippet, _ := readContextLines(targetFile, dp.StartLine)
	dp.ContextLines = snippet
	return dp
}

// capSummary enforces strictly <= maxLines and <= maxTokens constraints. A truncated
// summary ends with the evidence pointer, so the reader can always reach the full log.
func capSummary(rawLines []string, pointer string) (string, int, int) {
	finalLines := make([]string, 0, len(rawLines))
	truncated := false

	for i := 0; i < len(rawLines) && i < MaxLoopLimit; i++ {
		if len(finalLines) >= MaxDistillLines-2 {
			truncated = true
			break
		}
		candidate := strings.Join(append(finalLines, rawLines[i]), "\n")
		if estimateTokens(candidate) >= MaxDistillTokens-50 {
			truncated = true
			break
		}
		finalLines = append(finalLines, rawLines[i])
	}

	if truncated {
		finalLines = append(finalLines, fmt.Sprintf("[Truncated: output capped at <= %d lines / <= %d tokens]", MaxDistillLines, MaxDistillTokens))
		finalLines = append(finalLines, pointer)
	}

	res := strings.Join(finalLines, "\n")
	return res, len(finalLines), estimateTokens(res)
}

// resolveLevel implements SARIF 2.1.0 3.27.10: an absent result.level defaults to the
// rule descriptor's defaultConfiguration.level and, failing that, to "warning".
func resolveLevel(run SarifRun, res SarifResult) string {
	if lvl := strings.TrimSpace(res.Level); lvl != "" {
		return lvl
	}
	rules := run.Tool.Driver.Rules
	for i := 0; i < len(rules) && i < MaxLoopLimit; i++ {
		if rules[i].ID != res.RuleID {
			continue
		}
		if lvl := strings.TrimSpace(rules[i].DefaultConfiguration.Level); lvl != "" {
			return lvl
		}
		break
	}
	return LevelWarning
}

// aggregateDiagnostics collects results with resolved levels, category tallies, and the
// count of genuine errors.
func aggregateDiagnostics(log SarifLog) ([]SarifResult, map[string]int, int) {
	categories := make(map[string]int)
	allResults := make([]SarifResult, 0)
	totalErrors := 0

	for _, run := range log.Runs {
		for _, res := range run.Results {
			res.Level = resolveLevel(run, res)
			allResults = append(allResults, res)
			cat := res.RuleID
			if cat == "" {
				cat = "unknown"
			}
			categories[cat]++
			if res.Level == LevelError {
				totalErrors++
			}
		}
	}
	return allResults, categories, totalErrors
}

// buildSummaryLines constructs formatted lines for the distillation summary.
func buildSummaryLines(allResults []SarifResult, totalErrors int, categories map[string]int, topFailures []DiagnosticPointer, pointer string) []string {
	lines := make([]string, 0, 40)
	lines = append(lines, "=== SARIF Diagnostic Distillation ===")
	lines = append(lines, fmt.Sprintf("Total Diagnostics: %d (Errors: %d)", len(allResults), totalErrors))
	lines = append(lines, fmt.Sprintf("Categories: %s", formatCategories(categories)))
	lines = append(lines, "")

	if len(topFailures) == 0 {
		lines = append(lines, "No diagnostic errors reported.")
	} else {
		lines = append(lines, "Top Root-Cause Failures:")
		for idx, fail := range topFailures {
			lines = append(lines, fmt.Sprintf("[%d] %s: %s", idx+1, fail.RuleID, fail.Message))
			if fail.URI != "" {
				lines = append(lines, fmt.Sprintf("    At: %s:%d:%d", fail.URI, fail.StartLine, fail.StartColumn))
				for _, ctxLine := range fail.ContextLines {
					lines = append(lines, "    "+ctxLine)
				}
			}
			lines = append(lines, "")
		}
	}
	lines = append(lines, pointer)
	return lines
}

// DistillSARIF analyzes a SARIF 2.1.0 document, groups errors, extracts context, and caps output.
func DistillSARIF(ctx context.Context, sarifJSON []byte, sourceRoot string, ephemeralDir string) (*DistillResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var log SarifLog
	if err := json.Unmarshal(sarifJSON, &log); err != nil {
		return nil, fmt.Errorf("failed to parse SARIF JSON: %w", err)
	}

	ephemeralPath, err := writeEphemeralSARIF(ephemeralDir, sarifJSON)
	if err != nil {
		return nil, err
	}

	pointer, err := sarifEvidencePointer(ephemeralPath, sarifJSON)
	if err != nil {
		return nil, err
	}

	allResults, categories, totalErrors := aggregateDiagnostics(log)
	topFailures := extractTopFailures(allResults, sourceRoot, 3)
	lines := buildSummaryLines(allResults, totalErrors, categories, topFailures, pointer)
	summaryText, lineCount, tokenEst := capSummary(lines, pointer)

	return &DistillResult{
		Summary:        summaryText,
		FullReportPath: ephemeralPath,
		TotalResults:   len(allResults),
		TotalErrors:    totalErrors,
		Categories:     categories,
		TopFailures:    topFailures,
		LineCount:      lineCount,
		TokenEstimate:  tokenEst,
	}, nil
}

// formatCategories produces a deterministic sorted string of category counts.
func formatCategories(cats map[string]int) string {
	keys := make([]string, 0, len(cats))
	for k := range cats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	limit := len(keys)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		parts = append(parts, fmt.Sprintf("%s (%d)", keys[i], cats[keys[i]]))
	}
	if len(keys) > 10 {
		parts = append(parts, fmt.Sprintf("+%d more", len(keys)-10))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
