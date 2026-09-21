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
// native clients, so a broken registration fails closed.
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
		return usageResponse(err)
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

func usageResponse(err error) Response {
	lines := []string{"praetor hook: " + boundReason(err.Error()), "usage: praetorctl hook <client> <event>"}
	for _, row := range registrationTable {
		lines = append(lines, "  "+row.Command())
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
