package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// legacyQuestionsMD is QUESTIONS.md exactly as the previous writer produced it.
const legacyQuestionsMD = "# User Decisions & Questions Buffer\n\n" +
	"> Questions buffered by autonomous agents for operator review.\n\n" +
	"| ID | Question | Options | Status | Decision |\n" +
	"| :--- | :--- | :--- | :--- | :--- |\n" +
	"| `Q-001` | Ship the beta? | Yes, No | decided | Yes |\n" +
	"| `Q-003` | Keep the flag? |  | pending |  |\n"

func writeQuestionFixture(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, questionLedgerName), text)
	return root
}

// hostileQuestion carries every character the previous codec lost: a pipe, line breaks and
// tabs, edge spaces, literal entities, and commas inside options.
func hostileQuestion() QuestionEntry {
	return QuestionEntry{
		Question: "  pick a | b\nsecond line\twith tab &amp; &#124; λ  ",
		Context:  "context | survives\r\nacross lines, commas and <tags>",
		Options:  []string{"a, with comma", "b | pipe", "  padded  ", "c\nnewline"},
	}
}

func TestQuestionLedger_Positive_FullRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := hostileQuestion()
	added, err := AddQuestion(root, want)
	if err != nil {
		t.Fatalf("AddQuestion: %v", err)
	}
	answer := "a, with comma | and a\nsecond line"
	if err := DecideQuestion(root, added.ID, answer); err != nil {
		t.Fatalf("DecideQuestion: %v", err)
	}

	got, err := ListQuestions(root, "all")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListQuestions: %+v, %v", got, err)
	}
	q := got[0]
	if q.ID != "Q-001" || q.Question != want.Question || q.Context != want.Context ||
		!reflect.DeepEqual(q.Options, want.Options) || q.SelectedAnswer != answer || q.Status != "decided" {
		t.Fatalf("field lost across the ledger:\n got %#v\nwant %#v with answer %q", q, want, answer)
	}
	if !q.CreatedAt.Equal(added.CreatedAt) || q.DecidedAt.IsZero() || q.DecidedAt.Before(q.CreatedAt) {
		t.Fatalf("timestamps lost: created %v (added %v), decided %v", q.CreatedAt, added.CreatedAt, q.DecidedAt)
	}
	text := readLedgerFile(t, filepath.Join(root, WorkingDirName, questionLedgerName))
	if strings.Count(text, questionMetadataRef) != 1 || strings.Contains(text, want.Context) {
		t.Fatalf("row must be one line in the current form, context in the sidecar:\n%s", text)
	}
}

func TestQuestionLedger_Positive_RenderParseRoundTripsVisibleFields(t *testing.T) {
	want := hostileQuestion()
	want.ID, want.Status, want.SelectedAnswer = "Q-007", "decided", "b | pipe, and more"
	parsed, err := ParseQuestionsMarkdownStrict(RenderQuestionsMarkdown([]QuestionEntry{want}))
	if err != nil || len(parsed) != 1 {
		t.Fatalf("render+parse: %+v, %v", parsed, err)
	}
	got := parsed[0]
	if got.ID != want.ID || got.Question != want.Question || !reflect.DeepEqual(got.Options, want.Options) ||
		got.Status != want.Status || got.SelectedAnswer != want.SelectedAnswer || got.Context != "" {
		t.Fatalf("visible fields changed:\n got %#v\nwant %#v", got, want)
	}
}

// Q-002 is gone from the fixture. Counting rows allocated Q-003 again, and the second Q-003
// then answered DecideQuestion in place of the first.
func TestQuestionLedger_Positive_IDsContinueFromTheLargest(t *testing.T) {
	root := writeQuestionFixture(t, legacyQuestionsMD)
	added, err := AddQuestion(root, QuestionEntry{Question: "new"})
	if err != nil {
		t.Fatalf("AddQuestion: %v", err)
	}
	if added.ID != "Q-004" {
		t.Fatalf("expected Q-004 after Q-003, got %s", added.ID)
	}
}

func TestQuestionLedger_Positive_LegacyLedgerReadsAndUpgrades(t *testing.T) {
	root := writeQuestionFixture(t, legacyQuestionsMD)
	before, err := ListQuestions(root, "all")
	if err != nil || len(before) != 2 {
		t.Fatalf("legacy ledger unreadable: %+v, %v", before, err)
	}
	if !reflect.DeepEqual(before[0].Options, []string{"Yes", "No"}) || before[0].SelectedAnswer != "Yes" ||
		len(before[1].Options) != 0 || before[1].Status != "pending" {
		t.Fatalf("legacy fields misread: %+v", before)
	}
	if err := DecideQuestion(root, "q-003", "Keep"); err != nil {
		t.Fatalf("decide a legacy row by case-folded ID: %v", err)
	}
	after, err := ListQuestions(root, "all")
	if err != nil || len(after) != 2 || after[0].Question != before[0].Question || after[1].SelectedAnswer != "Keep" {
		t.Fatalf("upgrade changed a record: %+v, %v", after, err)
	}
	text := readLedgerFile(t, filepath.Join(root, WorkingDirName, questionLedgerName))
	if strings.Count(text, questionMetadataRef) != 2 {
		t.Fatalf("a write must leave every row in the current form:\n%s", text)
	}
}

func TestQuestionLedger_Negative_RejectsInvalidDocuments(t *testing.T) {
	header := defaultQuestionsMD()
	cases := map[string]string{
		"duplicate id":        legacyQuestionsMD + "| `Q-001` | again | | pending | |\n",
		"pipe in legacy cell": header + "| `Q-001` | a | b | x | pending | |\n",
		"unknown status":      header + "| `Q-001` | q |  | maybe |  |\n",
		"noncanonical id":     header + "| `Q-1` | q |  | pending |  |\n",
		"blank question":      header + "| `Q-001` |   |  | pending |  |\n",
		"short current row":   header + "| `Q-001` | q | pending | " + questionMetadataRef + "\n",
		"unquoted current id": header + "| Q-001 | q |  | pending |  | " + questionMetadataRef + "\n",
		"missing sidecar":     header + "| `Q-001` | q |  | pending |  | " + questionMetadataRef + "\n",
		"nul byte":            header + "| `Q-001` | a\x00b |  | pending |  |\n",
		"invalid utf-8":       header + "| `Q-001` | a\xffb |  | pending |  |\n",
		"oversized":           header + strings.Repeat(" ", maxLedgerBytes),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeQuestionFixture(t, text)
			if qs, err := ListQuestions(root, "all"); err == nil {
				t.Fatalf("invalid ledger read as %+v", qs)
			}
			if _, err := AddQuestion(root, QuestionEntry{Question: "blocked"}); err == nil {
				t.Fatal("writer accepted an invalid ledger")
			}
			if err := DecideQuestion(root, "Q-001", "x"); err == nil {
				t.Fatal("decision accepted an invalid ledger")
			}
			assertIntegrityFile(t, filepath.Join(root, WorkingDirName, questionLedgerName), text)
		})
	}
}

func TestQuestionLedger_Negative_RejectsInvalidSidecar(t *testing.T) {
	row := defaultQuestionsMD() + "| `Q-001` | q |  | pending |  | " + questionMetadataRef + "\n"
	record := `{"context":"c","created_at":"2026-09-25T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z"}`
	cases := map[string]string{
		"missing record":  `{"version":1,"questions":{}}`,
		"bug member":      `{"version":1,"bugs":{"Q-001":` + record + `}}`,
		"extra member":    `{"version":1,"questions":{"Q-001":` + record + `},"bugs":{}}`,
		"wrong version":   `{"version":2,"questions":{"Q-001":` + record + `}}`,
		"bug id":          `{"version":1,"questions":{"BUG-001":` + record + `}}`,
		"null questions":  `{"version":1,"questions":null}`,
		"missing field":   `{"version":1,"questions":{"Q-001":{"context":"c","created_at":"2026-09-25T10:00:00Z"}}}`,
		"nul in context":  `{"version":1,"questions":{"Q-001":{"context":"a\u0000b","created_at":"2026-09-25T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z"}}}`,
		"duplicate id":    `{"version":1,"questions":{"Q-001":` + record + `,"Q-001":` + record + `}}`,
		"trailing gabage": `{"version":1,"questions":{}} x`,
	}
	for name, sidecar := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeQuestionFixture(t, row)
			writeIntegrityFile(t, filepath.Join(root, WorkingDirName, questionMetaName), sidecar)
			if qs, err := ListQuestions(root, "all"); err == nil {
				t.Fatalf("invalid sidecar accepted: %+v", qs)
			}
			if _, err := AddQuestion(root, QuestionEntry{Question: "blocked"}); err == nil {
				t.Fatal("writer accepted an invalid sidecar")
			}
		})
	}
	root := writeQuestionFixture(t, row)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, questionMetaName), `{"version":1,"questions":{"Q-001":`+record+`}}`)
	if qs, err := ListQuestions(root, "all"); err != nil || len(qs) != 1 || qs[0].Context != "c" {
		t.Fatalf("valid sidecar rejected: %+v, %v", qs, err)
	}
}

func TestAddQuestion_Negative_RefusesInvalidFields(t *testing.T) {
	cases := map[string]QuestionEntry{
		"nul in question":   {Question: "a\x00b"},
		"invalid utf-8":     {Question: "a\xffb"},
		"blank question":    {Question: " \t "},
		"oversized context": {Question: "q", Context: strings.Repeat("x", maxLedgerFieldBytes+1)},
		"blank option":      {Question: "q", Options: []string{"a", " "}},
		"nul in option":     {Question: "q", Options: []string{"a\x00"}},
		"too many options":  {Question: "q", Options: strings.Split(strings.Repeat("o,", maxQuestionOptions+1), ",")[:maxQuestionOptions+1]},
		"unknown status":    {Question: "q", Status: "maybe"},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := AddQuestion(root, q); err == nil {
				t.Fatalf("invalid question accepted: %#v", q)
			}
			if qs, err := ListQuestions(root, "all"); err != nil || len(qs) != 0 {
				t.Fatalf("a refused question must leave the ledger empty: %+v, %v", qs, err)
			}
		})
	}
	root := t.TempDir()
	if _, err := AddQuestion(root, QuestionEntry{Question: "q"}); err != nil {
		t.Fatal(err)
	}
	if err := DecideQuestion(root, "Q-001", "a\x00b"); err == nil {
		t.Fatal("a decision holding NUL was accepted")
	}
}

func TestAddQuestion_Boundary_LimitsAreInclusive(t *testing.T) {
	root := t.TempDir()
	options := make([]string, maxQuestionOptions)
	for i := range options {
		options[i] = strings.Repeat("o", i+1)
	}
	q := QuestionEntry{Question: strings.Repeat("q", maxLedgerFieldBytes), Context: strings.Repeat("c", maxLedgerFieldBytes), Options: options}
	if _, err := AddQuestion(root, q); err != nil {
		t.Fatalf("fields exactly at the bound must be accepted: %v", err)
	}
	got, err := ListQuestions(root, "all")
	if err != nil || len(got) != 1 || len(got[0].Options) != maxQuestionOptions || len(got[0].Context) != maxLedgerFieldBytes {
		t.Fatalf("boundary record not read back: %v", err)
	}
	// An empty options list is a question with no preset answers, not one blank option.
	if _, err := AddQuestion(root, QuestionEntry{Question: "open", Options: []string{}}); err != nil {
		t.Fatal(err)
	}
	open, err := ListQuestions(root, "all")
	if err != nil || len(open) != 2 || len(open[1].Options) != 0 {
		t.Fatalf("empty options misread: %+v, %v", open, err)
	}
}

// The question sidecar is part of the ledger, so the state sync binding covers it exactly as
// it covers bugs.meta.json.
func TestQuestionSidecar_Boundary_BoundIntoStateSync(t *testing.T) {
	root := t.TempDir()
	if _, err := AddQuestion(root, QuestionEntry{Question: "q", Context: "c", CreatedAt: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncState(t.Context(), root, "bind question sidecar"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("fresh sync must verify: %v", err)
	}
	path := filepath.Join(root, WorkingDirName, questionMetaName)
	writeIntegrityFile(t, path, `{"version":1,"questions":{"Q-001":{"context":"edited","created_at":"2026-09-25T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z"}}}`+"\n")
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("a question sidecar edit did not stale the sync marker")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("question sidecar removal did not stale the sync marker")
	}
}
