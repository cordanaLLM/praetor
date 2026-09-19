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

// Canonical events served by the entrypoint. pre-edit and post-tool (section 3.1 of the
// rollout spec) are not wired yet: their evaluator is H2's checkpoint.py hand-off, not
// built here.
const (
	EventPreTool     Event = "pre-tool"
	EventEnvironment Event = "environment"
	// EventStop is the client's execution-loop-termination event. Only the agy dialect
	// decodes and encodes it today (H3); the other clients gain it with H2's checkpoint
	// state-ledger verify.
	EventStop Event = "stop"
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
	// ConversationID identifies the agent conversation a payload belongs to. Populated
	// only by a dialect whose native payload carries one (agy's conversationId); empty
	// for a dialect that has none.
	ConversationID string
	// Step is the payload's own step counter, reused across event kinds because the
	// rollout spec declares one generic field (3.2): agy's stepIdx for a tool event,
	// agy's executionNum for a Stop event. Zero means the payload carried none.
	Step int
	// StopActive reports whether a Stop payload is a repeat for its ConversationID, so
	// an encoder can bound how many times it answers "continue" for one conversation
	// (3.4's "block once"). claude and codex read this from the client's own
	// stop_hook_active field once H2 wires their Stop dialect; agy derives it from Step.
	StopActive bool
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
