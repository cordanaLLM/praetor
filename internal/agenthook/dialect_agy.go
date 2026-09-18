package agenthook

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Antigravity ("agy") hook payload and response shapes, verified against the
// "Lifecycle Hooks (hooks.json)" contract embedded in the installed Antigravity 1.2.6
// binary (agy --help at /home/kilian/.local/bin/agy; the contract text is a string
// literal in the binary itself, printed by the CLI's own in-app documentation, not
// model memory). docs/guides/agent-hooks.md quotes the relevant sections verbatim.
//
// What is docs-confirmed and lands here directly (decision row 7,
// .workingdir/planning/wrap-fork-rollout-spec-20260917.md:854):
//   - the common fields on every payload: conversationId, workspacePaths, stepIdx
//     (PreToolUse) / executionNum (Stop);
//   - PreToolUse's toolCall.name and toolCall.args.CommandLine for the run_command tool;
//   - PreToolUse's stdout: {"decision":"allow"|"deny","reason":...};
//   - Stop's stdin terminationReason/fullyIdle and its stdout {"decision":"continue",...}
//     versus any other value, which lets the agent stop.
//
// What the docs do not state and stays unverified until a recorded fixture supplies it
// (same decision row): the exact process shell and cwd Antigravity hands the command,
// its exit-code handling, the argument key of any tool besides run_command, and which
// tool names besides run_command belong on the command-tool list (an "edit tool" list
// specifically). Until then agyIsCommandTool only ever matches run_command, and every
// other tool name is allowed rather than misclassified.

// agyCommandArgKey is the docs-confirmed argument key of the run_command tool call, for
// example: "toolCall":{"name":"run_command","args":{"CommandLine":"npm test"}}.
const agyCommandArgKey = "CommandLine"

// agyToolCall is the docs-confirmed shape of a PreToolUse payload's "toolCall" member.
type agyToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// agyCommon is the "common fields" every agy hook payload carries (transcriptPath,
// artifactDirectoryPath and modelName are read by nothing here, so they are left for the
// json package to ignore rather than declared and unused).
type agyCommon struct {
	ConversationID *string  `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
}

// agyPreToolPayload is PreToolUse's documented stdin shape.
type agyPreToolPayload struct {
	agyCommon
	ToolCall *agyToolCall `json:"toolCall"`
	StepIdx  *int         `json:"stepIdx"`
}

// agyStopPayload is Stop's documented stdin shape.
type agyStopPayload struct {
	agyCommon
	ExecutionNum      *int    `json:"executionNum"`
	TerminationReason *string `json:"terminationReason"`
	FullyIdle         *bool   `json:"fullyIdle"`
}

// agyDecode implements Dialect.decode for agy. Unlike the native-client shape, an agy
// payload carries no event-name field to contradict against the argv event (section 3.5
// of the rollout spec): the argv event alone selects which shape below applies.
func agyDecode(d Dialect, event Event, payload []byte) (Canonical, error) {
	switch event {
	case EventPreTool:
		return agyDecodePreTool(d, payload)
	case EventStop:
		return agyDecodeStop(payload)
	default:
		return Canonical{}, fmt.Errorf("%w: agy has no payload shape for %s", ErrUnsupported, event)
	}
}

func agyDecodePreTool(d Dialect, payload []byte) (Canonical, error) {
	if _, err := decodeObject(payload); err != nil {
		return Canonical{}, err
	}
	var doc agyPreToolPayload
	if err := json.Unmarshal(payload, &doc); err != nil {
		return Canonical{}, fmt.Errorf("agy pre-tool payload: %w", err)
	}
	canonical := Canonical{Event: EventPreTool}
	agyFillCommon(&canonical, doc.agyCommon)
	if doc.StepIdx != nil {
		canonical.Step = *doc.StepIdx
	}
	if doc.ToolCall == nil {
		return canonical, nil // no tool call at all: nothing to classify, allow
	}
	canonical.Tool = doc.ToolCall.Name
	if !agyIsCommandTool(d, canonical.Tool) {
		return canonical, nil // unclassified tool (or an edit tool: unverified, 3.5): allow
	}
	command, err := agyCommandOf(doc.ToolCall.Args)
	if err != nil {
		return Canonical{}, err
	}
	canonical.Command = command
	return canonical, nil
}

func agyDecodeStop(payload []byte) (Canonical, error) {
	if _, err := decodeObject(payload); err != nil {
		return Canonical{}, err
	}
	var doc agyStopPayload
	if err := json.Unmarshal(payload, &doc); err != nil {
		return Canonical{}, fmt.Errorf("agy stop payload: %w", err)
	}
	canonical := Canonical{Event: EventStop}
	agyFillCommon(&canonical, doc.agyCommon)
	if doc.ExecutionNum != nil {
		canonical.Step = *doc.ExecutionNum
	}
	// executionNum's zero-point is not documented; the docs' own example
	// ("executionNum": 1) is not labelled as the first attempt either way. Treating 1 as
	// the first execution matches how the docs show stepIdx and invocationNum (both
	// already above zero in their one shown example, never stated as zero-based).
	// Confirm against a recorded fixture before this backs a real block decision (H2).
	canonical.StopActive = canonical.Step > 1
	return canonical, nil
}

func agyFillCommon(canonical *Canonical, common agyCommon) {
	if common.ConversationID != nil {
		canonical.ConversationID = *common.ConversationID
	}
	if len(common.WorkspacePaths) > 0 && common.WorkspacePaths[0] != "" {
		canonical.Workspaces = []string{common.WorkspacePaths[0]}
	}
}

// agyIsCommandTool reuses the row's commandTools data (run_command only, today) but not
// the generic Dialect.isCommandTool method: that method treats an empty tool name as a
// command by default, which is correct for the other four dialects (their registration
// matches Bash alone) but wrong for agy, whose registration matches every tool ("*") so
// an empty or missing name must not be assumed to be run_command.
func agyIsCommandTool(d Dialect, tool string) bool {
	for _, candidate := range d.commandTools {
		if candidate == tool {
			return true
		}
	}
	return false
}

// agyCommandOf extracts the docs-confirmed CommandLine argument of a run_command call.
func agyCommandOf(args json.RawMessage) (string, error) {
	if len(args) == 0 || string(args) == "null" {
		return "", fmt.Errorf("toolCall.args.%s must be nonempty text", agyCommandArgKey)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", fmt.Errorf("toolCall.args must be an object: %w", err)
	}
	raw, present := fields[agyCommandArgKey]
	if !present || string(raw) == "null" {
		return "", fmt.Errorf("toolCall.args.%s must be nonempty text", agyCommandArgKey)
	}
	var command string
	if err := json.Unmarshal(raw, &command); err != nil {
		return "", fmt.Errorf("toolCall.args.%s must be text: %w", agyCommandArgKey, err)
	}
	if strings.TrimFunc(command, isPythonSpace) == "" {
		return "", fmt.Errorf("toolCall.args.%s must be nonempty text", agyCommandArgKey)
	}
	return command, nil
}

// agyEncode implements Dialect.encode for agy: always exit 0, always a JSON decision
// object on stdout (the docs' contract has no exit-code channel at all).
func agyEncode(d Dialect, canonical Canonical, verdict Verdict) Response {
	switch canonical.Event {
	case EventPreTool:
		return agyEncodePreTool(verdict)
	case EventStop:
		return agyEncodeStop(canonical, verdict)
	default:
		// PostToolUse, PreInvocation and PostInvocation are H2/H4 territory (no
		// evaluator or registration row exists for them yet); fail closed rather than
		// answer a shape nothing has verified.
		return Response{Stderr: []byte("praetor hook: agy has no encoder for " + boundReason(string(canonical.Event)) + "\n"), ExitCode: usageExit}
	}
}

func agyEncodePreTool(verdict Verdict) Response {
	body := map[string]any{"decision": "allow"}
	var stderr []byte
	switch verdict.Outcome {
	case Allow:
	case Skip:
		stderr = []byte("praetor hook: " + boundReason(verdict.Reason) + ", skipped\n")
	default: // Deny and any outcome this dialect does not recognise fail closed.
		body["decision"] = "deny"
		body["reason"] = boundReason(verdict.Reason)
	}
	return agyRespond(body, stderr)
}

// agyEncodeStop implements the rollout spec's 3.4 "stop: block once" rule: a Stop verdict
// only ever denies when the payload itself was unreadable or the workspace could not be
// resolved (evaluate has no real Stop evaluator to consult yet, H2), and the spec allows
// answering "continue" for that once per conversation, not indefinitely. A Skip (neutral,
// ungoverned workspace) is not blocking per 3.4's own table and always lets the agent
// stop.
func agyEncodeStop(canonical Canonical, verdict Verdict) Response {
	if verdict.Outcome != Deny || canonical.StopActive {
		return agyRespond(map[string]any{}, nil)
	}
	return agyRespond(map[string]any{"decision": "continue", "reason": boundReason(verdict.Reason)}, nil)
}

func agyRespond(body map[string]any, stderr []byte) Response {
	encoded, err := json.Marshal(body)
	if err != nil {
		return Response{Stderr: []byte("praetor hook: encode agy response: " + err.Error() + "\n"), ExitCode: usageExit}
	}
	return Response{Stdout: append(encoded, '\n'), Stderr: stderr}
}
