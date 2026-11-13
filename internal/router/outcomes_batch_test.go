package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// cancelOnOutcomeCheck deterministically expires a context after validation and file setup.
type cancelOnOutcomeCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
	at     int
}

// Err cancels at the selected checkpoint without timing-dependent sleeps or filesystem hooks.
func (c *cancelOnOutcomeCheck) Err() error {
	c.checks++
	if c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

// TestAppendOutcomesCancellationBeforeWrite refuses single and batched appends after file setup.
func TestAppendOutcomesCancellationBeforeWrite(t *testing.T) {
	for variant := 0; variant < 2; variant++ {
		t.Run([]string{"single", "batch"}[variant], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "outcomes.jsonl")
			before := []byte("existing\n")
			if err := contextopt.WriteSnapshot(t.Context(), path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &cancelOnOutcomeCheck{Context: base, cancel: cancel, at: 3 + variant}
			var err error
			if variant == 0 {
				err = AppendOutcome(ctx, path, sampleOutcome("review", "ok"))
			} else {
				err = AppendOutcomes(ctx, path, []Outcome{sampleOutcome("review", "ok")})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("append after expiry: %v (checks=%d)", err, ctx.checks)
			}
			after, readErr := contextopt.ReadSnapshot(t.Context(), path)
			if readErr != nil || string(after) != string(before) {
				t.Fatalf("expired append changed log: %s %v", after, readErr)
			}
		})
	}
}

// TestAppendOutcomes_Positive_OneBatchInOrder writes every record of a batch after the existing log.
func TestAppendOutcomes_Positive_OneBatchInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	if err := AppendOutcome(t.Context(), path, sampleOutcome("review", "ok")); err != nil {
		t.Fatal(err)
	}
	if err := AppendOutcomes(t.Context(), path, []Outcome{sampleOutcome("review", "fail"), sampleOutcome("audit", "ok")}); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadOutcomes(t.Context(), path)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %d (%v), want 3", len(rows), err)
	}
	if rows[1].Result != "fail" || rows[2].Task != "audit" {
		t.Fatalf("batch order lost: %+v", rows)
	}
}

// TestAppendOutcomes_Negative_InvalidRecordWritesNothing refuses the whole batch on one bad record.
func TestAppendOutcomes_Negative_InvalidRecordWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	if err := AppendOutcome(t.Context(), path, sampleOutcome("review", "ok")); err != nil {
		t.Fatal(err)
	}
	before, err := contextopt.ReadSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	bad := sampleOutcome("review", "ok")
	bad.Target = ""
	if err := AppendOutcomes(t.Context(), path, []Outcome{sampleOutcome("review", "ok"), bad}); err == nil {
		t.Fatal("batch with an invalid record accepted")
	}
	after, err := contextopt.ReadSnapshot(t.Context(), path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("refused batch changed the log (%v)", err)
	}
}

// TestAppendOutcomes_Boundary_EmptyAndOversizedBatch writes nothing for an empty batch and refuses one past the bound.
func TestAppendOutcomes_Boundary_EmptyAndOversizedBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	if err := AppendOutcomes(t.Context(), path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty batch created the log: %v", err)
	}
	full := make([]Outcome, MaxOutcomeBatch)
	for i := range full {
		full[i] = sampleOutcome("review", "ok")
	}
	if err := AppendOutcomes(t.Context(), path, full); err != nil {
		t.Fatalf("batch at the bound refused: %v", err)
	}
	if err := AppendOutcomes(t.Context(), path, append(full, sampleOutcome("review", "ok"))); err == nil {
		t.Fatal("batch past the bound accepted")
	}
	rows, err := ReadOutcomes(t.Context(), path)
	if err != nil || len(rows) != MaxOutcomeBatch {
		t.Fatalf("rows = %d (%v), want %d", len(rows), err, MaxOutcomeBatch)
	}
}
