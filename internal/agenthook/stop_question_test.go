package agenthook

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// stopFixture is one final message and what the stop check must answer for it.
type stopFixture struct {
	name    string
	message string
	want    Outcome
}

const fence = "```"

// stopFixtures are shared by the unit table and by the per-client replays, so both
// directions (deny and allow) run the same messages through every client.
var stopFixtures = []stopFixture{
	{"closing question", "Fixed the parser.\n\nShould I also update the docs?", Deny},
	{"closing question with trailing emphasis", "All green.\n\n**Want me to push?**", Deny},
	{"question above an options list", "Two ways forward.\n\nWhich do you prefer?\n- A: rebase\n- B: merge", Deny},
	{"options list then a request to choose", "Options:\n\n- A: rebase\n- B: merge\n\nLet me know which one.", Deny},
	{"question in a code block", "Ran it.\n\n" + fence + "\n$ grep -n 'why?' file\nShould this fail?\n" + fence + "\n\nDone.", Allow},
	{"question in an unclosed code block", "Done.\n\n" + fence + "\nShould this fail?", Allow},
	{"question in inline code", "The regexp is `a?` and it matches.", Allow},
	{"question in a blockquote", "The reviewer wrote:\n\n> Should we merge this?\n\nI merged it.", Allow},
	{"question mark in a url", "See https://example.com/search?q=1 for the report.", Allow},
	{"question then a sentence in the closing paragraph", "Why does it fail? The cache is stale. Cleared it.", Deny},
	{"question then an offer", "Fixed it.\n\nShould I push? Let me know.", Deny},
	{"either-or question then a closing sentence", "Fixed it.\n\nShould I open the PR, or leave it for review? Your call.", Deny},
	{"options list then a which-do-you question", "Options:\n- A: rebase\n- B: merge\n\nWhich do you want? Happy to do either.", Deny},
	{"closing question about a link", "Fixed it.\n\nWant me to merge https://github.com/o/r/pull/12?", Deny},
	{"closing question about a link in parentheses", "Fixed it.\n\nMerge (https://github.com/o/r/pull/12)?", Deny},
	{"url with a query string then a statement", "Fixed it.\n\nSee https://example.com/a?b=1.", Allow},
	{"rhetorical question mid-report", "Why did it fail?\n\nThe cache was stale.\n\nI cleared it and reran.\n\nAll tests pass.", Allow},
	{"options list without a choice request", "Changed:\n\n- parser.go\n- parser_test.go\n\nAll tests pass.", Allow},
	{"choice words without a list", "Pick up from here tomorrow.", Allow},
	{"statement", "Done. Tests pass.", Allow},
	{"summary list then a let-me-know offer", "Changed:\n- a.go\n- b.go\n\nLet me know if you need anything else.", Allow},
	{"summary list then a tell-me offer", "Changed:\n- a.go\n- b.go\n\nTell me if CI fails.", Allow},
	{"options list then a let-me-know-whether", "Options:\n- A: rebase\n- B: merge\n\nLet me know whether to rebase.", Deny},
	{"options list then a let-me-know-if-you-prefer", "Options:\n- A: rebase\n- B: merge\n\nLet me know if you prefer B.", Deny},
	{"question in an indented code block", "Ran it.\n\n    Should this fail?\n\nDone.", Allow},
	{"question in a closing table cell", "Result:\n\n| case | verdict |\n| --- | --- |\n| why? | none |", Allow},
	{"full-width closing question", "Fixed it.\n\nPush now\uff1f", Deny},
	{"single paragraph that asks and answers", "Why did it fail? The cache was stale.", Deny},
	{"intro then numbered questions", "Two decisions needed:\n\n1. Rebase or merge?\n2. Push now?", Deny},
	{"numbered questions only", "1. Rebase or merge?\n2. Push now?", Deny},
	{"statement then bulleted questions", "Done.\n\n- Should I rebase?\n- Should I push?", Deny},
	{"list of statements then no question", "Done.\n\n- Rebased.\n- Pushed.", Allow},
	{"question list followed by a statement paragraph", "Open items:\n\n- Why did it fail?\n\nIt was the cache.", Allow},
	{"list item with a question mark mid-item", "Done.\n\n- Fixed the why? case.", Allow},
	{"empty message", "", Skip},
	{"blank message", " \n\t\n", Skip},
}

func TestEvaluateStopQuestionFixtures(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini"} {
		row := stopRow(t, client)
		for _, tc := range stopFixtures {
			got := evaluateStopQuestion(row, Canonical{Event: EventStop, Return: tc.message})
			if got.Outcome != tc.want {
				t.Errorf("%s/%s: got %+v, want outcome %v", client, tc.name, got, tc.want)
			}
			if got.Outcome != Allow && got.Reason == "" {
				t.Errorf("%s/%s: %v without a stated reason", client, tc.name, got.Outcome)
			}
		}
	}
}

// stopRow looks up the stop row of client, never the first row of its table.
func stopRow(t *testing.T, client string) Registration {
	t.Helper()
	row, err := ParseArguments(client, string(EventStop))
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// TestEvaluateStopQuestionStatesTheNoToolFallback: a client may offer no structured question
// tool in its current mode (Codex outside the modes with request_user_input), so every deny
// also says to restate the choice as a statement.
func TestEvaluateStopQuestionStatesTheNoToolFallback(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini"} {
		got := evaluateStopQuestion(stopRow(t, client), Canonical{Return: "Shall I proceed?"})
		if got.Outcome != Deny || !strings.Contains(got.Reason, "without a question") {
			t.Errorf("%s: %+v lacks the no-tool fallback", client, got)
		}
	}
}

func TestEvaluateStopQuestionNamesTheClientTool(t *testing.T) {
	want := map[string]string{"claude": "AskUserQuestion", "codex": "request_user_input", "gemini": "ask_user"}
	for client, tool := range want {
		got := evaluateStopQuestion(stopRow(t, client), Canonical{Return: "Shall I proceed?"})
		if got.Outcome != Deny || !strings.Contains(got.Reason, tool) || !strings.HasPrefix(got.Reason, "[BLOCKED BY HISS] ") {
			t.Errorf("%s: %+v does not name %s", client, got, tool)
		}
	}
}

func TestEvaluateStopQuestionRepeatedStopIsAStatedSkip(t *testing.T) {
	got := evaluateStopQuestion(Registration{Client: "claude", Event: EventStop}, Canonical{Return: "Shall I proceed?", StopActive: true})
	if got.Outcome != Skip || !strings.Contains(got.Reason, "stop_hook_active") {
		t.Errorf("repeated stop: %+v", got)
	}
}

func TestEvaluateStopQuestionAgyHasNoMessageToJudge(t *testing.T) {
	got := evaluateStopQuestion(Registration{Client: "agy", Event: EventStop}, Canonical{Return: "Shall I proceed?"})
	if got.Outcome != Skip || !strings.Contains(got.Reason, "no final message") {
		t.Errorf("agy stop: %+v", got)
	}
}

// Boundary: only the last two paragraphs are judged.
func TestClosesWithQuestionJudgesOnlyTheClosingParagraphs(t *testing.T) {
	row := Registration{Client: "claude", Event: EventStop}
	for name, tc := range map[string]struct {
		message string
		want    Outcome
	}{
		"question two paragraphs from the end":    {"Ready?\n\nMiddle.\n\nEnd.", Allow},
		"question one paragraph from the end":     {"Ready?\n\nEnd.", Allow},
		"question in the last paragraph":          {"Middle.\n\nReady?", Deny},
		"list is the last paragraph":              {"Which one?\n\n- A\n- B", Deny},
		"single list item is not an options list": {"- A\n\nLet me know.", Allow},
	} {
		if got := evaluateStopQuestion(row, Canonical{Return: tc.message}); got.Outcome != tc.want {
			t.Errorf("%s: %+v, want %v", name, got, tc.want)
		}
	}
}

func finalMessagePayload(t *testing.T, client, message string, active bool) []byte {
	t.Helper()
	key := finalMessageKey(client)
	fields := map[string]any{key: message, "stop_hook_active": active}
	return commandPayload(t, fields)
}

func stopInvocation(client, root string, getenv func(string) string, payload []byte) Invocation {
	return Invocation{
		Client: client, Event: "stop", Stdin: strings.NewReader(string(payload)), Getenv: getenv, WorkDir: root,
		Policy: nil, Settings: config.HookSettings{Python: [][]string{{"python3"}}},
	}
}

// TestRunStopPerClient replays every fixture through Run for each native client with a
// clean ledger and checkpoint, so only the question check decides the outcome.
func TestRunStopPerClient(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	for _, client := range []string{"claude", "codex", "gemini"} {
		for _, tc := range stopFixtures {
			in := stopInvocation(client, root, getenv, finalMessagePayload(t, client, tc.message, false))
			in.Policy = policy(t)
			response := Run(context.Background(), in)
			denied := response.ExitCode == 2 && strings.HasPrefix(string(response.Stderr), "[BLOCKED BY HISS] ")
			if denied != (tc.want == Deny) {
				t.Errorf("%s/%s: %+v, want outcome %v", client, tc.name, response, tc.want)
			}
			if tc.want == Skip && !strings.Contains(string(response.Stderr), "skipped") {
				t.Errorf("%s/%s: skip not stated: %+v", client, tc.name, response)
			}
		}
	}
}

func TestRunStopRepeatedStopIsSkippedPerClient(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	for _, client := range []string{"claude", "codex", "gemini"} {
		in := stopInvocation(client, root, getenv, finalMessagePayload(t, client, "Should I proceed?", true))
		in.Policy = policy(t)
		response := Run(context.Background(), in)
		if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "stop_hook_active") {
			t.Errorf("%s: repeated stop must be a stated skip: %+v", client, response)
		}
	}
}

// The question check runs before the ledger: an unsynced ledger still denies a stop with no
// question, and a question denies first without reading the ledger.
func TestRunStopQuestionDeniesBeforeTheLedgerIsRead(t *testing.T) {
	root := repository(t, true) // never synced: the ledger check alone would deny too
	in := stopInvocation("claude", root, noEnvironment, finalMessagePayload(t, "claude", "Should I proceed?", false))
	in.Policy = policy(t)
	response := Run(context.Background(), in)
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "AskUserQuestion") {
		t.Errorf("question must deny first: %+v", response)
	}
}

func TestRunStopWrongTypedMessageDoesNotDeny(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	in := stopInvocation("claude", root, getenv, []byte(`{"last_assistant_message":7,"stop_hook_active":"yes"}`))
	in.Policy = policy(t)
	if response := Run(context.Background(), in); response.ExitCode != 0 {
		t.Errorf("a malformed optional member must not deny the stop: %+v", response)
	}
}

func TestDecodeStopReadsTheFinalMessagePerClient(t *testing.T) {
	for client, key := range map[string]string{"claude": "last_assistant_message", "codex": "last_assistant_message", "gemini": "prompt_response"} {
		dialect, _ := DialectFor(client)
		canonical, err := dialect.Decode(EventStop, commandPayload(t, map[string]any{key: "Done?", "stop_hook_active": true}))
		if err != nil || canonical.Return != "Done?" || !canonical.StopActive {
			t.Errorf("%s: %+v, %v", client, canonical, err)
		}
	}
	dialect, _ := DialectFor("gemini")
	canonical, err := dialect.Decode(EventStop, commandPayload(t, map[string]any{"last_assistant_message": "Done?"}))
	if err != nil || canonical.Return != "" {
		t.Errorf("gemini must not read last_assistant_message: %+v, %v", canonical, err)
	}
}

func TestRunAgyStopStatesItJudgesNoMessage(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "agy", "stop", root, commandPayload(t, map[string]any{"executionNum": 1, "terminationReason": "model_stop"}))
	if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "no final message") {
		t.Errorf("agy stop: %+v", response)
	}
}
