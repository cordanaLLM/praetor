package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/state"
)

func plantedClaudeOutcome(t *testing.T) (string, Canonical, correlationEntry) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, state.WorkingDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	canonical := Canonical{AgentID: "agent", TranscriptPath: plantClaudeTranscript(t, plantedClaudeUsage)}
	entry := correlationEntry{
		Task: "feature_implementation", ResolvedModel: "claude-haiku-4-5",
		CreatedAt:       time.Now().UTC().Add(-4 * time.Second).Unix(),
		ClaudeSubagent:  &router.ClaudeSubagentRoute{Model: "haiku", Effort: "low"},
		HandbackVerdict: "pass",
		Resolution:      config.Resolution{ManifestSHA256: digestOf("manifest")},
	}
	return root, canonical, entry
}

func TestClaudeOutcomeReceipt(t *testing.T) {
	root, canonical, entry := plantedClaudeOutcome(t)
	if err := appendClaudeOutcome(t.Context(), root, canonical, entry); err != nil {
		t.Fatal(err)
	}
	rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	assertClaudeOutcomeRoute(t, rows[0])
	assertClaudeOutcomeMeasurements(t, rows[0])
}

func assertClaudeOutcomeRoute(t *testing.T, o router.Outcome) {
	t.Helper()
	if o.Task != "feature_implementation" || o.Lane != "claude-subagent" || o.Target != "haiku" || o.Effort != "low" {
		t.Fatalf("route fields: %+v", o)
	}
	if o.Identity.Harness != "claude-code" || o.IdentityKey == "" {
		t.Fatalf("identity absent: %+v", o)
	}
}

func assertClaudeOutcomeMeasurements(t *testing.T, o router.Outcome) {
	t.Helper()
	if o.ResolvedModel != "claude-haiku-4-5" || o.Tokens != 98 || o.DurationMS < 3000 || o.Result != "ok" || o.Note != "" {
		t.Fatalf("measurement fields: %+v", o)
	}
}

func TestClaudeOutcomeVerdicts(t *testing.T) {
	for _, word := range []string{"pass", "green", "ok", "done", "fail", "red", "blocked", "partial", "unknown", ""} {
		t.Run(word, func(t *testing.T) {
			_, _, entry := plantedClaudeOutcome(t)
			entry.HandbackVerdict = word
			o := claudeOutcome(entry)
			want := router.OutcomeFail
			if slices.Contains([]string{"pass", "green", "ok", "done"}, word) {
				want = router.OutcomeOK
			}
			if o.Result != want || ((word == "unknown" || word == "") && !strings.HasPrefix(o.Note, "verdict ")) {
				t.Fatalf("verdict %q: %+v", word, o)
			}
		})
	}
}

func TestClaudeOutcomeSkipsUnlabeled(t *testing.T) {
	root, canonical, entry := plantedClaudeOutcome(t)
	entry.Task = ""
	if err := appendClaudeOutcome(t.Context(), root, canonical, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, router.DefaultOutcomeLogPath)); !os.IsNotExist(err) {
		t.Fatalf("unlabeled stop wrote log: %v", err)
	}
}

func TestClaudeOutcomeUnreadAndAppendError(t *testing.T) {
	root, canonical, entry := plantedClaudeOutcome(t)
	canonical.TranscriptPath = filepath.Join(t.TempDir(), "missing.jsonl")
	if err := appendClaudeOutcome(t.Context(), root, canonical, entry); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, router.DefaultOutcomeLogPath)
	rows, err := router.ReadOutcomes(t.Context(), path)
	if err != nil || len(rows) != 1 || rows[0].Tokens != 0 || rows[0].Note != "tokens unread" {
		t.Fatalf("unread transcript: %+v %v", rows, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appendClaudeOutcome(t.Context(), root, canonical, entry); err == nil {
		t.Fatal("append error swallowed")
	}
}

func TestClaudeOutcomeRequestedAndDefaultRoutes(t *testing.T) {
	_, _, entry := plantedClaudeOutcome(t)
	var err error
	entry.ClaudeSubagent, err = claudeRequestedRoute(json.RawMessage(`{"model":"opus","effort":"high"}`))
	if err != nil {
		t.Fatal(err)
	}
	o := claudeOutcome(entry)
	if o.Target != "opus" || o.Effort != "high" {
		t.Fatalf("requested route: %+v", o)
	}
	entry.ClaudeSubagent, err = claudeRequestedRoute(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	entry.ResolvedModel = ""
	o = claudeOutcome(entry)
	if o.Target != "default" || o.Effort != "" || o.ResolvedModel != "none" || o.Identity.PhysicalModel != "none" {
		t.Fatalf("default route: %+v", o)
	}
}
