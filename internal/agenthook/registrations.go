package agenthook

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
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

// Command is the engine call that serves the row: one executable call that resolves through
// PATH or PATHEXT and is valid under `sh -c` and `cmd /c`. A hand-written registration can
// carry it as is. This repository's tracked client files reach it through the skew guard
// .config/agent/hooks/praetor_hook.py (the AGY plugin through its own copy beside hooks.json),
// which hands the call only to an engine whose usage lists this command and otherwise skips,
// so an engine older than the row never blocks the client (docs/guides/agent-hooks.md,
// Rollout).
func (r Registration) Command() string {
	return "praetorctl hook " + r.Client + " " + string(r.Event)
}

// ServedBy reports whether a hook handler's command line already runs this row's evaluator:
// the engine call itself under any path to praetorctl or standardsctl, the skew guard this
// repository's tracked files use (praetor_hook.py <client> <event>), or, for the pre-tool row,
// one of the Python adapters the engine call replaced. Adoption asks it before registering a
// row, so a repository that already wires the evaluator is not given a second copy that runs
// the same policy twice on every call.
func (r Registration) ServedBy(commandLine string) bool {
	tokens := strings.Fields(commandLineSeparators.Replace(commandLine))
	for i, token := range tokens {
		if r.servedFrom(strings.TrimSuffix(path.Base(token), ".exe"), tokens[i+1:]) {
			return true
		}
	}
	return false
}

// servedFrom judges one command-line token, reduced to its base name, and the tokens after it.
func (r Registration) servedFrom(name string, rest []string) bool {
	switch {
	case r.Event == EventPreTool && slices.Contains(preToolAdapters, name):
		return true
	case name == skewGuardScript:
		return r.invokedBy(rest)
	case name == util.PraetorCLI || name == util.LegacyCLI:
		return len(rest) > 0 && rest[0] == "hook" && r.invokedBy(rest[1:])
	default:
		return false
	}
}

// invokedBy reports whether args open with this row's client and event.
func (r Registration) invokedBy(args []string) bool {
	return len(args) >= 2 && args[0] == r.Client && args[1] == string(r.Event)
}

// commandLineSeparators turns the quoting and the Windows path separator of a registered
// command line into forms strings.Fields and path.Base split on.
var commandLineSeparators = strings.NewReplacer(`"`, " ", "'", " ", `\`, "/")

// skewGuardScript is the base name of the skew guard (Command); preToolAdapters are the base
// names of the Python pre-tool adapters that preceded the engine call: this repository's
// command guards and the interceptor adoption scaffolds.
const skewGuardScript = "praetor_hook.py"

var preToolAdapters = []string{"command_guard.py", "codex_pre_tool.py", "block_evasion.py"}

// HookFile is where a native client reads the hook registrations of a repository, the unit the
// timeout field of a registration counts in there, whether the client strips comments before
// it parses the file, so a JSONC file a strict merge refuses is still one it reads, and whether
// it reads a matcher of only letters, digits and _ as the exact tool name
// (clientjson.Hook.ExactLiteral).
type HookFile struct {
	Path         string
	TimeoutUnit  time.Duration
	Comments     bool
	ExactLiteral bool
}

// nativeHookFiles are the repository files carrying each native client's registrations. The
// units are the ones this repository's tracked files use: Claude Code and Codex count seconds
// (timeout 15), Gemini CLI milliseconds (timeout 15000). Gemini CLI's settings loader parses
// JSON.parse(stripJsonComments(content)), so its file may carry comments.
//
// Matchers: Claude Code evaluates a matcher of only letters, digits, _, -, spaces, `,` and `|`
// as exact names and any other one as an unanchored JavaScript regular expression
// (code.claude.com/docs/en/hooks, "Matcher patterns"); Codex takes a matcher of only ASCII
// letters, digits, _ and `|` as exact names and any other one as an unanchored regex::Regex
// (openai/codex rust-v0.145.0, codex-rs/hooks/src/events/common.rs, matches_matcher). For both,
// Bash and ^Bash$ select one tool. Gemini CLI runs new RegExp(matcher).test(toolName) on every
// matcher (google-gemini/gemini-cli v0.61.0, packages/core/src/hooks/hookPlanner.ts,
// matchesToolName), so there run_shell_command also selects any tool whose name contains it.
// Its pre-tool row is therefore ^run_shell_command$, the one tool its dialect routes to the
// command policy (dialectTable, commandTools); a bare run_shell_command group an earlier
// adoption wrote still serves that row (clientjson coversMatcher).
var nativeHookFiles = map[string]HookFile{
	string(clientid.Claude): {Path: ".claude/settings.json", TimeoutUnit: time.Second, ExactLiteral: true},
	string(clientid.Codex):  {Path: ".codex/hooks.json", TimeoutUnit: time.Second, ExactLiteral: true},
	string(clientid.Gemini): {Path: ".gemini/settings.json", TimeoutUnit: time.Millisecond, Comments: true},
}

// NativeHookFile returns the repository hook file of client, or false for a client without
// one: AGY registers through its plugin, and registrationTable has no pre-tool row for the
// context-only clients (Cursor, Windsurf, Copilot), whatever hook surface the client itself
// offers.
func NativeHookFile(client string) (HookFile, bool) {
	file, ok := nativeHookFiles[client]
	return file, ok
}

// registrationTable is the support matrix of the entrypoint. A pair without a row is
// rejected before any input is read. The checkpoint rows (pre-edit, post-tool, stop) reach
// the native clients only (H2); Lefthook keeps its H1 rows until its jobs are re-pointed at
// this entrypoint (H4), so `lefthook post-tool` etc. reach no evaluator here: they are answered
// as a pair this engine does not serve (unsupportedResponse).
// Codex carries no pre-edit row: measured fact, section 1 of the rollout spec ("no pre-edit
// event registered today").
var registrationTable = []Registration{
	{Client: "claude", Event: EventPreTool, NativeEvent: "PreToolUse", Matcher: "^Bash$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventPreEdit, NativeEvent: "PreToolUse", Matcher: "^(Edit|Write)$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventPostTool, NativeEvent: "PostToolUse", Timeout: 60 * time.Second},
	{Client: "claude", Event: EventStop, NativeEvent: "Stop", Timeout: 60 * time.Second},
	{Client: "claude", Event: EventPreDispatch, NativeEvent: "PreToolUse", Matcher: "^Agent$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventDispatchReceipt, NativeEvent: "PostToolUse", Matcher: "^Agent$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventDispatchAbort, NativeEvent: "PostToolUseFailure", Matcher: "^Agent$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventDispatchAbort, NativeEvent: "PermissionDenied", Matcher: "^Agent$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventPreHandback, NativeEvent: "PreToolUse", Matcher: "^SubagentHandback$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventHandbackReceipt, NativeEvent: "PostToolUse", Matcher: "^SubagentHandback$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventHandbackAbort, NativeEvent: "PostToolUseFailure", Matcher: "^SubagentHandback$", Timeout: 15 * time.Second},
	{Client: "claude", Event: EventHandbackAbort, NativeEvent: "PermissionDenied", Matcher: "^SubagentHandback$", Timeout: 15 * time.Second},
	// SubagentStop also fires for Claude Code's internal agents (prompt suggestions, /btw side
	// questions), whose agent_type is empty unless the session runs as a named agent. The
	// matcher selects a nonempty agent_type, so those human-facing answers never reach the
	// gate; an internal agent under a named session agent is uncorrelated and skipped.
	{Client: "claude", Event: EventPostReturn, NativeEvent: "SubagentStop", Matcher: "^.+$", Timeout: 60 * time.Second},
	{Client: "codex", Event: EventPreTool, NativeEvent: "PreToolUse", Matcher: "^Bash$", Timeout: 15 * time.Second},
	{Client: "codex", Event: EventPostTool, NativeEvent: "PostToolUse", Timeout: 60 * time.Second},
	{Client: "codex", Event: EventStop, NativeEvent: "Stop", Timeout: 60 * time.Second},
	{Client: "codex", Event: EventPreDispatch, NativeEvent: "PreToolUse", Matcher: "^spawn_agent$", Timeout: 15 * time.Second},
	{Client: "codex", Event: EventPostReturn, NativeEvent: "SubagentStop", Timeout: 60 * time.Second},
	{Client: "gemini", Event: EventPreTool, NativeEvent: "BeforeTool", Matcher: "^run_shell_command$", Timeout: 15 * time.Second},
	{Client: "gemini", Event: EventPreEdit, NativeEvent: "BeforeTool", Matcher: "^(replace|write_file)$", Timeout: 15 * time.Second},
	{Client: "gemini", Event: EventPostTool, NativeEvent: "AfterTool", Timeout: 60 * time.Second},
	{Client: "gemini", Event: EventStop, NativeEvent: "AfterAgent", Timeout: 60 * time.Second},
	{Client: "gemini", Event: EventPreDispatch, NativeEvent: "BeforeTool", Matcher: "^invoke_agent$", Timeout: 15 * time.Second},
	{Client: "lefthook", Event: EventPreTool, NativeEvent: "agent-pre-tool"},
	{Client: "lefthook", Event: EventEnvironment, NativeEvent: "pre-rebase"},
	// agy: NativeEvent and Timeout are docs-confirmed (Hook Spec Fields; "Execution
	// timeout in seconds. Defaults to 30"). Matcher "*" is the docs' own "matches all
	// tools" spelling: agy classifies by payload, not by registration (3.5). Stop has no
	// matcher at all (its hooks.json group is a flat handler list, not a matcher group).
	{Client: "agy", Event: EventPreTool, NativeEvent: "PreToolUse", Matcher: "*", Timeout: 30 * time.Second},
	{Client: "agy", Event: EventPreDispatch, NativeEvent: "PreToolUse", Matcher: "invoke_subagent", Timeout: 30 * time.Second},
	{Client: "agy", Event: EventStop, NativeEvent: "Stop", Timeout: 30 * time.Second},
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
