package agenthook

import (
	"fmt"
	"time"
)

// Registration is one row of the client registration table: which native event of which
// client reaches which canonical event, and the bounds the registration declares.
type Registration struct {
	Client string
	Event  Event
	// NativeEvent is the client's own name for the event: the settings key of a native
	// client, the job name of the Lefthook fallback.
	NativeEvent string
	// Matcher is the client's tool matcher; empty where the client has none.
	Matcher string
	// Timeout is the budget the registration declares; zero where the client has none.
	Timeout time.Duration
}

// Command is the complete registration string for tracked files. It is one executable
// call that resolves through PATH or PATHEXT and is valid under `sh -c` and `cmd /c`.
func (r Registration) Command() string {
	return "praetorctl hook " + r.Client + " " + string(r.Event)
}

// registrationTable is the support matrix of the entrypoint. A pair without a row is
// rejected before any input is read.
var registrationTable = []Registration{
	{Client: "claude", Event: EventPreTool, NativeEvent: "PreToolUse", Matcher: "^Bash$", Timeout: 15 * time.Second},
	{Client: "codex", Event: EventPreTool, NativeEvent: "PreToolUse", Matcher: "^Bash$", Timeout: 15 * time.Second},
	{Client: "gemini", Event: EventPreTool, NativeEvent: "BeforeTool", Matcher: "run_shell_command", Timeout: 15 * time.Second},
	{Client: "lefthook", Event: EventPreTool, NativeEvent: "agent-pre-tool"},
	{Client: "lefthook", Event: EventEnvironment, NativeEvent: "pre-rebase"},
}

// Registrations returns a copy of the rows of one client, in table order. An unknown
// client has no rows.
func Registrations(client string) []Registration {
	rows := make([]Registration, 0, len(registrationTable))
	for _, row := range registrationTable {
		if row.Client == client {
			rows = append(rows, row)
		}
	}
	return rows
}

// ParseArguments validates the two command-line arguments against the argument grammar
// and the registration table. It returns the registration row that serves the pair.
func ParseArguments(client, event string) (Registration, error) {
	if !argumentShape.MatchString(client) || !argumentShape.MatchString(event) {
		return Registration{}, fmt.Errorf("%w: arguments must match %s", ErrUnsupported, argumentShape)
	}
	for _, row := range registrationTable {
		if row.Client == client && row.Event == Event(event) {
			return row, nil
		}
	}
	return Registration{}, fmt.Errorf("%w: %s %s", ErrUnsupported, client, event)
}
