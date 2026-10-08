// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// SpendReport records spend attributed per PR number and total unattributed spend.
type SpendReport struct {
	SpendByPRNumber          map[int]float64
	RequestsByPRNumber       map[int]int
	LocalRequestsByPRNumber  map[int]int
	FrontierTokensByPRNumber map[int]int64
	Unattributed             float64
}

// rawSpendEntry is an intermediate representation of a spend log row/line.
type rawSpendEntry struct {
	Model       string          `json:"model"`
	Spend       float64         `json:"spend"`
	Cost        float64         `json:"cost"`
	Tokens      int64           `json:"tokens"`
	InputTokens int64           `json:"input_tokens"`
	StartTime   string          `json:"start_time"`
	Timestamp   string          `json:"timestamp"`
	Branch      string          `json:"branch"`
	PullRequest json.RawMessage `json:"pull_request"`
	PR          json.RawMessage `json:"pr"`
	Tags        []string        `json:"tags"`
	Metadata    json.RawMessage `json:"metadata"`
}

func parseRawPR(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var num int
	if err := json.Unmarshal(raw, &num); err == nil && num > 0 {
		return num
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		str = strings.TrimPrefix(str, "#")
		if n, err := strconv.Atoi(str); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func matchTag(tag string, branchToPR map[string]int, validPRs map[int]bool) (int, bool) {
	if prNum, ok := branchToPR[tag]; ok {
		return prNum, true
	}
	if strings.HasPrefix(tag, "branch:") {
		b := strings.TrimPrefix(tag, "branch:")
		if prNum, ok := branchToPR[b]; ok {
			return prNum, true
		}
	}
	if strings.HasPrefix(tag, "pr:") || strings.HasPrefix(tag, "pull_request:") {
		val := strings.TrimPrefix(tag, "pr:")
		val = strings.TrimPrefix(val, "pull_request:")
		val = strings.TrimPrefix(val, "#")
		if num, err := strconv.Atoi(val); err == nil && validPRs[num] {
			return num, true
		}
	}
	return 0, false
}

func matchTags(tags []string, branchToPR map[string]int, validPRs map[int]bool) (int, bool) {
	for i := 0; i < len(tags) && i < 50; i++ {
		tag := strings.TrimSpace(tags[i])
		if tag == "" {
			continue
		}
		if prNum, ok := matchTag(tag, branchToPR, validPRs); ok {
			return prNum, true
		}
	}
	return 0, false
}

func matchMetadata(meta json.RawMessage, branchToPR map[string]int, validPRs map[int]bool) (int, bool) {
	if len(meta) == 0 {
		return 0, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal(meta, &m); err != nil {
		return 0, false
	}
	if b, ok := m["branch"].(string); ok && b != "" {
		if prNum, found := branchToPR[b]; found {
			return prNum, true
		}
	}
	return matchMetadataPR(m, validPRs)
}

func matchMetadataPR(m map[string]interface{}, validPRs map[int]bool) (int, bool) {
	if prVal, ok := m["pull_request"]; ok {
		if num := parseInterfaceInt(prVal); num > 0 && validPRs[num] {
			return num, true
		}
	}
	if prVal, ok := m["pr"]; ok {
		if num := parseInterfaceInt(prVal); num > 0 && validPRs[num] {
			return num, true
		}
	}
	return 0, false
}

func parseInterfaceInt(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		clean := strings.TrimPrefix(val, "#")
		n, err := strconv.Atoi(clean)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

func attributeEntry(entry *rawSpendEntry, branchToPR map[string]int, validPRs map[int]bool) (int, bool) {
	if entry.Branch != "" {
		if prNum, ok := branchToPR[entry.Branch]; ok {
			return prNum, true
		}
	}
	if prNum := parseRawPR(entry.PullRequest); prNum > 0 && validPRs[prNum] {
		return prNum, true
	}
	if prNum := parseRawPR(entry.PR); prNum > 0 && validPRs[prNum] {
		return prNum, true
	}
	if prNum, ok := matchTags(entry.Tags, branchToPR, validPRs); ok {
		return prNum, true
	}
	if prNum, ok := matchMetadata(entry.Metadata, branchToPR, validPRs); ok {
		return prNum, true
	}
	return 0, false
}

func entrySpend(e *rawSpendEntry) float64 {
	if e.Spend > 0 {
		return e.Spend
	}
	return e.Cost
}

func entryTokens(e *rawSpendEntry) int64 {
	if e.Tokens > 0 {
		return e.Tokens
	}
	if e.InputTokens > 0 {
		return e.InputTokens
	}
	return 0
}

func processSpendEntry(entry *rawSpendEntry, classifier *Classifier, branchToPR map[string]int, validPRs map[int]bool, report *SpendReport) {
	spend := entrySpend(entry)
	prNum, ok := attributeEntry(entry, branchToPR, validPRs)
	if ok {
		if spend > 0 {
			report.SpendByPRNumber[prNum] += spend
		}
		report.RequestsByPRNumber[prNum]++
		if classifier != nil && classifier.IsLocal(entry.Model) {
			report.LocalRequestsByPRNumber[prNum]++
		}
		tokens := entryTokens(entry)
		if tokens > 0 && classifier != nil && classifier.IsFrontier(entry.Model) {
			report.FrontierTokensByPRNumber[prNum] += tokens
		}
	} else if spend > 0 {
		report.Unattributed += spend
	}
}

// ReadSpendLogJSONL decodes JSON lines from reader and attributes spend to PRs or unattributed bucket.
func ReadSpendLogJSONL(ctx context.Context, r io.Reader, classifier *Classifier, branchToPR map[string]int, validPRs map[int]bool, report *SpendReport) error {
	limitedReader := &countReader{r: io.LimitReader(r, MaxFileBytes+1)}
	scanner := bufio.NewScanner(limitedReader)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineCount := 0
	for ; lineCount < MaxSourceLines && scanner.Scan(); lineCount++ {
		if lineCount%1000 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		lineBytes := bytes.TrimSpace(scanner.Bytes())
		if len(lineBytes) == 0 {
			continue
		}
		var entry rawSpendEntry
		if err := json.Unmarshal(lineBytes, &entry); err != nil {
			return fmt.Errorf("spend log line %d: %w", lineCount+1, err)
		}
		processSpendEntry(&entry, classifier, branchToPR, validPRs, report)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if limitedReader.count > MaxFileBytes {
		return fmt.Errorf("spend log exceeds %d byte limit", MaxFileBytes)
	}
	if scanner.Scan() {
		return fmt.Errorf("spend log exceeds %d lines limit", MaxSourceLines)
	}
	return nil
}

func parseCSVHeader(header []string) (map[string]int, error) {
	indices := make(map[string]int, len(header))
	hasSpendCol := false
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		indices[key] = i
		if key == "spend" || key == "cost" || key == "model" {
			hasSpendCol = true
		}
	}
	if !hasSpendCol {
		return nil, fmt.Errorf("CSV header must include spend, cost or model columns, got %v", header)
	}
	return indices, nil
}

// ReadSpendLogCSV decodes CSV from reader and attributes spend to PRs or unattributed bucket.
func ReadSpendLogCSV(ctx context.Context, r io.Reader, classifier *Classifier, branchToPR map[string]int, validPRs map[int]bool, report *SpendReport) error {
	limitedReader := &countReader{r: io.LimitReader(r, MaxFileBytes+1)}
	csvReader := csv.NewReader(limitedReader)
	header, err := csvReader.Read()
	if err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}
	indices, err := parseCSVHeader(header)
	if err != nil {
		return err
	}

	lineCount := 0
	for ; lineCount < MaxSourceLines; lineCount++ {
		if lineCount%1000 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read CSV line %d: %w", lineCount+2, err)
		}
		entry := csvRecordToEntry(record, indices)
		processSpendEntry(&entry, classifier, branchToPR, validPRs, report)
	}
	if limitedReader.count > MaxFileBytes {
		return fmt.Errorf("spend log exceeds %d byte limit", MaxFileBytes)
	}
	if _, err := csvReader.Read(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("spend log exceeds %d lines limit", MaxSourceLines)
	}
	return nil
}

func extractCSVSpend(record []string, indices map[string]int) float64 {
	if idx, ok := indices["spend"]; ok && idx < len(record) {
		val, err := strconv.ParseFloat(strings.TrimSpace(record[idx]), 64)
		if err == nil {
			return val
		}
	}
	if idx, ok := indices["cost"]; ok && idx < len(record) {
		val, err := strconv.ParseFloat(strings.TrimSpace(record[idx]), 64)
		if err == nil {
			return val
		}
	}
	return 0
}

func extractCSVField(record []string, indices map[string]int, field string) string {
	if idx, ok := indices[field]; ok && idx < len(record) {
		return strings.TrimSpace(record[idx])
	}
	return ""
}

func extractCSVInt64(record []string, indices map[string]int, field string) int64 {
	if idx, ok := indices[field]; ok && idx < len(record) {
		val, err := strconv.ParseInt(strings.TrimSpace(record[idx]), 10, 64)
		if err == nil {
			return val
		}
	}
	return 0
}

func extractCSVPR(record []string, indices map[string]int) json.RawMessage {
	if idx, ok := indices["pull_request"]; ok && idx < len(record) {
		return json.RawMessage(strconv.Quote(strings.TrimSpace(record[idx])))
	}
	if idx, ok := indices["pr"]; ok && idx < len(record) {
		return json.RawMessage(strconv.Quote(strings.TrimSpace(record[idx])))
	}
	return nil
}

func extractCSVTags(record []string, indices map[string]int) []string {
	if idx, ok := indices["tags"]; ok && idx < len(record) {
		tagsStr := strings.TrimSpace(record[idx])
		if tagsStr != "" {
			return strings.Split(tagsStr, ",")
		}
	}
	return nil
}

func csvRecordToEntry(record []string, indices map[string]int) rawSpendEntry {
	var entry rawSpendEntry
	entry.Spend = extractCSVSpend(record, indices)
	entry.Model = extractCSVField(record, indices, "model")
	entry.Tokens = extractCSVInt64(record, indices, "tokens")
	entry.InputTokens = extractCSVInt64(record, indices, "input_tokens")
	entry.Branch = extractCSVField(record, indices, "branch")
	entry.PullRequest = extractCSVPR(record, indices)
	entry.Tags = extractCSVTags(record, indices)
	if idx, ok := indices["metadata"]; ok && idx < len(record) {
		entry.Metadata = json.RawMessage(record[idx])
	}
	return entry
}

func buildPRLookupMaps(prs []forge.MergedPullRequest) (map[string]int, map[int]bool) {
	branchToPR := make(map[string]int, len(prs))
	validPRs := make(map[int]bool, len(prs))
	for _, pr := range prs {
		validPRs[pr.Number] = true
		if pr.HeadBranch != "" {
			branchToPR[pr.HeadBranch] = pr.Number
		}
	}
	return branchToPR, validPRs
}

func parseSpendLogStream(ctx context.Context, r io.Reader, classifier *Classifier, branchToPR map[string]int, validPRs map[int]bool, report *SpendReport) error {
	bufReader := bufio.NewReader(r)
	peekBytes, peekErr := bufReader.Peek(100)
	if peekErr != nil && len(peekBytes) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(peekBytes)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return ReadSpendLogJSONL(ctx, bufReader, classifier, branchToPR, validPRs, report)
	}
	return ReadSpendLogCSV(ctx, bufReader, classifier, branchToPR, validPRs, report)
}

// ReadSpendLogFile reads a gateway spend-log export file (JSON lines or CSV).
func ReadSpendLogFile(ctx context.Context, path string, prs []forge.MergedPullRequest, classifier *Classifier) (reportResult *SpendReport, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, errors.New("empty spend log path")
	}
	f, err := os.Open(cleanPath) // #nosec G304 -- path is configured in manifest or CLI flag.
	if err != nil {
		return nil, fmt.Errorf("open spend log file %s: %w", cleanPath, err)
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()

	info, err := f.Stat()
	if err == nil && info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("spend log file %s exceeds %d byte limit", cleanPath, MaxFileBytes)
	}

	branchToPR, validPRs := buildPRLookupMaps(prs)
	report := &SpendReport{
		SpendByPRNumber:          make(map[int]float64),
		RequestsByPRNumber:       make(map[int]int),
		LocalRequestsByPRNumber:  make(map[int]int),
		FrontierTokensByPRNumber: make(map[int]int64),
	}
	if err := readSpendLogFormat(ctx, f, cleanPath, classifier, branchToPR, validPRs, report); err != nil {
		return nil, fmt.Errorf("read spend log file %s: %w", cleanPath, err)
	}
	return report, nil
}

func readSpendLogFormat(ctx context.Context, f io.Reader, path string, classifier *Classifier, branchToPR map[string]int, validPRs map[int]bool, report *SpendReport) error {
	if strings.HasSuffix(path, ".jsonl") {
		return ReadSpendLogJSONL(ctx, f, classifier, branchToPR, validPRs, report)
	}
	if strings.HasSuffix(path, ".csv") {
		return ReadSpendLogCSV(ctx, f, classifier, branchToPR, validPRs, report)
	}
	return parseSpendLogStream(ctx, f, classifier, branchToPR, validPRs, report)
}
