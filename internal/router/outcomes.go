package router

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

// Outcome is one measured result of a routed task, keyed by its task label. The log of these
// records is the local source the efficiency ledger reads to measure routing, not guess it.
type Outcome struct {
	Time       time.Time `json:"time"`
	Task       string    `json:"task"`
	Lane       string    `json:"lane,omitempty"`
	Target     string    `json:"target"`
	Result     string    `json:"result"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Note       string    `json:"note,omitempty"`
}

// ValidateOutcome refuses a record the log must not hold.
func ValidateOutcome(o Outcome) error {
	if !ValidTaskLabel(o.Task) || !routingName(o.Target) {
		return errors.New("outcome needs a valid task label and target")
	}
	if o.Lane != "" && !routingName(o.Lane) {
		return errors.New("outcome lane is not a valid name")
	}
	switch o.Result {
	case OutcomeOK, OutcomeFail, OutcomeTimeout:
	default:
		return fmt.Errorf("outcome result %q must be ok, fail or timeout", o.Result)
	}
	if o.Time.IsZero() || o.DurationMS < 0 || len(o.Note) > maxOutcomeNoteBytes {
		return errors.New("outcome needs a time, a nonnegative duration and a note of at most 256 bytes")
	}
	return nil
}

// AppendOutcome adds one record to the JSON Lines log at path, creating the log and its
// directory. The log is private to the operator (mode 0600) and is never rewritten.
func AppendOutcome(ctx context.Context, path string, o Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
		if err := ValidateOutcome(o); err != nil {
			return nil, fmt.Errorf("outcome log line %d: %w", len(outcomes)+1, err)
		}
		outcomes = append(outcomes, o)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read outcome log: %w", err)
	}
	return outcomes, nil
}
