package agenthook

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

// Evaluation budgets (3.3 of the rollout spec), each inside its registration's own timeout
// (registrations.go). Stop nests two of its own: state verification, then the checkpoint due
// check; budgetFor sums them for the one outer deadline Run applies.
const (
	preToolBudget        = 10 * time.Second
	preEditBudget        = 10 * time.Second
	postToolBudget       = 30 * time.Second
	stateVerifyBudget    = 20 * time.Second
	stopCheckpointBudget = 30 * time.Second
	environmentBudget    = 5 * time.Second
)

// budgetFor is the outer context deadline Run applies before evaluate reads anything.
func budgetFor(event Event) time.Duration {
	switch event {
	case EventPreEdit:
		return preEditBudget
	case EventPostTool:
		return postToolBudget
	case EventStop:
		return stateVerifyBudget + stopCheckpointBudget
	case EventEnvironment:
		return environmentBudget
	default:
		return preToolBudget
	}
}

// evaluate is the one dispatcher behind Run: decode, resolve the workspace, judge. It never
// returns an error; every failure becomes a Verdict a dialect can encode. It returns the
// canonical payload alongside the verdict, matching H3's Dialect.Encode(Canonical, Verdict)
// (dialect.go, out of scope here): an encoder needs fields off canonical, such as agy's
// StopActive, that the verdict alone cannot carry, and every return below reports whatever
// of canonical it managed to build before the failure that produced its verdict.
func evaluate(ctx context.Context, dialect Dialect, row Registration, in Invocation) (Canonical, Verdict) {
	canonical, err := decodeCanonical(ctx, dialect, row.Event, in)
	if err != nil {
		return Canonical{Event: row.Event}, Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] Invalid hook input: " + err.Error()}
	}
	root, verdict, ok := resolveGovernedRoot(ctx, canonical, in)
	if !ok {
		return canonical, verdict
	}
	if verdict := Environment(in.Getenv); verdict.Outcome != Allow {
		return canonical, verdict
	}
	return canonical, dispatch(ctx, row, canonical, root, in)
}

func decodeCanonical(ctx context.Context, dialect Dialect, event Event, in Invocation) (Canonical, error) {
	if event == EventEnvironment {
		return Canonical{Event: event}, nil
	}
	payload, err := readBounded(ctx, in.Stdin)
	if err != nil {
		return Canonical{}, err
	}
	return dialect.Decode(event, payload)
}

// resolveGovernedRoot answers the workspace root and whether evaluation continues. The second
// return is the verdict to use when it does not: no repository or an ungoverned workspace
// under the governed-only scope are a neutral skip, never a failure.
func resolveGovernedRoot(ctx context.Context, canonical Canonical, in Invocation) (string, Verdict, bool) {
	root, err := ResolveRoot(ctx, canonical.Workspaces, in.WorkDir)
	switch {
	case err != nil:
		return "", Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] " + err.Error()}, false
	case root == "":
		return "", Verdict{Outcome: Skip, Reason: "no repository"}, false
	case !Governed(root) && !AppliesEverywhere(in.Settings):
		return "", Verdict{Outcome: Skip, Reason: "workspace not governed"}, false
	}
	return root, Verdict{}, true
}

// checkpointWired names the clients whose pre-edit, post-tool and stop rows this change
// routes to the checkpoint evaluators: claude, codex and gemini (registrations.go). agy
// already carries its own Stop row (H3), decoded and encoded entirely by its own dialect
// functions (dialect_agy.go, out of scope here); its recorded and docs-derived goldens
// expect the plain command-policy flow below, not a Go-native state verify or a
// checkpoint.py hand-off, so it keeps that flow until a follow-up wires agy specifically.
func checkpointWired(client string) bool {
	return client == "claude" || client == "codex" || client == "gemini"
}

func dispatch(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	if !checkpointWired(row.Client) {
		return evaluateCommand(canonical, in)
	}
	switch row.Event {
	case EventPreEdit:
		return evaluatePreEdit(ctx, row, canonical, root, in)
	case EventPostTool:
		return evaluatePostTool(ctx, root, in)
	case EventStop:
		return evaluateStop(ctx, root, canonical, in)
	default:
		return evaluateCommand(canonical, in)
	}
}

func evaluateCommand(canonical Canonical, in Invocation) Verdict {
	if canonical.Command == "" {
		return Verdict{Outcome: Allow}
	}
	return in.Policy.Command(canonical.Command)
}

// noInterpreterVerdict differs per event (3.3): pre-edit and stop fail closed (deny; stop's
// deny is its block-once), post-tool is a stated skip since it can only annotate, never block.
func noInterpreterVerdict(event Event) Verdict {
	const reason = "checkpoint evaluator unavailable: no Python interpreter"
	if event == EventPostTool {
		return Verdict{Outcome: Skip, Reason: reason}
	}
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] " + reason}
}

// evaluatePreEdit runs checkpoint_scope.py with the normalised payload it expects: the
// canonical fields already decoded, rather than the client's raw event, so the same call
// shape works whichever native client's payload produced them.
func evaluatePreEdit(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	argv, err := ResolveInterpreter(in.Getenv, InterpreterCandidates(in.Settings))
	if err != nil {
		return noInterpreterVerdict(EventPreEdit)
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": row.NativeEvent,
		"tool_name":       canonical.Tool,
		"tool_input":      map[string]string{"file_path": canonical.FilePath},
		"cwd":             root,
	})
	if err != nil {
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] normalise pre-edit payload: " + err.Error()}
	}
	result, runErr := runInterpreter(ctx, argv, root, checkpointScript(root, "checkpoint_scope.py"), nil, payload)
	_, markerErr := extractMarker(result.Stdout, checkpointScopeMarker)
	if runErr != nil || markerErr != nil {
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] checkpoint scope evaluator failed: " + checkpointFailureReason(runErr, markerErr, result)}
	}
	return Verdict{Outcome: Allow}
}

// evaluatePostTool never blocks: it annotates. An evaluator that cannot answer, or answers
// with nothing due, both allow; only a due checkpoint carries the note.
func evaluatePostTool(ctx context.Context, root string, in Invocation) Verdict {
	argv, err := ResolveInterpreter(in.Getenv, InterpreterCandidates(in.Settings))
	if err != nil {
		return noInterpreterVerdict(EventPostTool)
	}
	result, runErr := runInterpreter(ctx, argv, root, checkpointScript(root, "checkpoint.py"), []string{"--event", "tool", "--json", "--marker"}, nil)
	line, markerErr := extractMarker(result.Stdout, checkpointResultMarker)
	if runErr != nil || markerErr != nil {
		return Verdict{Outcome: Skip, Reason: "checkpoint due-status unavailable: " + checkpointFailureReason(runErr, markerErr, result)}
	}
	report, decodeErr := decodeCheckpointReport(line)
	if decodeErr != nil {
		return Verdict{Outcome: Skip, Reason: "checkpoint due-status unavailable: " + decodeErr.Error()}
	}
	if !report.Enabled || !report.Due {
		return Verdict{Outcome: Allow}
	}
	return Verdict{Outcome: Skip, Reason: "Praetor checkpoint due: " + strings.Join(report.Actions, "; ")}
}

// evaluateStop verifies the state ledger before the checkpoint itself (3.3): a stale or
// unverifiable ledger blocks on its own, independent of whether a checkpoint is due.
func evaluateStop(ctx context.Context, root string, canonical Canonical, in Invocation) Verdict {
	verifyCtx, cancel := context.WithTimeout(ctx, stateVerifyBudget)
	defer cancel()
	if err := state.VerifyStateSync(verifyCtx, root); err != nil {
		reason := "Praetor state could not be verified: " + err.Error() +
			". Inspect and repair the existing ledger, run praetorctl state sync ., then retry; " +
			"do not report completion while state is unverified."
		return Verdict{Outcome: Deny, Reason: stopReason(reason, canonical.StopActive)}
	}
	return evaluateStopCheckpoint(ctx, root, canonical, in)
}

func evaluateStopCheckpoint(ctx context.Context, root string, canonical Canonical, in Invocation) Verdict {
	argv, err := ResolveInterpreter(in.Getenv, InterpreterCandidates(in.Settings))
	if err != nil {
		verdict := noInterpreterVerdict(EventStop)
		verdict.Reason = stopReason(verdict.Reason, canonical.StopActive)
		return verdict
	}
	checkCtx, cancel := context.WithTimeout(ctx, stopCheckpointBudget)
	defer cancel()
	result, runErr := runInterpreter(checkCtx, argv, root, checkpointScript(root, "checkpoint.py"), []string{"--event", "stop", "--json", "--marker"}, nil)
	line, markerErr := extractMarker(result.Stdout, checkpointResultMarker)
	if runErr != nil || markerErr != nil {
		reason := "Praetor checkpoint could not be verified: " + checkpointFailureReason(runErr, markerErr, result)
		return Verdict{Outcome: Deny, Reason: stopReason(reason, canonical.StopActive)}
	}
	report, decodeErr := decodeCheckpointReport(line)
	if decodeErr != nil {
		reason := "Praetor checkpoint could not be verified: " + decodeErr.Error()
		return Verdict{Outcome: Deny, Reason: stopReason(reason, canonical.StopActive)}
	}
	if !report.Enabled || !report.Due {
		return Verdict{Outcome: Allow}
	}
	reason := "Praetor checkpoint due: " + strings.Join(report.Actions, "; ") +
		". Review and stage only task-owned public changes; run required verification and normal " +
		"signed-off commits/pushes. Reuse an existing PR or create a draft for the exact pushed " +
		"branch. Preserve unrelated and private files. Report a concrete blocker if this cannot complete."
	return Verdict{Outcome: Deny, Reason: stopReason(reason, canonical.StopActive)}
}

// stopReason mirrors the wording split the adapter's blocked() made between a first Stop pass
// and a second, StopActive one (`.config/agent/hooks/checkpoint.py:58-63`, deleted in H4): the
// first pass states the block reason as-is, a repeated pass makes explicit that the checkpoint
// is still incomplete. Verdict carries one Reason string, so this collapses the adapter's two
// JSON fields (stopReason, systemMessage) into the one a future per-dialect stop encoding
// (H3/H4, once Dialect.Encode takes the full Canonical) can still split back apart if it needs
// both texts; until then, this is what reaches the client on either pass.
func stopReason(reason string, active bool) string {
	if active {
		return "Checkpoint remains incomplete: " + reason
	}
	return reason
}
