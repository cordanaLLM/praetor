// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package backlogcap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

const batchDate = "2026-10-07"

// capPolicy resolves a policy whose repository layer declares caps.
func capPolicy(t *testing.T, caps config.BacklogCaps) *config.EffectivePolicy {
	t.Helper()
	layer := config.PolicyLayer{
		Source:  config.PolicySource{ID: "repository", SHA256: strings.Repeat("a", 64)},
		Backlog: caps,
	}
	policy, err := config.ResolvePolicy(t.Context(), []config.PolicyLayer{layer})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// defectLedger writes count open defect rows, each located in a Go file that exists.
func defectLedger(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "internal/app/app.go", "package app\n\nfunc A() {}\n")
	for i := 0; i < count; i++ {
		if _, err := state.AddBug(root, state.BugEntry{Title: "defect", Location: "internal/app/app.go:3", Kind: state.BugKindDefect}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func evaluate(t *testing.T, root string, caps config.BacklogCaps) *Report {
	t.Helper()
	report, err := Evaluate(t.Context(), root, capPolicy(t, caps))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func batchFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(BatchDir)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// Under the cap: the category is counted, no batch is written and the gate passes.
func TestBacklogCap_Positive_UnderTheCapWritesNoBatchAndPasses(t *testing.T) {
	root := defectLedger(t, 2)
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 3, Action: config.BacklogGate}})
	category := &report.Categories[0]
	if category.Count() != 2 || category.State() != StateUnder || !category.Present {
		t.Fatalf("category = %+v, state %s", category, category.State())
	}
	written, err := WriteBatches(t.Context(), root, report, batchDate)
	if err != nil || len(written) != 0 || len(batchFiles(t, root)) != 0 {
		t.Fatalf("a batch was written under the cap: %v %v", written, err)
	}
	if err := report.Gate(); err != nil {
		t.Fatalf("gate failed under the cap: %v", err)
	}
}

// Boundary, inclusive side: a count equal to max is at the cap, writes nothing and passes.
func TestBacklogCap_Boundary_ExactlyAtTheCapPasses(t *testing.T) {
	root := defectLedger(t, 3)
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 3, Action: config.BacklogGate}})
	if state := report.Categories[0].State(); state != StateAt {
		t.Fatalf("state at max = %s, want %s", state, StateAt)
	}
	if written, err := WriteBatches(t.Context(), root, report, batchDate); err != nil || len(written) != 0 {
		t.Fatalf("a batch was written at the cap: %v %v", written, err)
	}
	if err := report.Gate(); err != nil {
		t.Fatalf("gate failed at the cap: %v", err)
	}
	if line := report.Categories[0].Line(); !strings.Contains(line, "defects: 3 of 3, at the cap") {
		t.Fatalf("status line = %q", line)
	}
}

// Boundary, exclusive side, and the planted extra row: one row over max writes the batch,
// which names every item, and the gate fails naming the category, the count and the cap.
func TestBacklogCap_Negative_OneOverTheCapBatchesEveryItemAndGateFails(t *testing.T) {
	root := defectLedger(t, 3)
	if _, err := state.AddBug(root, state.BugEntry{Title: "planted", Location: "core", Kind: state.BugKindDefect}); err != nil {
		t.Fatal(err)
	}
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 3, Action: config.BacklogGate}})
	if state := report.Categories[0].State(); state != StateOver {
		t.Fatalf("state one over max = %s", state)
	}
	err := report.Gate()
	if err == nil || !strings.Contains(err.Error(), "backlog cap defects: 4 items, over the cap of 3") {
		t.Fatalf("gate error = %v", err)
	}
	written, err := WriteBatches(t.Context(), root, report, batchDate)
	if err != nil || len(written) != 1 || written[0] != ".workingdir/batches/defects-2026-10-07.md" {
		t.Fatalf("written = %v, %v", written, err)
	}
	batch := readBatch(t, root, written[0])
	for _, id := range []string{"BUG-001", "BUG-002", "BUG-003", "BUG-004"} {
		if strings.Count(batch, "`"+id+"`") != 1 {
			t.Errorf("batch names %s %d times:\n%s", id, strings.Count(batch, "`"+id+"`"), batch)
		}
	}
	for _, want := range []string{"## internal/app/app.go (3)", "## core (1)", "over its cap of 3", "re-check: location resolves",
		"re-check: not re-checked: the location is not a Go file:line"} {
		if !strings.Contains(batch, want) {
			t.Errorf("batch lacks %q:\n%s", want, batch)
		}
	}
}

func readBatch(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A row without a kind is a finding and counts as a defect; a scope row is not counted; a
// resolved or deferred row is not counted.
func TestBacklogCap_Negative_UnlabelledRowIsAFindingAndCounts(t *testing.T) {
	root := t.TempDir()
	for _, bug := range []state.BugEntry{
		{Title: "unlabelled"},
		{Title: "scope", Kind: state.BugKindScope},
		{Title: "deferred", Kind: state.BugKindDefect, Status: "deferred"},
		{Title: "defect", Kind: state.BugKindDefect, Status: "investigating"},
	} {
		if _, err := state.AddBug(root, bug); err != nil {
			t.Fatal(err)
		}
	}
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 10}})
	category := report.Categories[0]
	if category.Count() != 2 || category.Items[0].ID != "BUG-001" || category.Items[1].ID != "BUG-004" {
		t.Fatalf("counted items = %+v", category.Items)
	}
	if len(category.Findings) != 1 || !strings.Contains(category.Findings[0], "BUG-001 has no kind") {
		t.Fatalf("findings = %v", category.Findings)
	}
}

// A category praetor has no reader for is reported as not counted with the reason, never as
// zero, and a gate on it fails rather than passing.
func TestBacklogCap_Negative_UncountedCategoryIsNeverZero(t *testing.T) {
	report := evaluate(t, t.TempDir(), config.BacklogCaps{ForgeAlerts: config.BacklogCap{Max: 5, Action: config.BacklogGate}})
	category := &report.Categories[0]
	if category.Counted || category.State() != StateNotCounted || !strings.Contains(category.Line(), "not counted: praetor has no forge alert reader") {
		t.Fatalf("forge alerts = %+v / %q", category, category.Line())
	}
	if err := report.Gate(); err == nil || !strings.Contains(err.Error(), "forge_alerts has action gate but cannot be counted") {
		t.Fatalf("gate on an uncounted category: %v", err)
	}
	report = evaluate(t, t.TempDir(), config.BacklogCaps{ForgeAlerts: config.BacklogCap{Max: 5}})
	if err := report.Gate(); err != nil {
		t.Fatalf("an uncounted report-only category must not fail the gate: %v", err)
	}
}

// Over the cap with action report, nothing is batched or gated; with action batch, the batch
// is written and the gate still passes.
func TestBacklogCap_Boundary_ActionSelectsTheResponse(t *testing.T) {
	root := defectLedger(t, 2)
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 1}})
	if written, err := WriteBatches(t.Context(), root, report, batchDate); err != nil || len(written) != 0 || report.Gate() != nil {
		t.Fatalf("report action batched or gated: %v %v", written, err)
	}
	report = evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 1, Action: config.BacklogBatch}})
	if written, err := WriteBatches(t.Context(), root, report, batchDate); err != nil || len(written) != 1 || report.Gate() != nil {
		t.Fatalf("batch action: %v %v gate %v", written, err, report.Gate())
	}
}

// The re-check marks a location past the end of its file, and tasks and questions, which have
// no resolver, as not re-checked; tasks group by their section and questions form one group.
func TestBacklogCap_Positive_RecheckAndGrouping(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "short.go", "package short\n")
	if _, err := state.AddBug(root, state.BugEntry{Title: "moved", Location: "short.go:40", Kind: state.BugKindDefect}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".workingdir/OPEN.md", "## Spikes\n- [ ] one\n- [x] done\n## Docs\n- [ ] two\n")
	if _, err := state.AddQuestion(root, state.QuestionEntry{Question: "ship?"}); err != nil {
		t.Fatal(err)
	}
	// Two pending tasks are over a cap of one; one question is at it.
	batch := config.BacklogCap{Max: 1, Action: config.BacklogBatch}
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 1}, Tasks: batch, Questions: batch})
	if written, err := WriteBatches(t.Context(), root, report, batchDate); err != nil || len(written) != 1 {
		t.Fatalf("only tasks are over: %v %v", written, err)
	}
	tasks := readBatch(t, root, BatchPath(config.BacklogTasks, batchDate))
	for _, want := range []string{"## Docs (1)", "## Spikes (1)", "`task 1` one", "`task 3` two", "not re-checked: no resolver for this category"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("tasks batch lacks %q:\n%s", want, tasks)
		}
	}
	if strings.Index(tasks, "## Docs") > strings.Index(tasks, "## Spikes") {
		t.Errorf("groups are not sorted:\n%s", tasks)
	}
	if verdict := recheckLocation(root, Item{Location: "short.go:40"}); !strings.Contains(verdict, "no longer resolves: line 40 is past the end of a 1-line file") {
		t.Fatalf("stale verdict = %q", verdict)
	}
	if report.Categories[2].Items[0].Group != "pending questions" {
		t.Fatalf("question group = %+v", report.Categories[2].Items)
	}
}

// Negative: a malformed date writes nothing; boundary: the same input writes the same bytes.
func TestWriteBatches_DateAndDeterminism(t *testing.T) {
	root := defectLedger(t, 2)
	report := evaluate(t, root, config.BacklogCaps{Defects: config.BacklogCap{Max: 1, Action: config.BacklogBatch}})
	if _, err := WriteBatches(t.Context(), root, report, "07.10.2026"); err == nil || len(batchFiles(t, root)) != 0 {
		t.Fatal("a malformed date was accepted")
	}
	first, err := WriteBatches(t.Context(), root, report, batchDate)
	if err != nil {
		t.Fatal(err)
	}
	before := readBatch(t, root, first[0])
	if _, err := WriteBatches(t.Context(), root, report, batchDate); err != nil {
		t.Fatal(err)
	}
	if after := readBatch(t, root, first[0]); after != before {
		t.Fatalf("the batch is not deterministic:\n%s\n---\n%s", before, after)
	}
}

// Boundary: an absent ledger counts as empty and says so; a policy without caps, or none at
// all, yields an empty report; a nil context is refused.
func TestEvaluate_Boundary_AbsentLedgerAndNoPolicy(t *testing.T) {
	report := evaluate(t, t.TempDir(), config.BacklogCaps{Questions: config.BacklogCap{Max: 2}})
	category := &report.Categories[0]
	if category.Present || category.Count() != 0 || !strings.Contains(category.Line(), ".workingdir/QUESTIONS.md is absent") {
		t.Fatalf("absent ledger = %+v / %q", category, category.Line())
	}
	if empty, err := Evaluate(t.Context(), t.TempDir(), nil); err != nil || len(empty.Categories) != 0 {
		t.Fatalf("nil policy: %+v %v", empty, err)
	}
	if empty := evaluate(t, t.TempDir(), config.BacklogCaps{}); len(empty.Categories) != 0 {
		t.Fatalf("uncapped policy: %+v", empty)
	}
	//nolint:staticcheck // SA1012: the nil context is the input under test.
	if _, err := Evaluate(nil, t.TempDir(), nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
