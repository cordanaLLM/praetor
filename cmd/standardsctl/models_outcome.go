package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// defaultOutcomeLog is the private, git-ignored log the efficiency ledger reads later.
const defaultOutcomeLog = ".workingdir/routing/outcomes.jsonl"

type modelOutcomeFlags struct {
	lane, target, result, note, log *string
	durationMS                      *int64
}

// addModelOutcomeFlags registers the models outcome flags; the task label reuses --task.
func addModelOutcomeFlags(fs *flag.FlagSet) modelOutcomeFlags {
	return modelOutcomeFlags{
		lane:       fs.String("lane", "", "models outcome: lane name the route returned"),
		target:     fs.String("target", "", "models outcome: alias or model ID the harness ran"),
		result:     fs.String("result", "", "models outcome: ok, fail or timeout"),
		note:       fs.String("note", "", "models outcome: short note, at most 256 bytes"),
		log:        fs.String("outcome-log", defaultOutcomeLog, "models outcome: JSON Lines log the record is appended to"),
		durationMS: fs.Int64("duration-ms", 0, "models outcome: how long the run took"),
	}
}

// handleModelsOutcome appends one measured result of a routed task to the outcome log and
// echoes the stored record, so a dispatcher records what it ran and reads back what was kept.
func handleModelsOutcome(ctx context.Context, task string, flags modelOutcomeFlags) error {
	outcome := router.Outcome{Time: time.Now().UTC(), Task: task, Lane: *flags.lane, Target: *flags.target,
		Result: *flags.result, DurationMS: *flags.durationMS, Note: *flags.note}
	if err := router.AppendOutcome(ctx, *flags.log, outcome); err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	data, err := json.MarshalIndent(outcome, "", "  ")
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
		return fmt.Errorf("write outcome: %w", err)
	}
	return nil
}
