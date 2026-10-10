package router

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// MaxOutcomeLines bounds the records one read of the outcome log returns.
	MaxOutcomeLines = 100_000
	// maxOutcomeLineBytes bounds one record, so one append is a single small write.
	maxOutcomeLineBytes = 4096
	maxOutcomeNoteBytes = 256
)

// Outcome results: how a routed task ended.
const (
	OutcomeOK      = "ok"
	OutcomeFail    = "fail"
	OutcomeTimeout = "timeout"
)

// RunIdentity records the provenance, execution harness, and configuration digests of a run.
type RunIdentity struct {
	PhysicalModel  string   `json:"physical_model"`
	Harness        string   `json:"harness"`
	HarnessVersion string   `json:"harness_version"`
	PromptDigest   string   `json:"prompt_digest"`
	ContextDigest  string   `json:"context_digest"`
	ContextBytes   int64    `json:"context_bytes"`
	ToolSet        []string `json:"tool_set,omitempty"`
	PriorRounds    int      `json:"prior_rounds,omitempty"`
	Retries        int      `json:"retries,omitempty"`
	CostEstimate   float64  `json:"cost_estimate,omitempty"`
}

// Key computes a deterministic cryptographic hash identifying this exact run configuration.
// Two runs differing only in their prompt template (or brief) digest yield distinct keys.
// Per-attempt values (retries, prior rounds, cost estimate, context bytes) are excluded.
func (id RunIdentity) Key() string {
	h := sha256.New()
	tools := make([]string, len(id.ToolSet))
	copy(tools, id.ToolSet)
	sort.Strings(tools)
	h.Write([]byte("v1\x00"))
	h.Write([]byte(id.PhysicalModel))
	h.Write([]byte{0})
	h.Write([]byte(id.Harness))
	h.Write([]byte{0})
	h.Write([]byte(id.HarnessVersion))
	h.Write([]byte{0})
	h.Write([]byte(id.PromptDigest))
	h.Write([]byte{0})
	h.Write([]byte(id.ContextDigest))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(tools, ",")))
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// ValidateRunIdentity verifies that all mandatory identity fields are present and valid.
func ValidateRunIdentity(id RunIdentity) error {
	if err := validateIdentityNames(id); err != nil {
		return err
	}
	if err := validateIdentityDigests(id); err != nil {
		return err
	}
	return validateIdentityMetrics(id)
}

func validateIdentityNames(id RunIdentity) error {
	if id.PhysicalModel == "" || !routingName(id.PhysicalModel) {
		return errors.New("identity requires a valid physical model name")
	}
	if id.Harness == "" || !routingName(id.Harness) {
		return errors.New("identity requires a valid harness name")
	}
	if id.HarnessVersion == "" || len(id.HarnessVersion) > maxOutcomeNoteBytes {
		return errors.New("identity requires a nonempty harness version")
	}
	return validateIdentityTools(id.ToolSet)
}

func validateIdentityTools(tools []string) error {
	if len(tools) > MaxRoutingTags {
		return fmt.Errorf("identity tool set holds %d tools, exceeding bound of %d", len(tools), MaxRoutingTags)
	}
	seen := make(map[string]bool, len(tools))
	for i := 0; i < len(tools); i++ {
		tool := tools[i]
		if tool == "" || len(tool) > maxRoutingNameBytes || strings.IndexFunc(tool, unicode.IsSpace) >= 0 || strings.IndexFunc(tool, unicode.IsControl) >= 0 {
			return errors.New("identity tool set must contain valid tool names")
		}
		if seen[tool] {
			return fmt.Errorf("identity tool set contains duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	return nil
}

func validateIdentityDigests(id RunIdentity) error {
	if id.PromptDigest == "" || len(id.PromptDigest) > maxOutcomeNoteBytes {
		return errors.New("identity requires a prompt template digest")
	}
	if id.ContextDigest == "" || len(id.ContextDigest) > maxOutcomeNoteBytes {
		return errors.New("identity requires a compiled context digest")
	}
	return nil
}

func validateIdentityMetrics(id RunIdentity) error {
	if id.ContextBytes < 0 {
		return errors.New("identity context bytes must be nonnegative")
	}
	if id.PriorRounds < 0 {
		return errors.New("identity prior rounds must be nonnegative")
	}
	if id.Retries < 0 {
		return errors.New("identity retries must be nonnegative")
	}
	if id.CostEstimate < 0 {
		return errors.New("identity cost estimate must be nonnegative")
	}
	return nil
}

// Outcome is one measured result of a routed task, keyed by its task label. The log of these
// records is the local source the efficiency ledger reads to measure routing, not guess it.
type Outcome struct {
	Time          time.Time   `json:"time"`
	Task          string      `json:"task"`
	Lane          string      `json:"lane,omitempty"`
	Target        string      `json:"target"`
	ResolvedModel string      `json:"resolved_model,omitempty"`
	Result        string      `json:"result"`
	DurationMS    int64       `json:"duration_ms,omitempty"`
	Note          string      `json:"note,omitempty"`
	Identity      RunIdentity `json:"identity"`
	IdentityKey   string      `json:"identity_key,omitempty"`
	ActualCost    *float64    `json:"actual_cost,omitempty"`
	EstimateError *float64    `json:"estimate_error,omitempty"`
	Branch        string      `json:"branch,omitempty"`
}

// Key returns the cryptographic key for the outcome's identity fields.
func (o Outcome) Key() string {
	if o.IdentityKey != "" {
		return o.IdentityKey
	}
	return o.Identity.Key()
}

// ValidateOutcome refuses a record the log must not hold.
func ValidateOutcome(o Outcome) error {
	if err := validateOutcomeBasics(o); err != nil {
		return err
	}
	return validateOutcomeIdentity(o)
}

func validateOutcomeBasics(o Outcome) error {
	if !ValidTaskLabel(o.Task) || !routingName(o.Target) {
		return errors.New("outcome needs a valid task label and target")
	}
	if o.Lane != "" && !routingName(o.Lane) {
		return errors.New("outcome lane is not a valid name")
	}
	if err := validateOutcomeResult(o.Result); err != nil {
		return err
	}
	return validateOutcomeDimensions(o)
}

func validateOutcomeDimensions(o Outcome) error {
	if o.Time.IsZero() || o.DurationMS < 0 || len(o.Note) > maxOutcomeNoteBytes {
		return errors.New("outcome needs a time, a nonnegative duration and a note of at most 256 bytes")
	}
	if o.ActualCost != nil && *o.ActualCost < 0 {
		return errors.New("outcome actual cost must be nonnegative")
	}
	return nil
}

func validateOutcomeResult(result string) error {
	switch result {
	case OutcomeOK, OutcomeFail, OutcomeTimeout:
		return nil
	default:
		return fmt.Errorf("outcome result %q must be ok, fail or timeout", result)
	}
}

func validateOutcomeIdentity(o Outcome) error {
	if err := ValidateRunIdentity(o.Identity); err != nil {
		return fmt.Errorf("outcome identity: %w", err)
	}
	resolved := o.ResolvedModel
	if resolved == "" {
		resolved = o.Identity.PhysicalModel
	}
	if resolved == "" || !routingName(resolved) {
		return errors.New("outcome needs a valid resolved model")
	}
	if o.ResolvedModel != "" && o.ResolvedModel != o.Identity.PhysicalModel {
		return errors.New("outcome resolved model must match identity physical model")
	}
	if o.IdentityKey != "" && o.IdentityKey != o.Identity.Key() {
		return errors.New("outcome identity key does not match identity fields")
	}
	return nil
}

// AppendOutcome adds one record to the JSON Lines log at path, creating the log and its
// directory. The log is private to the operator (mode 0600) and is never rewritten.
func AppendOutcome(ctx context.Context, path string, o Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	PrepareOutcome(&o)
	if err := ValidateOutcome(o); err != nil {
		return err
	}
	line, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	if len(line) >= maxOutcomeLineBytes {
		return errors.New("outcome record exceeds its byte bound")
	}
	return writeOutcomeRecord(path, line)
}

// PrepareOutcome initializes computed fields (resolved model, identity key, and estimate error)
// on an outcome record if they are not already set.
func PrepareOutcome(o *Outcome) {
	if o.ResolvedModel == "" && o.Identity.PhysicalModel != "" {
		o.ResolvedModel = o.Identity.PhysicalModel
	}
	if o.IdentityKey == "" && o.Identity.PhysicalModel != "" {
		o.IdentityKey = o.Identity.Key()
	}
	if o.EstimateError == nil && o.ActualCost != nil {
		diff := EstimateError(o.Identity.CostEstimate, *o.ActualCost)
		o.EstimateError = &diff
	}
}

func writeOutcomeRecord(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create outcome log directory: %w", err)
	}
	file, err := openOutcomeAppend(path)
	if err != nil {
		return fmt.Errorf("open outcome log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return errors.Join(fmt.Errorf("stat outcome log: %w", err), closeErr)
	}
	if !info.Mode().IsRegular() {
		closeErr := file.Close()
		return errors.Join(errors.New("outcome log must be a regular file"), closeErr)
	}
	_, writeErr := file.Write(append(line, '\n'))
	return errors.Join(writeErr, file.Close())
}

// ReadOutcomes returns the records of the log at path in file order. A missing log is an empty
// history. A line that does not decode, or a log past MaxOutcomeLines, is an error: a ledger
// must not average over records it could not read.
func ReadOutcomes(ctx context.Context, path string) (outcomes []Outcome, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Outcome{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect outcome log: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("outcome log must be a regular file")
	}
	file, err := openRoutingInput(path)
	if err != nil {
		return nil, fmt.Errorf("open outcome log: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(before, actual) {
		return nil, errors.New("outcome log changed while opening")
	}
	return scanOutcomeRecords(ctx, file)
}

func scanOutcomeRecords(ctx context.Context, file *os.File) ([]Outcome, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, maxOutcomeLineBytes), maxOutcomeLineBytes)
	outcomes := make([]Outcome, 0)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(outcomes) >= MaxOutcomeLines {
			return nil, fmt.Errorf("outcome log holds more than %d records", MaxOutcomeLines)
		}
		var o Outcome
		if err := json.Unmarshal(scanner.Bytes(), &o); err != nil {
			return nil, fmt.Errorf("outcome log line %d: %w", len(outcomes)+1, err)
		}
		if err := validateOutcomeBasics(o); err != nil {
			return nil, fmt.Errorf("outcome log line %d: %w", len(outcomes)+1, err)
		}
		if o.Identity.PhysicalModel != "" {
			if err := validateOutcomeIdentity(o); err != nil {
				return nil, fmt.Errorf("outcome log line %d: %w", len(outcomes)+1, err)
			}
		}
		outcomes = append(outcomes, o)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read outcome log: %w", err)
	}
	return outcomes, nil
}

// EstimateError calculates actual cost minus estimated cost.
func EstimateError(estimated, actual float64) float64 {
	return actual - estimated
}

// FormatEstimateError renders the estimate error as signed currency and optional percentage.
func FormatEstimateError(estimated, actual float64) string {
	diff := EstimateError(estimated, actual)
	if estimated > 0 {
		return fmt.Sprintf("%+.4f (%+.1f%%)", diff, (diff/estimated)*100)
	}
	return fmt.Sprintf("%+.4f", diff)
}

// LaneReconciliation summarizes cost estimates, actual spend, and estimation errors for one lane.
type LaneReconciliation struct {
	Lane          string   `json:"lane"`
	Runs          int      `json:"runs"`
	MeasuredRuns  int      `json:"measured_runs"`
	EstimatedCost float64  `json:"estimated_cost"`
	ActualCost    float64  `json:"actual_cost"`
	EstimateError float64  `json:"estimate_error"`
	ErrorRatio    *float64 `json:"error_ratio,omitempty"`
}

// ReconcileLanes aggregates estimated and actual costs across outcomes grouped by lane.
func ReconcileLanes(outcomes []Outcome) []LaneReconciliation {
	if len(outcomes) == 0 {
		return []LaneReconciliation{}
	}
	groups := make(map[string]*LaneReconciliation)
	for i := 0; i < len(outcomes) && i < MaxOutcomeLines; i++ {
		o := outcomes[i]
		lane := o.Lane
		if lane == "" {
			lane = "default"
		}
		entry, ok := groups[lane]
		if !ok {
			entry = &LaneReconciliation{Lane: lane}
			groups[lane] = entry
		}
		entry.Runs++
		if o.ActualCost != nil && o.Identity.PhysicalModel != "" {
			entry.MeasuredRuns++
			entry.EstimatedCost += o.Identity.CostEstimate
			entry.ActualCost += *o.ActualCost
		}
	}
	return finalizeLaneReconciliations(groups)
}

func finalizeLaneReconciliations(groups map[string]*LaneReconciliation) []LaneReconciliation {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]LaneReconciliation, 0, len(names))
	for i := 0; i < len(names); i++ {
		entry := groups[names[i]]
		if entry.MeasuredRuns > 0 {
			diff := EstimateError(entry.EstimatedCost, entry.ActualCost)
			entry.EstimateError = diff
			if entry.EstimatedCost > 0 {
				ratio := diff / entry.EstimatedCost
				entry.ErrorRatio = &ratio
			}
		}
		result = append(result, *entry)
	}
	return result
}

// RenderLaneReconciliation formats the per-lane estimate-error metric table.
func RenderLaneReconciliation(reconciliations []LaneReconciliation) string {
	if len(reconciliations) == 0 {
		return "no outcomes recorded for reconciliation\n"
	}
	var sb strings.Builder
	for i := 0; i < len(reconciliations); i++ {
		r := reconciliations[i]
		if r.MeasuredRuns == 0 {
			fmt.Fprintf(&sb, "lane %s: estimate-error not measured (runs: %d)\n", r.Lane, r.Runs)
			continue
		}
		fmt.Fprintf(&sb, "lane %s: estimate-error %s (estimated: $%.4f, actual: $%.4f, runs: %d)\n",
			r.Lane, FormatEstimateError(r.EstimatedCost, r.ActualCost), r.EstimatedCost, r.ActualCost, r.Runs)
	}
	return sb.String()
}
