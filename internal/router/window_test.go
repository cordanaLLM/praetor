package router

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

const windowCatalog = `version: 1
lanes:
  claude-sub:
    harness: claude-code
    command: [claude, -p, "{task}"]
    account: max-a
  api:
    harness: api-harness
    command: [api-harness, --model, "{target}"]
tiers:
  work:
    target_tasks: [implement]
    models:
      - {id: sub-model, family: anthropic, lane: claude-sub, cost_per_m_in: 0, cost_per_m_out: 0}
      - {id: api-model, family: openai, lane: api, cost_per_m_in: 3, cost_per_m_out: 15}
  subonly:
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

const windowRecordID = "claude-code/five_hour"

var windowNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func loadWindowCatalog(t *testing.T, body string) *RoutingConfig {
	t.Helper()
	cfg, err := ParseRoutingConfig([]byte(body), "window.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// windowReading is a fresh subscription reading of the fixture's record.
func windowReading(used float64, age, resetsIn time.Duration) catalog.Record {
	resets := windowNow.Add(resetsIn)
	return catalog.Record{
		ModelID:     windowRecordID,
		AccessPath:  catalog.AccessSubscriptionCLI,
		Provenance:  catalog.Provenance{Source: "claude-local", FetchedAt: windowNow.Add(-age), Kind: catalog.KindMeasured},
		UsageWindow: &catalog.UsageWindow{UsedPercent: &used, ResetsAt: &resets},
	}
}

func windowGovernor(t *testing.T, cfg *RoutingConfig, records ...catalog.Record) *WindowGovernor {
	t.Helper()
	governor, err := NewWindowGovernor(cfg, catalog.Snapshot{GeneratedAt: windowNow, Records: records}, windowNow)
	if err != nil {
		t.Fatal(err)
	}
	return governor
}

func windowArbiter(t *testing.T, governor *WindowGovernor) *ModelCapacityArbiter {
	t.Helper()
	arbiter := NewModelCapacityArbiter(loadWindowCatalog(t, windowCatalog), nil)
	arbiter.Windows = governor
	return arbiter
}

func TestWindowAboveStopRefusesAndNamesTheWindow(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	arbiter := windowArbiter(t, windowGovernor(t, cfg, windowReading(85, time.Minute, 2*time.Hour)))
	_, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "review"})
	if !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("a window above stop must refuse the dispatch: %v", err)
	}
	want := "window claude-code/five_hour (account max-a) stop: used 85.0% >= stop 80.0%, resets 2026-10-13T14:00:00Z, source claude-local age 60s"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal must name the window\nwant %q\ngot  %v", want, err)
	}
	route := routeTask(t, arbiter, TaskRequest{Task: "implement"})
	if route.Model.ID != "api-model" || route.Window != nil {
		t.Fatalf("work must move to the ungoverned lane: %+v", route)
	}
	if len(route.Skipped) != 1 || route.Skipped[0].Model != "sub-model" || !strings.Contains(route.Skipped[0].Reason, "stop") {
		t.Fatalf("the refused candidate must be listed: %+v", route.Skipped)
	}
}

func TestWindowBelowWarnAdmitsWithPacingSignal(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	route := routeTask(t, windowArbiter(t, windowGovernor(t, cfg, windowReading(40, time.Minute, 2*time.Hour))), TaskRequest{Task: "implement"})
	if route.Model.ID != "sub-model" || route.Window == nil || route.Window.Level != WindowOK {
		t.Fatalf("a reading below warn must admit the subscription lane: %+v", route)
	}
	if pace := route.Window.PacePercentPerAgentHour; pace == nil || math.Abs(*pace-20) > 1e-9 {
		t.Fatalf("pace = (80-40)/2h/1 agent = 20, got %v", pace)
	}
	if route.Window.Advice != "" {
		t.Fatalf("ok carries no advice: %q", route.Window.Advice)
	}
}

func TestWindowUnknownRefuses(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	stale := windowReading(10, 601*time.Second, 2*time.Hour)
	cases := map[string]struct {
		governor *WindowGovernor
		reason   string
	}{
		"stale":           {windowGovernor(t, cfg, stale), "unknown: reading is stale, older than 600s"},
		"missing":         {windowGovernor(t, cfg), "unknown: no subscription_cli reading with a usage window"},
		"no readings":     {nil, "unknown: no window readings supplied"},
		"other record id": {windowGovernor(t, cfg, renamedReading(windowReading(10, 0, time.Hour), "codex-cli/codex")), "unknown: no subscription_cli reading"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := windowArbiter(t, tc.governor).SelectForTask(context.Background(), TaskRequest{Task: "review"})
			if !errors.Is(err, ErrNoEligibleModel) || !strings.Contains(err.Error(), "(account max-a) "+tc.reason) {
				t.Fatalf("want refusal %q, got %v", tc.reason, err)
			}
		})
	}
}

func renamedReading(record catalog.Record, id string) catalog.Record {
	record.ModelID = id
	return record
}

func TestWindowDecideBoundaries(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	nan := math.NaN()
	negative := -1.0
	minusOne := -1
	cases := []struct {
		name   string
		record catalog.Record
		level  WindowLevel
	}{
		{"just below warn", windowReading(64.999, 0, time.Hour), WindowOK},
		{"exactly warn", windowReading(65, 0, time.Hour), WindowWarn},
		{"just below stop", windowReading(79.999, 0, time.Hour), WindowWarn},
		{"exactly stop", windowReading(80, 0, time.Hour), WindowStop},
		{"age exactly the maximum", windowReading(10, 600*time.Second, time.Hour), WindowOK},
		{"age one second past the maximum", windowReading(10, 601*time.Second, time.Hour), WindowUnknown},
		{"stamped within the clock skew", windowReading(10, -time.Minute, time.Hour), WindowOK},
		{"stamped in the future", windowReading(10, -61*time.Second, time.Hour), WindowUnknown},
		{"reset exactly now", windowReading(10, 0, 0), WindowUnknown},
		{"reset passed", windowReading(10, 0, -time.Minute), WindowUnknown},
		{"no reset reported", withoutReset(windowReading(10, 0, time.Hour)), WindowOK},
		{"no used percent", withUsed(windowReading(10, 0, time.Hour), nil), WindowUnknown},
		{"used percent NaN", withUsed(windowReading(10, 0, time.Hour), &nan), WindowUnknown},
		{"negative burn", withBurn(windowReading(10, 0, time.Hour), &negative, nil), WindowUnknown},
		{"negative agents", withBurn(windowReading(10, 0, time.Hour), nil, &minusOne), WindowUnknown},
		{"api record is no window", withAccess(windowReading(10, 0, time.Hour), catalog.AccessAPI), WindowUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := windowGovernor(t, cfg, tc.record).Decide("max-a")
			if decision.Level != tc.level {
				t.Fatalf("level %s, want %s: %s", decision.Level, tc.level, decision.Reason)
			}
			if decision.Admits() != (tc.level == WindowOK || tc.level == WindowWarn) {
				t.Fatalf("Admits disagrees with level %s", decision.Level)
			}
			if (decision.Level == WindowWarn) != (decision.Advice != "") {
				t.Fatalf("only warn carries the pacing advice: %+v", decision)
			}
		})
	}
	if decision := windowGovernor(t, cfg, windowReading(10, 0, time.Hour)).Decide("undeclared"); decision.Level != WindowUnknown {
		t.Fatalf("an undeclared account must be unknown: %+v", decision)
	}
}

func withoutReset(record catalog.Record) catalog.Record {
	window := *record.UsageWindow
	window.ResetsAt = nil
	record.UsageWindow = &window
	return record
}

func withUsed(record catalog.Record, used *float64) catalog.Record {
	window := *record.UsageWindow
	window.UsedPercent = used
	record.UsageWindow = &window
	return record
}

func withBurn(record catalog.Record, burn *float64, agents *int) catalog.Record {
	window := *record.UsageWindow
	window.BurnPercentPerHour, window.ActiveAgents = burn, agents
	record.UsageWindow = &window
	return record
}

func withAccess(record catalog.Record, access catalog.AccessPath) catalog.Record {
	record.AccessPath = access
	return record
}

// TestWindowBurnScalesWithActiveAgents replays the fixture of the #1142 thread, converted to
// percent of a 650-unit window: used 100, measured burn 20/h, 280 minutes to the reset, and a
// per-agent floor of 9 units per agent-hour.
func TestWindowBurnScalesWithActiveAgents(t *testing.T) {
	const ceiling = 650.0
	used, burn := 100/ceiling*100, 20/ceiling*100
	cases := []struct {
		agents int
		floor  float64
		level  WindowLevel
	}{
		{1, 9 / ceiling * 100, WindowOK},
		{30, 9 / ceiling * 100, WindowWarn},
		{120, 9 / ceiling * 100, WindowStop},
		{120, 0, WindowOK},
	}
	for _, tc := range cases {
		body := windowCatalog
		if tc.floor > 0 {
			body = strings.Replace(body, "  window:\n", "  window:\n    per_agent_percent_per_hour: 1.3846153846153846\n", 1)
		}
		cfg := loadWindowCatalog(t, body)
		agents := tc.agents
		record := withBurn(windowReading(used, 0, 280*time.Minute), &burn, &agents)
		decision := windowGovernor(t, cfg, record).Decide("max-a")
		if decision.Level != tc.level {
			t.Fatalf("%d agents, floor %v: level %s, want %s (%s)", tc.agents, tc.floor, decision.Level, tc.level, decision.Reason)
		}
		wantPace := (80 - used) / (280.0 / 60) / float64(tc.agents)
		if decision.PacePercentPerAgentHour == nil || math.Abs(*decision.PacePercentPerAgentHour-wantPace) > 1e-9 {
			t.Fatalf("%d agents: pace %v, want %v", tc.agents, decision.PacePercentPerAgentHour, wantPace)
		}
	}
}

func TestWindowPolicyValidation(t *testing.T) {
	window := "  window:\n    accounts:\n      max-a: {window_record: claude-code/five_hour}\n"
	cases := []struct {
		name, from, to string
		valid          bool
	}{
		{"defaults", "", "", true},
		{"explicit lines", window, "  window:\n    warn_percent: 50\n    stop_percent: 100\n    accounts:\n      max-a: {window_record: r}\n", true},
		{"warn equals stop", window, "  window:\n    warn_percent: 80\n    stop_percent: 80\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"stop below default warn", window, "  window:\n    stop_percent: 60\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"stop above 100", window, "  window:\n    warn_percent: 50\n    stop_percent: 100.5\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"age at its bound", window, "  window:\n    max_reading_age_seconds: 86400\n    accounts:\n      max-a: {window_record: r}\n", true},
		{"age past its bound", window, "  window:\n    max_reading_age_seconds: 86401\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"negative age", window, "  window:\n    max_reading_age_seconds: -1\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"negative horizon", window, "  window:\n    stop_horizon_minutes: -1\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"floor at its bound", window, "  window:\n    per_agent_percent_per_hour: 100\n    accounts:\n      max-a: {window_record: r}\n", true},
		{"negative floor", window, "  window:\n    per_agent_percent_per_hour: -0.5\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"unknown key", window, "  window:\n    stop_pct: 80\n    accounts:\n      max-a: {window_record: r}\n", false},
		{"empty window record", window, "  window:\n    accounts:\n      max-a: {window_record: \"\"}\n", false},
		{"lane names undeclared account", window, "  window:\n    accounts:\n      other: {window_record: r}\n", false},
		{"lane account without window", window, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(windowCatalog, tc.from, tc.to, 1)
			if tc.from != "" && body == windowCatalog {
				t.Fatal("fixture replacement did not apply")
			}
			_, err := ParseRoutingConfig([]byte(body), "window.yaml")
			if tc.valid && err != nil {
				t.Fatalf("valid policy refused: %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidRoutingConfig) {
				t.Fatalf("invalid policy accepted or wrong error: %v", err)
			}
		})
	}
}

func TestNewWindowGovernorInputs(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	plain := loadLaneCatalog(t)
	if _, err := NewWindowGovernor(plain, catalog.Snapshot{}, windowNow); err == nil {
		t.Fatal("a config without governance.window must be refused")
	}
	if _, err := NewWindowGovernor(cfg, catalog.Snapshot{}, time.Time{}); err == nil {
		t.Fatal("a zero time must be refused")
	}
	broken := windowReading(10, 0, time.Hour)
	broken.Provenance.Source = ""
	if _, err := NewWindowGovernor(cfg, catalog.Snapshot{Records: []catalog.Record{broken}}, windowNow); err == nil {
		t.Fatal("a record without provenance must be refused")
	}
	older, newer := windowReading(90, 5*time.Minute, time.Hour), windowReading(10, time.Minute, time.Hour)
	for _, order := range [][]catalog.Record{{older, newer}, {newer, older}} {
		if decision := windowGovernor(t, cfg, order...).Decide("max-a"); decision.Level != WindowOK {
			t.Fatalf("the newer reading must win whatever the order: %+v", decision)
		}
	}
}

func TestLoadWindowSnapshot(t *testing.T) {
	cfg := loadWindowCatalog(t, windowCatalog)
	data, err := catalog.Snapshot{GeneratedAt: windowNow, Records: []catalog.Record{windowReading(70, 0, time.Hour)}}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "windows.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadWindowSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	governor, err := NewWindowGovernor(cfg, snapshot, windowNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision := governor.Decide("max-a"); decision.Level != WindowWarn {
		t.Fatalf("read-back reading at 70%% must warn: %+v", decision)
	}
	if _, err := LoadWindowSnapshot(context.Background(), filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing snapshot must fail")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"records": [{"model_id": ""}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWindowSnapshot(context.Background(), bad); err == nil {
		t.Fatal("an invalid snapshot must fail")
	}
}
