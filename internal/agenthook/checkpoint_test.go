package agenthook

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

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
	if response.ExitCode != 0 || len(response.Stdout) != 0 {
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
	if response.ExitCode != 0 || len(response.Stdout) != 0 || len(response.Stderr) != 0 {
		t.Errorf("clean post-tool must allow silently: %+v", response)
	}
}

func TestRunPostToolNoInterpreterIsAStatedSkip(t *testing.T) {
	root := repository(t, true)
	empty := pathOnly(t.TempDir())
	response := Run(context.Background(), checkpointInvocation(t, "post-tool", root, empty, []byte(`{}`)))
	if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "no Python interpreter") {
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
		if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "unavailable") {
			t.Errorf("%s marker: post-tool must degrade to a skip, not a block: %+v", name, response)
		}
	}
}

func TestRunPreEditAllowsWhenTheScopeScriptPasses(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, checkpointScopeMarker+"\n")
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-edit", Stdin: strings.NewReader(`{"tool_name":"Edit","cwd":"` + root + `"}`),
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
			Client: "claude", Event: "pre-edit", Stdin: strings.NewReader(`{"tool_name":"Edit","cwd":"` + root + `"}`),
			Getenv: getenv, WorkDir: root, Policy: policy(t), Settings: config.HookSettings{Python: [][]string{{"python3"}}},
		})
		if response.ExitCode != 2 || !strings.HasPrefix(string(response.Stderr), "[BLOCKED BY HISS-16] ") {
			t.Errorf("%s: pre-edit must fail closed: %+v", name, response)
		}
	}
}

func TestRunPreEditNoInterpreterDenies(t *testing.T) {
	root := repository(t, true)
	empty := pathOnly(t.TempDir())
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-edit", Stdin: strings.NewReader(`{"tool_name":"Edit","cwd":"` + root + `"}`),
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

func TestRunStopCleanAllows(t *testing.T) {
	root := syncedRepository(t)
	_, getenv := buildStub(t, "python3")
	stubStdout(t, cleanReport)
	response := Run(context.Background(), checkpointInvocation(t, "stop", root, getenv, []byte(`{}`)))
	if response.ExitCode != 0 {
		t.Errorf("clean stop must allow: %+v", response)
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
	if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "unavailable") {
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
	first := evaluateStop(context.Background(), root, Canonical{StopActive: false}, in)
	second := evaluateStop(context.Background(), root, Canonical{StopActive: true}, in)
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
