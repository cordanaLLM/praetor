package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
}

// CommandPolicyMarker is the line the Lefthook dialect prints for an evaluated allow. It
// is the marker the Python guard prints, so both satisfy the same caller.
const CommandPolicyMarker = "PRAETOR_COMMAND_POLICY_OK"

var dialectTable = []Dialect{
	{Client: "claude", payloadClient: "claude", commandTools: []string{"Bash"}, denyExit: 2},
	{Client: "codex", payloadClient: "codex", commandTools: []string{"Bash"}, denyExit: 2},
	{Client: "gemini", payloadClient: "gemini", commandTools: []string{"run_shell_command"}, denyExit: 2},
	{Client: "lefthook", payloadClient: "claude", everyToolIsCommand: true, denyExit: 1, allowMarker: CommandPolicyMarker},
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
// command-line event is authoritative: a payload naming another event is an error.
func (d Dialect) Decode(event Event, payload []byte) (Canonical, error) {
	object, err := decodeObject(payload)
	if err != nil {
		return Canonical{}, err
	}
	if err := d.checkEventName(event, object); err != nil {
		return Canonical{}, err
	}
	canonical := Canonical{Event: event}
	if canonical.Tool, err = optionalString(object, "tool_name"); err != nil {
		return Canonical{}, err
	}
	workspace, err := optionalString(object, "cwd")
	if err != nil {
		return Canonical{}, err
	}
	if workspace != "" {
		canonical.Workspaces = []string{workspace}
	}
	if event == EventPreTool && d.isCommandTool(canonical.Tool) {
		if canonical.Command, err = commandOf(object); err != nil {
			return Canonical{}, err
		}
	}
	return canonical, nil
}

// Encode renders a verdict in the client's dialect.
func (d Dialect) Encode(event Event, verdict Verdict) Response {
	switch verdict.Outcome {
	case Allow:
		if d.allowMarker != "" && event == EventPreTool {
			return Response{Stdout: []byte(d.allowMarker + "\n")}
		}
		return Response{}
	case Skip:
		return Response{Stderr: []byte("praetor hook: " + boundReason(verdict.Reason) + ", skipped\n")}
	default:
		return Response{Stderr: []byte(boundReason(verdict.Reason) + "\n"), ExitCode: d.denyExit}
	}
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
	var input map[string]json.RawMessage
	if err := json.Unmarshal(object["tool_input"], &input); err != nil || input == nil {
		return "", errors.New("tool_input must be an object")
	}
	command, err := optionalString(input, "command")
	if err != nil || strings.TrimSpace(command) == "" {
		return "", errors.New("tool_input.command must be nonempty text")
	}
	return command, nil
}

// boundReason keeps a diagnostic inside MaxReasonBytes without splitting a rune.
func boundReason(reason string) string {
	if len(reason) <= MaxReasonBytes {
		return reason
	}
	return strings.ToValidUTF8(reason[:MaxReasonBytes], "")
}
