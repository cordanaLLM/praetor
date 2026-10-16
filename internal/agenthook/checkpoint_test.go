package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

// preEditStdin encodes the pre-edit hook payload properly instead of splicing root into a
// hand-written JSON literal. root is a filesystem path, and on Windows it contains
// backslashes (C:\Users\...); spliced directly into `"cwd":"` + root + `"` those backslashes
// land in the JSON text unescaped, which is not valid JSON ('\U', '\R', ... are not legal
// escapes). The hook's own JSON parser correctly refused it, so every pre-edit test built
// this way failed with "[BLOCKED BY HISS-16] Invalid hook input: hook input must be one JSON
// object" on the Windows leg of the portability matrix (#135) before the scope script it
// meant to exercise ever ran -- including TestRunPreEditDeniesOnAMissingMarkerOrAFailedScopeScript,
// which did not fail only because a refused-input denial and its own intended denial produce
// the same exit code and prefix. json.Marshal escapes whatever the path contains.
func preEditStdin(t *testing.T, root string) *bytes.Reader {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"tool_name": "Edit", "cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(payload)
}

// checkpointInvocation builds an Invocation whose interpreter resolves to the stub built by
// buildStub, running Run through the real EventPostTool/EventPreEdit/EventStop dispatch
// (evaluate.go) rather than calling its unexported evaluators directly: this is the same
// public-interface path TestRunGoldensPerDialect and its neighbours already use for pre-tool.
func checkpointInvocation(t *testing.T, event, root string, getenv func(string) string, payload []byte) Invocation {
	t.Helper()
	return Invocation{
		Client: "claude", Event: event, Stdin: strings.NewReader(string(payload)), Getenv: getenv, WorkDir: root,
		Policy: policy(t), Settings: config.HookSettings{Python: [][]string{{"python3"}}},
	}
}

func stubStdout(t *testing.T, value string) {
	t.Helper()
	t.Setenv("PRAETOR_STUB_STDOUT", value)
}

func dueReport(actions ...string) string {
	quoted := make([]string, len(actions))
	for i, action := range actions {
		quoted[i] = `"` + action + `"`
	}
	return checkpointResultMarker + `{"schema_version":1,"enabled":true,"due":true,"actions":[` + strings.Join(quoted, ",") + `]}` + "\n"
}

const cleanReport = checkpointResultMarker + `{"schema_version":1,"enabled":true,"due":false,"actions":[]}` + "\n"

func TestRunPostToolReportsADueCheckpointAsANote(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, dueReport("commit", "push"))
	response := Run(context.Background(), checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
	if response.ExitCode != 0 || standardClockOutput(decodeStdout(t, response)) == "" {
		t.Fatalf("due checkpoint must not block post-tool: %+v", response)
	}
	if !strings.Contains(string(response.Stderr), "Praetor checkpoint due") {
		t.Errorf("due note missing: %q", response.Stderr)
	}
}

func TestRunPostToolAllowsWhenNothingIsDue(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	response := Run(context.Background(), checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
	if response.ExitCode != 0 || standardClockOutput(decodeStdout(t, response)) == "" || len(response.Stderr) != 0 {
		t.Errorf("clean post-tool must allow silently: %+v", response)
	}
}

func TestRunPostToolNoInterpreterIsAStatedSkip(t *testing.T) {
	root := repository(t, true)
	empty := pathOnly(t.TempDir())
	response := Run(context.Background(), checkpointInvocation(t, "post-tool", root, empty, []byte(`{}`)))
	if response.ExitCode != 0 || standardClockOutput(decodeStdout(t, response)) == "" || !strings.Contains(string(response.Stderr), "no Python interpreter") {
		t.Errorf("missing interpreter must be a stated skip, not a block: %+v", response)
	}
}

func TestRunPostToolMissingOrDuplicateMarkerIsASkip(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	for name, stdout := range map[string]string{
		"missing":   "no marker here\n",
		"duplicate": cleanReport + cleanReport,
	} {
		stubStdout(t, stdout)
		response := Run(context.Background(), checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
		if response.ExitCode != 0 || standardClockOutput(decodeStdout(t, response)) == "" || !strings.Contains(string(response.Stderr), "unavailable") {
			t.Errorf("%s marker: post-tool must degrade to a skip, not a block: %+v", name, response)
		}
	}
}

func TestRunPreEditAllowsWhenTheScopeScriptPasses(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, checkpointScopeMarker+"\n")
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-edit", Stdin: preEditStdin(t, root),
		Getenv: getenv, WorkDir: root, Policy: policy(t), Settings: config.HookSettings{Python: [][]string{{"python3"}}},
	})
	if response.ExitCode != 0 || len(response.Stderr) != 0 {
		t.Errorf("scope pass must allow: %+v", response)
	}
}

func TestRunPreEditDeniesOnAMissingMarkerOrAFailedScopeScript(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	for name, tc := range map[string]struct {
		stdout string
		exit   string
	}{
		"missing marker":   {"nothing to see\n", "0"},
		"duplicate marker": {checkpointScopeMarker + "\n" + checkpointScopeMarker + "\n", "0"},
		"script failed":    {"", "2"},
	} {
		t.Setenv("PRAETOR_STUB_EXIT", tc.exit)
		stubStdout(t, tc.stdout)
		response := Run(context.Background(), Invocation{
			Client: "claude", Event: "pre-edit", Stdin: preEditStdin(t, root),
			Getenv: getenv, WorkDir: root, Policy: policy(t), Settings: config.HookSettings{Python: [][]string{{"python3"}}},
		})
		if response.ExitCode != 2 || !strings.HasPrefix(string(response.Stderr), "[BLOCKED BY HISS] ") {
			t.Errorf("%s: pre-edit must fail closed: %+v", name, response)
		}
	}
}

func TestRunPreEditNoInterpreterDenies(t *testing.T) {
	root := repository(t, true)
	empty := pathOnly(t.TempDir())
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-edit", Stdin: preEditStdin(t, root),
		Getenv: empty, WorkDir: root, Policy: policy(t), Settings: config.HookSettings{Python: [][]string{{"python3"}}},
	})
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "no Python interpreter") {
		t.Errorf("missing interpreter must deny pre-edit: %+v", response)
	}
}

// syncedRepository is a governed repository with a verified state.SyncState ledger, the
// precondition evaluateStop's state.VerifyStateSync call needs to pass.
func syncedRepository(t *testing.T) string {
	t.Helper()
	root := repository(t, true)
	if _, err := state.SyncState(context.Background(), root, "checkpoint test fixture"); err != nil {
		t.Fatalf("sync fixture state: %v", err)
	}
	return root
}

func TestRunStopOnAStaleOrMissingLedgerBlocks(t *testing.T) {
	root := repository(t, true) // governed, but never synced: no .workingdir/STATE.md
	_, getenv := buildStub(t, "python3")
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "state could not be verified") {
		t.Errorf("missing ledger must block stop: %+v", response)
	}
}

// A verified-fresh ledger with an unresolved P0 row still blocks stop, as the lefthook
// state-audit step did before the engine row (positive); resolving the row clears it
// (negative); a clean ledger allows (boundary, TestRunStopCleanAllows).
func TestRunStopBlocksOnAnUnresolvedP0Row(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	bug, err := state.AddBug(root, state.BugEntry{Title: "stop audit fixture", Severity: "p0"})
	if err != nil {
		t.Fatalf("add bug: %v", err)
	}
	if _, err := state.SyncState(context.Background(), root, "p0 fixture"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	run := func() Response {
		return Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	}
	if response := run(); response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "state audit failed") {
		t.Errorf("unresolved P0 must block stop: %+v", response)
	}
	if err := state.ResolveBug(root, bug.ID, "fixed"); err != nil {
		t.Fatalf("resolve bug: %v", err)
	}
	if _, err := state.SyncState(context.Background(), root, "p0 resolved"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if response := run(); response.ExitCode != 0 {
		t.Errorf("resolved P0 must allow stop: %+v", response)
	}
}

func TestRunStopCleanAllows(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	if response.ExitCode != 0 {
		t.Errorf("clean stop must allow: %+v", response)
	}
}

func TestRunStopFailedScriptIncludesStderr(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	t.Setenv("PRAETOR_STUB_EXIT", "1")
	t.Setenv("PRAETOR_STUB_STDERR", " \ncheckpoint lock unavailable\n\t")
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	want := "Praetor checkpoint could not be verified: exit status 1: checkpoint lock unavailable"
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), want) {
		t.Errorf("failed script lost its cause: exit=%d stderr=%q", response.ExitCode, response.Stderr)
	}
}

func TestRunStopDueCheckpointBlocksOnce(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, dueReport("commit"))
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "Praetor checkpoint due") {
		t.Errorf("due checkpoint must block stop once: %+v", response)
	}
}

func TestRunStopMissingMarkerDeniesAndNoInterpreterBlocksOnce(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, "no marker here\n")
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "could not be verified") {
		t.Errorf("missing marker must block stop: %+v", response)
	}
	empty := pathOnly(t.TempDir())
	response = Run(context.Background(), checkpointInvocation(t, "stop", root, empty, []byte(`{}`)))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "no Python interpreter") {
		t.Errorf("missing interpreter must block stop once: %+v", response)
	}
}

// TestRunPostToolEvaluatorTimeout is the H2 boundary case for a checkpoint evaluator that
// never answers: Run's own outer budget (budgetFor) already bounds this, this test tightens
// it so the case runs in well under a second instead of postToolBudget's 30s.
func TestRunPostToolEvaluatorTimeout(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	t.Setenv("PRAETOR_STUB_SLEEP_MS", "2000")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	response := Run(ctx, checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
	if response.ExitCode != 0 || standardClockOutput(decodeStdout(t, response)) == "" || !strings.Contains(string(response.Stderr), "unavailable") {
		t.Errorf("a timed-out evaluator must degrade post-tool to a skip: %+v", response)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the outer budget did not bound the call: %s", elapsed)
	}
}

// TestEvaluateStopHonoursStopActiveOnASecondPass exercises evaluateStop directly rather than
// through Run: dialect.Decode does not read stop_hook_active into Canonical yet (dialect.go is
// out of scope for this change, see the commit body), so Canonical.StopActive can only be
// driven by construction here until a follow-up extends Decode. The due-detection outcome
// itself must be identical either way; only the wording differs.
func TestEvaluateStopHonoursStopActiveOnASecondPass(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, dueReport("commit"))
	in := Invocation{Getenv: getenv, Settings: config.HookSettings{Python: [][]string{{"python3"}}}}
	first := evaluateStop(context.Background(), Registration{Client: "claude", Event: EventStop}, root, Canonical{StopActive: false}, in)
	second := evaluateStop(context.Background(), Registration{Client: "claude", Event: EventStop}, root, Canonical{StopActive: true}, in)
	if first.Outcome != Deny || second.Outcome != Deny {
		t.Fatalf("both passes must block: first=%+v second=%+v", first, second)
	}
	if strings.Contains(first.Reason, "Checkpoint remains incomplete") {
		t.Errorf("the first pass must not carry the repeated-continuation wording: %q", first.Reason)
	}
	if !strings.Contains(second.Reason, "Checkpoint remains incomplete") {
		t.Errorf("the second pass must state the checkpoint is still incomplete: %q", second.Reason)
	}
	if !strings.Contains(first.Reason, "Praetor checkpoint due") || !strings.Contains(second.Reason, "Praetor checkpoint due") {
		t.Errorf("stop_hook_active must not change what is due: first=%q second=%q", first.Reason, second.Reason)
	}
}

// slowRootLookup replaces the workspace root lookup with one that outlasts every deadline, as a
// loaded Windows runner's git rev-parse did (#1386), for the rest of the test.
func slowRootLookup(t *testing.T) {
	t.Helper()
	previous := resolveWorkspaceRoot
	t.Cleanup(func() { resolveWorkspaceRoot = previous })
	resolveWorkspaceRoot = func(ctx context.Context, _ []string, _ string) (string, error) {
		<-ctx.Done()
		return "", fmt.Errorf("resolve workspace root: exit status 1\n%w", ctx.Err())
	}
}

// A deadline that ends while the post-tool hook resolves the workspace root is the evaluator's
// timeout too: post-tool degrades to a stated skip, as it does when the evaluator itself times
// out (#1386). The pre-tool hook judges a command and stays fail-closed: the same lookup blocks it.
func TestRunPostToolRootLookupPastTheDeadline(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	slowRootLookup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	post := Run(ctx, checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
	if post.ExitCode != 0 || standardClockOutput(decodeStdout(t, post)) == "" || !strings.Contains(string(post.Stderr), "unavailable") {
		t.Errorf("a root lookup past the deadline must degrade post-tool to a skip: %+v", post)
	}
	preCtx, preCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer preCancel()
	pre := Run(preCtx, checkpointInvocation(t, "pre-tool", root, getenv, []byte(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)))
	if pre.ExitCode != 2 || !strings.Contains(string(pre.Stderr), "resolve workspace root") {
		t.Errorf("pre-tool must stay fail-closed on the same lookup: %+v", pre)
	}
}

// A root lookup that fails before the deadline is no timeout: post-tool keeps the block, and the
// clock note it carries must not turn the deny into a delivery. Before #1386 the encoder rendered
// every verdict with the note as context, so the deny passed as exit 0 with an empty stderr.
func TestRunPostToolRootLookupFailureStaysVisible(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	previous := resolveWorkspaceRoot
	t.Cleanup(func() { resolveWorkspaceRoot = previous })
	resolveWorkspaceRoot = func(context.Context, []string, string) (string, error) {
		return "", errors.New("resolve workspace root: planted failure")
	}
	post := Run(context.Background(), checkpointInvocation(t, "post-tool", root, getenv, []byte(`{}`)))
	if post.ExitCode != 2 || !strings.Contains(string(post.Stderr), "planted failure") {
		t.Errorf("a failed root lookup must stay a visible deny at post-tool: %+v", post)
	}
}

// Boundary: a skip and an allow keep the clock delivery; only a deny gives it up.
func TestEncodeClockDeliveryPerOutcome(t *testing.T) {
	dialect, _ := DialectFor("claude")
	canonical := Canonical{Event: EventPostTool}
	for _, tc := range []struct {
		verdict  Verdict
		exit     int
		delivery bool
	}{
		{Verdict{Outcome: Allow, AddedContext: "clock: x"}, 0, true},
		{Verdict{Outcome: Skip, Reason: "r", AddedContext: "clock: x"}, 0, true},
		{Verdict{Outcome: Deny, Reason: "r", AddedContext: "clock: x"}, 2, false},
	} {
		got := dialect.Encode(canonical, tc.verdict)
		if got.ExitCode != tc.exit || strings.Contains(string(got.Stdout), "clock: x") != tc.delivery {
			t.Errorf("outcome %v: exit %d stdout %q", tc.verdict.Outcome, got.ExitCode, got.Stdout)
		}
	}
}
