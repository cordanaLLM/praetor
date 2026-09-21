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

// Canonical events served by the entrypoint. EventPreEdit and EventPostTool reach the
// checkpoint evaluators (H2); EventStop reaches them too, alongside the agy dialect that
// already decoded and encoded it (H3).
const (
	EventPreTool     Event = "pre-tool"
	EventPreEdit     Event = "pre-edit"
	EventPostTool    Event = "post-tool"
	EventStop        Event = "stop"
	EventEnvironment Event = "environment"
	// EventPreDispatch validates a subagent brief before the native client launches it.
	EventPreDispatch Event = "pre-dispatch"
	// EventDispatchReceipt binds a validated brief to the native agent identifier emitted
	// after an asynchronous launch. It carries no agent-authored text of its own.
	EventDispatchReceipt Event = "dispatch-receipt"
	// EventDispatchAbort releases a brief reservation after native launch failure or denial.
	EventDispatchAbort Event = "dispatch-abort"
	// EventPreHandback validates the report a native subagent is about to deliver.
	EventPreHandback Event = "pre-handback"
	// EventHandbackReceipt records that a validated native handback tool succeeded.
	EventHandbackReceipt Event = "handback-receipt"
	// EventHandbackAbort releases a validated handback after native failure or denial.
	EventHandbackAbort Event = "handback-abort"
	// EventPostReturn validates a subagent return before the parent receives it.
	EventPostReturn Event = "post-return"
)

// Input and output bounds (HISS-02). MaxInputBytes is the bound of the Python adapters.
const (
	MaxInputBytes  = 1 << 20
	MaxReasonBytes = 4096
	// MaxDispatchBriefs bounds native clients that can launch a batch in one tool call.
	MaxDispatchBriefs = 64
)

// argumentShape is the whole grammar of both command-line arguments.
var argumentShape = regexp.MustCompile(`^[a-z-]+$`)

// ErrUnsupported reports a client and event pair without a registration row.
var ErrUnsupported = errors.New("unsupported hook client or event")

// Canonical is the client-neutral payload every dialect decodes into. FilePath,
// ConversationID, Step and StopActive are additive (H2): every dialect's Decode today
// leaves them at their zero value; a dialect gains them only when it starts reading the
// matching payload key, which keeps this an additive change for the existing dialects.
type Canonical struct {
	Event    Event
	Tool     string
	Command  string
	FilePath string
	// FilePath is additive (H2): dialect.Decode does not populate it from a real payload
	// yet (only pre-edit's normalised hand-off to checkpoint_scope.py sets it directly).
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
	// stop_hook_active field once a follow-up wires their Stop dialect; agy derives it
	// from Step.
	StopActive bool
	// Briefs contains one or more dispatch bodies. Native clients other than agy carry one;
	// agy's invoke_subagent tool can launch a bounded batch.
	Briefs []string
	// Return is the agent-authored body exposed at the native handoff boundary.
	Return string
	// ToolUseID identifies one dispatch call across its pre- and post-tool events.
	ToolUseID string
	// AgentID identifies the launched subagent across a dispatch receipt and its return.
	AgentID string
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
