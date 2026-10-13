package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

const governedRouting = `version: 1
lanes:
  claude-sub:
    harness: claude-code
    command: [claude, -p, "{task}"]
    account: max-a
tiers:
  work:
    target_tasks: [review]
    lane: claude-sub
    models:
      - {id: sub-review, family: anthropic, cost_per_m_in: 0, cost_per_m_out: 0}
governance:
  exhaustion_threshold_percent: 80
  window:
    accounts:
      max-a: {window_record: claude-code/five_hour}
`

// claudeFixture writes a synthetic session log: 10 units before a rejection
// in the window that reset an hour ago, then usedUnits in the current window.
func claudeFixture(t *testing.T, rejected bool, usedUnits float64) string {
	t.Helper()
	now := time.Now().UTC()
	line := func(at time.Time, id string, units float64) string {
		// claude-opus-5-5 input costs 4 per million tokens.
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r-%s","sessionId":"s1","message":{"id":%q,"model":"claude-opus-5-5","usage":{"input_tokens":%d,"output_tokens":0}}}`,
			at.Format(time.RFC3339Nano), id, id, int64(units/4*1e6))
	}
	reset := now.Add(-time.Hour)
	lines := []string{line(now.Add(-3*time.Hour), "m1", 10)}
	if rejected {
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":"limit","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":%d}}`,
			now.Add(-2*time.Hour).Format(time.RFC3339Nano), reset.Unix()))
	}
	lines = append(lines, line(now.Add(-30*time.Minute), "m2", usedUnits))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func governedRoute(t *testing.T, claudeDir string) (*router.TaskRoute, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "windows.json")
	if err := runSync([]string{"--sources=claude-local", "--claude-projects-dir=" + claudeDir, "--out=" + out}); err != nil {
		t.Fatalf("runSync() = %v", err)
	}
	cfg, err := router.ParseRoutingConfig([]byte(governedRouting), "routing.yaml")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := router.LoadWindowSnapshot(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	governor, err := router.NewWindowGovernor(cfg, snapshot, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	arbiter := router.NewModelCapacityArbiter(cfg, nil)
	arbiter.Windows = governor
	return arbiter.SelectForTask(context.Background(), router.TaskRequest{Task: "review"})
}

func TestClaudeLocalSnapshotDrivesTheWindowGovernor(t *testing.T) {
	route, err := governedRoute(t, claudeFixture(t, true, 2))
	if err != nil {
		t.Fatalf("2 of 10 units used must admit: %v", err)
	}
	if route.Window == nil || route.Window.Level != router.WindowOK || route.Window.Source != "claude-local" {
		t.Fatalf("want an ok decision from claude-local, got %+v", route.Window)
	}
	if _, err := governedRoute(t, claudeFixture(t, true, 8.5)); err == nil || !strings.Contains(err.Error(), "window claude-code/five_hour (account max-a) stop") {
		t.Fatalf("8.5 of 10 units used must refuse and name the window: %v", err)
	}
	if _, err := governedRoute(t, claudeFixture(t, false, 2)); err == nil || !strings.Contains(err.Error(), "unknown: reading has no used_percent") {
		t.Fatalf("without a rejection the ceiling is unknown and the lane must refuse: %v", err)
	}
}
