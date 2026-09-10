package lockdown

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	MaxDistillLines  = 58
	MaxDistillTokens = 1500
	MaxLoopLimit     = 1000
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

// SarifDriver describes the tool driver name and version.
type SarifDriver struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
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
	Summary        string            `json:"summary"`
	FullReportPath string            `json:"full_report_path"`
	TotalResults   int               `json:"total_results"`
	TotalErrors    int               `json:"total_errors"`
	Categories     map[string]int    `json:"categories"`
	TopFailures    []DiagnosticPointer `json:"top_failures"`
	LineCount      int               `json:"line_count"`
	TokenEstimate  int               `json:"token_estimate"`
}

// readContextLines reads +/-2 lines of context around targetLine from filePath.
func readContextLines(filePath string, targetLine int) ([]string, int) {
	file, err := os.Open(filePath)
	if err != nil {
		return []string{"[source context unavailable]"}, targetLine
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := make([]string, 0, 100)
	lineIdx := 1
	for scanner.Scan() && lineIdx <= targetLine+2 && len(lines) < MaxLoopLimit {
		lines = append(lines, scanner.Text())
		lineIdx++
	}

	start := targetLine - 2
	if start < 1 {
		start = 1
	}
	end := targetLine + 2
	if end > len(lines) {
		end = len(lines)
	}

	if start > len(lines) || start > end {
		return []string{"[source line out of range]"}, targetLine
	}

	snippet := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		prefix := "  "
		if i == targetLine {
			prefix = "> "
		}
		snippet = append(snippet, fmt.Sprintf("%s%4d | %s", prefix, i, lines[i-1]))
	}
	return snippet, start
}

// writeEphemeralSARIF writes full raw SARIF JSON to an ephemeral storage file.
func writeEphemeralSARIF(dir string, data []byte) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create ephemeral dir: %w", err)
	}

	hash := sha256.Sum256(data)
	fileName := fmt.Sprintf("sarif-%d-%s.sarif", time.Now().UnixNano(), hex.EncodeToString(hash[:6]))
	fullPath := filepath.Join(dir, fileName)

	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed writing ephemeral SARIF to %s: %w", fullPath, err)
	}
	return fullPath, nil
}

// estimateTokens returns an approximate token count based on whitespace and punctuation.
func estimateTokens(s string) int {
	words := strings.Fields(s)
	// Heuristic: words * 1.3 roughly approximates BPE token count for code
	return int(float64(len(words)) * 1.3)
}

// extractTopFailures extracts up to maxCount root-cause failure pointers with context.
func extractTopFailures(results []SarifResult, sourceRoot string, maxCount int) []DiagnosticPointer {
	failures := make([]DiagnosticPointer, 0, maxCount)
	count := 0
	for i := 0; i < len(results) && count < maxCount && i < MaxLoopLimit; i++ {
		res := results[i]
		dp := DiagnosticPointer{
			RuleID:  res.RuleID,
			Level:   res.Level,
			Message: res.Message.Text,
		}
		if len(res.Locations) > 0 {
			loc := res.Locations[0].PhysicalLocation
			dp.URI = loc.ArtifactLocation.URI
			dp.StartLine = loc.Region.StartLine
			dp.StartColumn = loc.Region.StartColumn

			targetFile := dp.URI
			if !filepath.IsAbs(targetFile) && sourceRoot != "" {
				targetFile = filepath.Join(sourceRoot, targetFile)
			}
			snippet, _ := readContextLines(targetFile, dp.StartLine)
			dp.ContextLines = snippet
		}
		failures = append(failures, dp)
		count++
	}
	return failures
}

// capSummary enforces strictly <= maxLines and <= maxTokens constraints.
func capSummary(rawLines []string, ephemeralPath string) (string, int, int) {
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
		finalLines = append(finalLines, fmt.Sprintf("Full SARIF log: %s", ephemeralPath))
	}

	res := strings.Join(finalLines, "\n")
	return res, len(finalLines), estimateTokens(res)
}

// aggregateDiagnostics collects results, category tallies, and error counts.
func aggregateDiagnostics(log SarifLog) ([]SarifResult, map[string]int, int) {
	categories := make(map[string]int)
	allResults := make([]SarifResult, 0)
	totalErrors := 0

	for _, run := range log.Runs {
		for _, res := range run.Results {
			allResults = append(allResults, res)
			cat := res.RuleID
			if cat == "" {
				cat = "unknown"
			}
			categories[cat]++
			if res.Level == "error" || res.Level == "" {
				totalErrors++
			}
		}
	}
	return allResults, categories, totalErrors
}

// buildSummaryLines constructs formatted lines for the distillation summary.
func buildSummaryLines(allResults []SarifResult, totalErrors int, categories map[string]int, topFailures []DiagnosticPointer, ephemeralPath string) []string {
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
	lines = append(lines, fmt.Sprintf("Full SARIF log written to: %s", ephemeralPath))
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

	allResults, categories, totalErrors := aggregateDiagnostics(log)
	topFailures := extractTopFailures(allResults, sourceRoot, 3)
	lines := buildSummaryLines(allResults, totalErrors, categories, topFailures, ephemeralPath)
	summaryText, lineCount, tokenEst := capSummary(lines, ephemeralPath)

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
