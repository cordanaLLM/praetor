package router

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// DefaultOutcomeLogPath is the repository-relative, private and git-ignored outcome log that
// models outcome appends to and the efficiency ledger reads.
const DefaultOutcomeLogPath = ".workingdir/routing/outcomes.jsonl"

const (
	// MaxOutcomeLines bounds the records one read of the outcome log returns.
	MaxOutcomeLines = 100_000
	// MaxOutcomeTokens bounds a whole run's usage, including cached input tokens.
	MaxOutcomeTokens int64 = 1_000_000_000_000
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

// Outcome is one measured result of a routed task, keyed by its task label. The log of these
// records is the local source the efficiency ledger reads to measure routing, not guess it.
type Outcome struct {
	Time            time.Time   `json:"time"`
	Task            string      `json:"task"`
	Lane            string      `json:"lane,omitempty"`
	Target          string      `json:"target"`
	ResolvedModel   string      `json:"resolved_model,omitempty"`
	Result          string      `json:"result"`
	DurationMS      int64       `json:"duration_ms,omitempty"`
	Tokens          int64       `json:"tokens,omitempty"`
	Effort          string      `json:"effort,omitempty"`
	Note            string      `json:"note,omitempty"`
	Identity        RunIdentity `json:"identity"`
	IdentityKey     string      `json:"identity_key,omitempty"`
	ActualCost      *float64    `json:"actual_cost,omitempty"`
	EstimateError   *float64    `json:"estimate_error,omitempty"`
	Branch          string      `json:"branch,omitempty"`
	Arm             string      `json:"arm,omitempty"`
	Suite           string      `json:"suite,omitempty"`
	Cutoff          *time.Time  `json:"cutoff,omitempty"`
	SampleCount     int         `json:"sample_count,omitempty"`
	RetainedDefects *int        `json:"retained_defects,omitempty"`
	VerdictMatch    *bool       `json:"verdict_match,omitempty"`
	OrderSensitive  *bool       `json:"order_sensitive,omitempty"`
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
	if o.Tokens < 0 || o.Tokens > MaxOutcomeTokens {
		return fmt.Errorf("outcome tokens must be between 0 and %d", MaxOutcomeTokens)
	}
	if !validClaudeEffort(o.Effort) {
		return errors.New("outcome effort must be empty, low, medium, high, xhigh or max")
	}
	if o.Time.IsZero() || o.DurationMS < 0 || len(o.Note) > maxOutcomeNoteBytes {
		return errors.New("outcome needs a time, a nonnegative duration and a note of at most 256 bytes")
	}
	if !validCost(o.ActualCost) {
		return errors.New("outcome actual cost must be a finite nonnegative amount")
	}
	return validateOutcomeReplay(o)
}

func validateOutcomeReplay(o Outcome) error {
	if !validOptionalReplayName(o.Arm) || !validOptionalReplayName(o.Suite) {
		return errors.New("outcome arm and suite must be valid whitespace-free names")
	}
	if o.Cutoff != nil && o.Cutoff.IsZero() {
		return errors.New("outcome cutoff must be a nonzero timestamp")
	}
	if o.SampleCount < 0 || (o.RetainedDefects != nil && *o.RetainedDefects < 0) {
		return errors.New("outcome sample and retained defect counts must be nonnegative")
	}
	return nil
}

func validOptionalReplayName(name string) bool {
	return name == "" || (routingName(name) && strings.IndexFunc(name, unicode.IsSpace) < 0)
}

// validateOutcomeEstimateError refuses an estimate error that is not actual minus estimate, so
// no record carries an error computed against a missing estimate.
func validateOutcomeEstimateError(o Outcome) error {
	if o.EstimateError == nil {
		return nil
	}
	estimate, actual, ok := o.MeasuredCosts()
	if !ok {
		return errors.New("outcome estimate error needs both a cost estimate and an actual cost")
	}
	if *o.EstimateError != EstimateError(estimate, actual) {
		return errors.New("outcome estimate error must equal actual cost minus cost estimate")
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
	return validateOutcomeEstimateError(o)
}

// AppendOutcome adds one record to the JSON Lines log at path, creating the log and its
// directory. The log is private to the operator (mode 0600) and is never rewritten.
func AppendOutcome(ctx context.Context, path string, o Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := encodeOutcomeLine(o)
	if err != nil {
		return err
	}
	return writeOutcomeRecord(ctx, path, line)
}

// MaxOutcomeBatch bounds the records one AppendOutcomes call writes.
const MaxOutcomeBatch = 256

// AppendOutcomes appends records as one batch: every record is prepared, validated and encoded
// before anything is written, then all lines go to the log in a single append write. An invalid
// record therefore leaves the log exactly as it was, and the log keeps its own bounds
// (MaxOutcomeLines) instead of a whole-file rewrite bound.
func AppendOutcomes(ctx context.Context, path string, records []Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(records) > MaxOutcomeBatch {
		return fmt.Errorf("outcome batch of %d records exceeds its bound of %d", len(records), MaxOutcomeBatch)
	}
	var batch []byte
	for i := 0; i < len(records) && i < MaxOutcomeBatch; i++ {
		line, err := encodeOutcomeLine(records[i])
		if err != nil {
			return fmt.Errorf("outcome record %d: %w", i, err)
		}
		batch = append(batch, line...)
	}
	if len(batch) == 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeOutcomeRecord(ctx, path, batch)
}

// encodeOutcomeLine prepares and validates one record and returns its newline-terminated JSON line.
func encodeOutcomeLine(o Outcome) ([]byte, error) {
	PrepareOutcome(&o)
	if err := ValidateOutcome(o); err != nil {
		return nil, err
	}
	line, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("encode outcome: %w", err)
	}
	if len(line) >= maxOutcomeLineBytes {
		return nil, fmt.Errorf("outcome record of %d bytes exceeds its bound of %d bytes; a long tool list is identified by its digest and count, leave the full list out", len(line), maxOutcomeLineBytes-1)
	}
	return append(line, '\n'), nil
}

// PrepareOutcome initializes computed fields (resolved model, tool-set digest and count, identity
// key, and estimate error) on an outcome record if they are not already set. The estimate error
// is set only when the record carries both a cost estimate and an actual cost.
func PrepareOutcome(o *Outcome) {
	if o.ResolvedModel == "" && o.Identity.PhysicalModel != "" {
		o.ResolvedModel = o.Identity.PhysicalModel
	}
	if o.Identity.ToolSetDigest == "" && len(o.Identity.ToolSet) > 0 {
		if digest, count, err := DigestToolSet(o.Identity.ToolSet); err == nil {
			o.Identity.ToolSetDigest, o.Identity.ToolCount = digest, count
		}
	}
	if o.IdentityKey == "" && o.Identity.PhysicalModel != "" {
		o.IdentityKey = o.Identity.Key()
	}
	if estimate, actual, ok := o.MeasuredCosts(); ok && o.EstimateError == nil {
		diff := EstimateError(estimate, actual)
		o.EstimateError = &diff
	}
}

// writeOutcomeRecord appends newline-terminated lines to the regular outcome log in one write.
func writeOutcomeRecord(ctx context.Context, path string, lines []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return errors.Join(err, file.Close())
	}
	_, writeErr := file.Write(lines)
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

// validateReadOutcome validates a record read back from the log. Only a legacy record, one with
// none of the fields run identity added, skips the identity checks; any record carrying one of
// them is validated in full.
func validateReadOutcome(o Outcome) error {
	if err := validateOutcomeBasics(o); err != nil {
		return err
	}
	if isLegacyOutcome(o) {
		return nil
	}
	return validateOutcomeIdentity(o)
}

// isLegacyOutcome reports whether o has the shape written before run identity: time, task, lane,
// target, result, duration and note only.
func isLegacyOutcome(o Outcome) bool {
	return reflect.ValueOf(o.Identity).IsZero() && o.ResolvedModel == "" && o.IdentityKey == "" &&
		o.ActualCost == nil && o.EstimateError == nil && o.Branch == "" && !hasReplayFields(o)
}

func hasReplayFields(o Outcome) bool {
	return o.Arm != "" || o.Suite != "" || o.Cutoff != nil || o.SampleCount != 0 ||
		o.RetainedDefects != nil || o.VerdictMatch != nil || o.OrderSensitive != nil
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
		if err := validateReadOutcome(o); err != nil {
			return nil, fmt.Errorf("outcome log line %d: %w", len(outcomes)+1, err)
		}
		outcomes = append(outcomes, o)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read outcome log: %w", err)
	}
	return outcomes, nil
}
