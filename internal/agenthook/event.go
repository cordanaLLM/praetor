// Package agenthook is the one agent-hook entrypoint behind `praetorctl hook <client> <event>`.
//
// A client registration contains one executable call and nothing else: no shell
// substitution, no interpreter name. The client dialect decodes bounded stdin into one
// canonical payload, the evaluator judges it in process, and the dialect encodes the
// verdict the way that client expects. The workspace comes from the payload, never from
// the registration string.
package agenthook

import (
	"errors"
	"fmt"
	"regexp"
)

// Event is a canonical hook event, independent of any client's native event name.
type Event string

// Canonical events served by the entrypoint.
const (
	EventPreTool     Event = "pre-tool"
	EventEnvironment Event = "environment"
)

// Input and output bounds (HISS-02). MaxInputBytes is the bound of the Python adapters.
const (
	MaxInputBytes  = 1 << 20
	MaxReasonBytes = 4096
)

// argumentShape is the whole grammar of both command-line arguments.
var argumentShape = regexp.MustCompile(`^[a-z-]+$`)

// ErrUnsupported reports a client and event pair without a registration row.
var ErrUnsupported = errors.New("unsupported hook client or event")

// Canonical is the client-neutral payload every dialect decodes into.
type Canonical struct {
	Event      Event
	Tool       string
	Command    string
	Workspaces []string
}

// Outcome is the decision class of a verdict.
type Outcome int

// Outcomes. Skip is a neutral allow that always carries a stated reason (HISS-21).
const (
	Allow Outcome = iota
	Deny
	Skip
)

// Verdict is one decision with the reason a dialect reports for Deny and Skip.
type Verdict struct {
	Outcome Outcome
	Reason  string
}

// Response is what the process writes and the code it exits with.
type Response struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// String renders a response for diagnostics with its streams as text.
func (r Response) String() string {
	return fmt.Sprintf("exit %d stdout %q stderr %q", r.ExitCode, r.Stdout, r.Stderr)
}
