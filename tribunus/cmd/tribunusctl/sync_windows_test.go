package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

// These tests check the claude-local records at the tribunus/catalog
// boundary the router's window governor reads: a subscription_cli record
// whose usage_window carries used_percent and resets_at, or omits
// used_percent with the reason in absent. tribunus imports no praetor
// internal package, so the governor's own tests (internal/router) cover the
// other side of this contract.

// claudeLogLine is a synthetic assistant line; units are claude-opus-5-5 input at 4 per million tokens.
func claudeLogLine(at time.Time, id string, units float64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r-%s","sessionId":"s1","message":{"id":%q,"model":"claude-opus-5-5","usage":{"input_tokens":%d,"output_tokens":0}}}`,
		at.Format(time.RFC3339Nano), id, id, int64(units/4*1e6))
}

func claudeRejectedLine(at, reset time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":"limit","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":%d}}`,
		at.Format(time.RFC3339Nano), reset.Unix())
}

func writeClaudeLog(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func syncFiveHour(t *testing.T, claudeDir, stateDir string) catalog.Record {
	t.Helper()
	out := filepath.Join(t.TempDir(), "windows.json")
	args := []string{"--sources=claude-local", "--claude-projects-dir=" + claudeDir, "--claude-state-dir=" + stateDir, "--out=" + out}
	if err := runSync(args); err != nil {
		t.Fatalf("runSync() = %v", err)
	}
	snap, err := readSnapshotFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range snap.Records {
		if record.ModelID == "claude-code/five_hour" {
			if record.AccessPath != catalog.AccessSubscriptionCLI || record.Provenance.Source != "claude-local" || record.UsageWindow == nil {
				t.Fatalf("not a governor reading: %+v", record)
			}
			return record
		}
	}
	t.Fatalf("no claude-code/five_hour record in %+v", snap.Records)
	return catalog.Record{}
}

func TestClaudeLocalSnapshotCarriesTheGovernorReading(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(-time.Hour)
	calibrated := writeClaudeLog(t,
		claudeLogLine(now.Add(-3*time.Hour), "m1", 10),
		claudeRejectedLine(now.Add(-2*time.Hour), reset),
		claudeLogLine(now.Add(-30*time.Minute), "m2", 8.5))
	window := syncFiveHour(t, calibrated, t.TempDir()).UsageWindow
	if window.UsedPercent == nil || *window.UsedPercent < 84.9 || *window.UsedPercent > 85.1 {
		t.Fatalf("8.5 of 10 units: used_percent = %v, want 85", window.UsedPercent)
	}
	if window.ResetsAt == nil || !window.ResetsAt.After(now) {
		t.Fatalf("resets_at must lie ahead: %v", window.ResetsAt)
	}
	uncalibrated := syncFiveHour(t, writeClaudeLog(t, claudeLogLine(now.Add(-30*time.Minute), "m1", 2)), t.TempDir())
	if uncalibrated.UsageWindow.UsedPercent != nil || !strings.Contains(uncalibrated.Absent["used_percent"], "unknown") {
		t.Fatalf("without a rejection used_percent must be absent with the reason: %+v", uncalibrated)
	}
}

// TestClaudeCeilingSurvivesTheLogsThroughTheStore: the first sync sees the
// rejection and stores the ceiling; the second sees only later usage, as after
// the rejection has left the scanned logs, and still reads 30 % from the store.
func TestClaudeCeilingSurvivesTheLogsThroughTheStore(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(-time.Hour)
	state := t.TempDir()
	syncFiveHour(t, writeClaudeLog(t,
		claudeLogLine(now.Add(-3*time.Hour), "m1", 10),
		claudeRejectedLine(now.Add(-2*time.Hour), reset)), state)
	if _, err := os.Stat(filepath.Join(state, "claude-ceilings", "default.json")); err != nil {
		t.Fatalf("the first sync must store the ceiling: %v", err)
	}
	later := syncFiveHour(t, writeClaudeLog(t, claudeLogLine(now.Add(-20*time.Minute), "m2", 3)), state).UsageWindow
	if later.UsedPercent == nil || *later.UsedPercent < 29.9 || *later.UsedPercent > 30.1 {
		t.Fatalf("3 of a stored 10 units: used_percent = %v, want 30", later.UsedPercent)
	}
}
