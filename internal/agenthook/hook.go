package agenthook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// usageExit is the exit code of a call no dialect can encode: the blocking code of the
// native clients, so a malformed pair fails closed. The one exception is engine skew
// (unsupportedResponse): a well-formed pair no row of this engine carries is a stated skip,
// which also means an older engine does not enforce a gate added after it.
const usageExit = 2

// Invocation is everything one hook call depends on. Nothing is read from process state.
// Settings is the operator's hooks section (S1); its zero value is the built-in governed
// scope with the built-in interpreter search order (settings.go).
type Invocation struct {
	Client   string
	Event    string
	Stdin    io.Reader
	Getenv   func(string) string
	WorkDir  string
	Policy   *Policy
	Settings config.HookSettings
	// CorrelationDir overrides the private repository cache in tests. Production callers
	// leave it empty so one deterministic path under Git's shared directory bridges hook
	// processes and isolated worktrees without entering the tracked working tree.
	CorrelationDir string
}

// Run serves one hook call: parse, read, decode, resolve, judge, encode. It never
// returns an error; every failure is a verdict in the client's dialect.
func Run(ctx context.Context, in Invocation) Response {
	row, err := ParseArguments(in.Client, in.Event)
	if err != nil {
		return unsupportedResponse(in, err)
	}
	dialect, known := DialectFor(row.Client)
	if !known {
		return usageResponse(fmt.Errorf("%w: no dialect for %s", ErrUnsupported, row.Client))
	}
	ctx, cancel := context.WithTimeout(ctx, budgetFor(row.Event))
	defer cancel()
	if dir := recordDirOf(in.Getenv); dir != "" {
		return recordAndAllow(ctx, dir, row, dialect, in)
	}
	canonical, verdict := evaluate(ctx, dialect, row, in)
	return dialect.Encode(canonical, verdict)
}

// recordDirOf reads RecordDirEnv without panicking on a nil Getenv (Invocation.Getenv
// is not required to be set; missing-collaborator tests deny closed through evaluate,
// which record mode must not shortcut).
func recordDirOf(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	return getenv(RecordDirEnv)
}

// unsupportedResponse answers a pair without a row. A known exit-code client naming a
// well-formed event that no row of this engine carries for that client gets a stated skip:
// the tracked registrations are pinned to this table in both directions by
// TestRegistrationTableMatchesTheTrackedClientFiles and TestTrackedRegistrationsNameOnlyEngineRows,
// so such a pair means the registration is newer than the running praetorctl (a new event, or
// an existing event gaining a row for this client), and failing closed there would block the
// operator's client (every subagent launch, or a subagent that cannot stop) until a
// reinstall. The cost is that an older engine skips, and so does not enforce, a gate added
// after it. Malformed arguments, an unknown client and agy (its encoder has no response shape
// for an event it does not know) keep the usage and the blocking exit code.
func unsupportedResponse(in Invocation, err error) Response {
	dialect, known := DialectFor(in.Client)
	if !known || dialect.encode != nil || !argumentShape.MatchString(in.Event) {
		return usageResponse(err)
	}
	reason := "this praetorctl serves no " + in.Client + " " + in.Event + " row: the registration is newer than the " +
		"running engine; rebuild bin/praetorctl (make hook-cli) or reinstall it from the checkout (make dev-install)"
	return dialect.Encode(Canonical{Event: Event(in.Event)}, Verdict{Outcome: Skip, Reason: reason})
}

// usageResponse lists every pair this engine serves, each once: two native events of one
// client can reach the same pair (Claude's PostToolUseFailure and PermissionDenied both reach
// dispatch-abort), and the tracked launcher reads this list to decide whether to call the
// engine at all.
func usageResponse(err error) Response {
	lines := []string{"praetor hook: " + boundReason(err.Error()), "usage: praetorctl hook <client> <event>"}
	listed := make(map[string]bool, len(registrationTable))
	for _, row := range registrationTable {
		if command := row.Command(); !listed[command] {
			listed[command] = true
			lines = append(lines, "  "+command)
		}
	}
	return Response{Stderr: []byte(strings.Join(lines, "\n") + "\n"), ExitCode: usageExit}
}

// readBounded reads at most MaxInputBytes+1 bytes and honours the context, so a client
// that never closes stdin cannot hold the hook past its budget. After a timeout the
// reader stays blocked on the open stream until the process exits, which is right after
// the verdict; closing a stream the caller owns would be the worse trade.
func readBounded(ctx context.Context, stdin io.Reader) ([]byte, error) {
	if stdin == nil {
		return nil, errors.New("hook input is missing")
	}
	type outcome struct {
		data []byte
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, MaxInputBytes+1))
		done <- outcome{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("hook input was not delivered in time: %w", ctx.Err())
	case result := <-done:
		if result.err != nil {
			return nil, fmt.Errorf("read hook input: %w", result.err)
		}
		return result.data, nil
	}
}
