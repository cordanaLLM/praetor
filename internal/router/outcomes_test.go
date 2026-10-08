package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleOutcome(task, result string) Outcome {
	return Outcome{Time: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Task: task, Lane: "gateway-coding", Target: "light", Result: result, DurationMS: 1200}
}

func TestOutcomeLogAppendsAndReadsBackInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "outcomes.jsonl")
	ctx := context.Background()
	for _, o := range []Outcome{sampleOutcome("stubs", OutcomeOK), sampleOutcome("synthesis", OutcomeFail), sampleOutcome("stubs", OutcomeTimeout)} {
		if err := AppendOutcome(ctx, path, o); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadOutcomes(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Result != OutcomeOK || got[1].Task != "synthesis" || got[2].Result != OutcomeTimeout || got[0].DurationMS != 1200 {
		t.Fatalf("readback wrong: %+v", got)
	}
}

func TestOutcomeLogPermissionsArePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "outcomes.jsonl")
	if err := AppendOutcome(context.Background(), path, sampleOutcome("stubs", OutcomeOK)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || (info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("log must be private: %v %v", info, err)
	}
}

func TestOutcomeLogMissingIsEmptyHistory(t *testing.T) {
	got, err := ReadOutcomes(context.Background(), filepath.Join(t.TempDir(), "absent.jsonl"))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing log: %v %v", got, err)
	}
}

func TestOutcomeRefusals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcomes.jsonl")
	ctx := context.Background()
	bad := map[string]func(*Outcome){
		"no task":         func(o *Outcome) { o.Task = "" },
		"padded task":     func(o *Outcome) { o.Task = " stubs" },
		"no target":       func(o *Outcome) { o.Target = "" },
		"unknown result":  func(o *Outcome) { o.Result = "great" },
		"no time":         func(o *Outcome) { o.Time = time.Time{} },
		"negative time":   func(o *Outcome) { o.DurationMS = -1 },
		"oversized note":  func(o *Outcome) { o.Note = strings.Repeat("n", maxOutcomeNoteBytes+1) },
		"invalid lane":    func(o *Outcome) { o.Lane = "a\nb" },
		"empty lane name": func(o *Outcome) { o.Lane = " " },
	}
	for name, mutate := range bad {
		o := sampleOutcome("stubs", OutcomeOK)
		mutate(&o)
		if err := AppendOutcome(ctx, path, o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a refused record created the log")
	}
	atBound := sampleOutcome("stubs", OutcomeOK)
	atBound.Note = strings.Repeat("n", maxOutcomeNoteBytes)
	if err := AppendOutcome(ctx, path, atBound); err != nil {
		t.Fatalf("note at the bound refused: %v", err)
	}
	if err := AppendOutcome(ctx, dir, atBound); err == nil {
		t.Fatal("a directory accepted as the log")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := AppendOutcome(cancelled, path, atBound); err == nil {
		t.Fatal("canceled context ignored")
	}
}

func TestOutcomeLogCorruptLineIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	if err := AppendOutcome(context.Background(), path, sampleOutcome("stubs", OutcomeOK)); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOutcomes(context.Background(), path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("corrupt record must fail the read, naming its line: %v", err)
	}
}
