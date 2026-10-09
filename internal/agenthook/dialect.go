package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dialect is one row of the client dialect table: how a client's stdin becomes a
// Canonical payload and how a Verdict becomes that client's exit code and streams.
type Dialect struct {
	Client string
	// payloadClient names the client whose native event names the payload carries. The
	// Lefthook fallback receives the Claude shape.
	payloadClient string
	// commandTools are the tool names routed to the command policy. A payload without a
	// tool name is a command tool: the registration matcher already selected it.
	commandTools []string
	// everyToolIsCommand makes the command string mandatory whatever the tool is called.
	everyToolIsCommand bool
	// denyExit is the exit code of a deny; the reason always goes to stderr.
	denyExit int
	// allowMarker is the stdout line of an evaluated pre-tool allow; empty for none.
	allowMarker string
	// decode overrides Decode below for a client whose payload the shared decoder
	// cannot express (agy has no tool_input/cwd/hook_event_name shape at all). Nil
	// selects the generic decoder; the override still gets d, so it can reuse the row's
	// own data (commandTools) without duplicating the table.
	decode func(d Dialect, event Event, payload []byte) (Canonical, error)
	// encode overrides Encode below for the same reason: agy's stdout is always a JSON
	// decision object, never an exit-code-and-marker pair. Nil selects the generic
	// encoder.
	encode func(d Dialect, canonical Canonical, verdict Verdict) Response
}

// CommandPolicyMarker is the line the Lefthook dialect prints for an evaluated allow. It
// is the marker the Python guard prints, so both satisfy the same caller.
const CommandPolicyMarker = "PRAETOR_COMMAND_POLICY_OK"

var dialectTable = []Dialect{
	{Client: "claude", payloadClient: "claude", commandTools: []string{"Bash"}, denyExit: 2},
	{Client: "codex", payloadClient: "codex", commandTools: []string{"Bash"}, denyExit: 2},
	{Client: "gemini", payloadClient: "gemini", commandTools: []string{"run_shell_command"}, denyExit: 2},
	{Client: "lefthook", payloadClient: "claude", everyToolIsCommand: true, denyExit: 1, allowMarker: CommandPolicyMarker},
	// agy (Antigravity): commandTools carries the one docs-confirmed command tool
	// (dialect_agy.go); decode/encode are agy's own functions, not the generic pair
	// below, because its payload and its stdout are shaped nothing like the other four.
	{Client: "agy", payloadClient: "agy", commandTools: []string{"run_command"}, decode: agyDecode, encode: agyEncode},
}

// DialectFor returns the dialect of one client.
func DialectFor(client string) (Dialect, bool) {
	for _, dialect := range dialectTable {
		if dialect.Client == client {
			return dialect, true
		}
	}
	return Dialect{}, false
}

// Decode turns one bounded stdin payload into the canonical payload of event. The
// command-line event is authoritative: a payload naming another event is an error. A
// payload that is a JSON object but fails to decode still yields its event and the
// client's stop_hook_active flag (continuedStop), so the return boundary can bound the
// deny of a malformed SubagentStop payload the same way it bounds a register violation.
func (d Dialect) Decode(event Event, payload []byte) (Canonical, error) {
	if d.decode != nil {
		return d.decode(d, event, payload)
	}
	object, err := decodeObject(payload)
	if err != nil {
		return Canonical{}, err
	}
	canonical, err := d.decodeNative(event, object)
	if err != nil {
		return Canonical{Event: event, StopActive: continuedStop(event, object)}, err
	}
	return canonical, nil
}

func (d Dialect) decodeNative(event Event, object map[string]json.RawMessage) (Canonical, error) {
	if err := d.checkEventName(event, object); err != nil {
		return Canonical{}, err
	}
	if agentTrafficEvent(event) {
		return decodeNativeAgentTraffic(d.Client, event, object)
	}
	canonical, err := d.decodeNativeTool(event, object)
	if err == nil && event == EventStop {
		fillMainStop(d.payloadClient, &canonical, object)
	}
	return canonical, err
}

// continuedStop reports whether a stop or post-return payload says a stop hook already
// continued the turn or subagent. Only a JSON true counts; an absent or malformed flag is
// false.
func continuedStop(event Event, object map[string]json.RawMessage) bool {
	if event != EventPostReturn && event != EventStop {
		return false
	}
	active, _, err := optionalBool(object, "stop_hook_active")
	return err == nil && active
}

// decodeNativeTool reads the tool-hook shape the native clients share: tool name,
// workspace, and the command of a command tool's pre-tool call.
func (d Dialect) decodeNativeTool(event Event, object map[string]json.RawMessage) (Canonical, error) {
	canonical := Canonical{Event: event}
	var err error
	if canonical.Tool, err = optionalString(object, "tool_name"); err != nil {
		return Canonical{}, err
	}
	if err = fillWorkspace(&canonical, object); err != nil {
		return Canonical{}, err
	}
	if event == EventPreTool && d.isCommandTool(canonical.Tool) {
		if canonical.Command, err = commandOf(object); err != nil {
			return Canonical{}, err
		}
	}
	return canonical, nil
}

// Encode renders a verdict in the client's dialect. canonical carries the event that
// produced verdict (canonical.Event) plus whatever else the encoder needs from the
// decoded payload (agy's Encode reads canonical.StopActive for a Stop verdict).
func (d Dialect) Encode(canonical Canonical, verdict Verdict) Response {
	if d.encode != nil {
		return d.encode(d, canonical, verdict)
	}
	switch verdict.Outcome {
	case Allow:
		return d.encodeAllow(canonical, verdict)
	case Skip:
		return Response{Stderr: []byte("praetor hook: " + boundReason(verdict.Reason) + ", skipped\n")}
	default:
		return Response{Stderr: []byte(boundReason(verdict.Reason) + "\n"), ExitCode: d.denyExit}
	}
}

// encodeAllow renders an allow: a read-only context delivery as Claude Code's
// hookSpecificOutput (encodeDelivery), else the allow marker of a pre-tool row, with the
// verdict's notice, when it has one, on stderr.
func (d Dialect) encodeAllow(canonical Canonical, verdict Verdict) Response {
	if verdict.UpdatedInput != nil || verdict.AddedContext != "" {
		return d.encodeDelivery(canonical, verdict)
	}
	var stderr []byte
	if verdict.Notice != "" {
		stderr = []byte("praetor hook: " + boundReason(verdict.Notice) + "\n")
	}
	if d.allowMarker != "" && canonical.Event == EventPreTool {
		return Response{Stdout: []byte(d.allowMarker + "\n"), Stderr: stderr}
	}
	return Response{Stderr: stderr}
}

// encodeDelivery renders a read-only context delivery in Claude Code's hookSpecificOutput
// (code.claude.com/docs/en/hooks): at PreToolUse an allow decision with updatedInput, the whole
// tool input, and the notice as its reason; at SubagentStart additionalContext, which lands in
// the subagent's context before its first prompt. Any other client fails closed.
func (d Dialect) encodeDelivery(canonical Canonical, verdict Verdict) Response {
	if d.Client != "claude" {
		return Response{Stderr: []byte("praetor hook: " + d.Client + " has no context delivery shape\n"), ExitCode: d.denyExit}
	}
	specific := map[string]any{"hookEventName": nativeEventName(d.Client, canonical.Event)}
	if verdict.UpdatedInput != nil {
		specific["permissionDecision"] = "allow"
		specific["permissionDecisionReason"] = boundReason(verdict.Notice)
		specific["updatedInput"] = verdict.UpdatedInput
	} else {
		specific["additionalContext"] = verdict.AddedContext
	}
	data, err := json.Marshal(map[string]any{"hookSpecificOutput": specific})
	if err != nil {
		return Response{Stderr: []byte("praetor hook: encode response: " + err.Error() + "\n"), ExitCode: d.denyExit}
	}
	return Response{Stdout: append(data, '\n')}
}

// nativeEventName returns the native event of client's first registration row for event.
func nativeEventName(client string, event Event) string {
	for _, row := range Registrations(client) {
		if row.Event == event {
			return row.NativeEvent
		}
	}
	return ""
}

func (d Dialect) isCommandTool(tool string) bool {
	return d.everyToolIsCommand || tool == "" || slices.Contains(d.commandTools, tool)
}

func (d Dialect) checkEventName(event Event, object map[string]json.RawMessage) error {
	named, err := optionalString(object, "hook_event_name")
	if err != nil || named == "" {
		return err
	}
	for _, row := range Registrations(d.payloadClient) {
		if row.Event == event && row.NativeEvent == named {
			return nil
		}
	}
	return fmt.Errorf("payload event %q contradicts the %s event", boundReason(named), event)
}

// decodeObject accepts exactly one JSON object of valid UTF-8, as the Python guard does.
func decodeObject(payload []byte) (map[string]json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, errors.New("hook input is empty")
	}
	if len(payload) > MaxInputBytes {
		return nil, errors.New("hook input exceeds 1 MiB")
	}
	if !utf8.Valid(payload) {
		return nil, errors.New("hook input is not valid UTF-8")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, errors.New("hook input must be one JSON object")
	}
	if object == nil {
		return nil, errors.New("hook input must be an object")
	}
	return object, nil
}

// optionalString reads a string member. An absent or null member is empty; any other
// type is an error rather than a silent default.
func optionalString(object map[string]json.RawMessage, key string) (string, error) {
	raw, present := object[key]
	if !present || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be text", key)
	}
	return value, nil
}

func commandOf(object map[string]json.RawMessage) (string, error) {
	input, err := requiredObject(object, "tool_input")
	if err != nil {
		return "", err
	}
	command, err := requiredString(input, "command")
	if err != nil {
		return "", fmt.Errorf("tool_input.%w", err)
	}
	return command, nil
}

// isPythonSpace is Python's str.isspace for one rune: Unicode white space plus the four
// information separators, so "nonempty" means the same to both guards.
func isPythonSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// boundReason keeps a diagnostic inside MaxReasonBytes without splitting a rune.
func boundReason(reason string) string {
	if len(reason) <= MaxReasonBytes {
		return reason
	}
	return strings.ToValidUTF8(reason[:MaxReasonBytes], "")
}
