package agenthook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Evaluation budgets, inside the registration timeouts of the table.
const (
	preToolBudget     = 10 * time.Second
	environmentBudget = 5 * time.Second
)

// usageExit is the exit code of a call no dialect can encode: the blocking code of the
// native clients, so a broken registration fails closed.
const usageExit = 2

// Invocation is everything one hook call depends on. Nothing is read from process state.
type Invocation struct {
	Client  string
	Event   string
	Stdin   io.Reader
	Getenv  func(string) string
	WorkDir string
	Policy  *Policy
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
	budget := preToolBudget
	if row.Event == EventEnvironment {
		budget = environmentBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return dialect.Encode(row.Event, evaluate(ctx, dialect, row.Event, in))
}

func usageResponse(err error) Response {
	lines := []string{"praetor hook: " + boundReason(err.Error()), "usage: praetorctl hook <client> <event>"}
	for _, row := range registrationTable {
		lines = append(lines, "  "+row.Command())
	}
	return Response{Stderr: []byte(strings.Join(lines, "\n") + "\n"), ExitCode: usageExit}
}

func evaluate(ctx context.Context, dialect Dialect, event Event, in Invocation) Verdict {
	canonical := Canonical{Event: event}
	if event != EventEnvironment {
		payload, err := readBounded(ctx, in.Stdin)
		if err == nil {
			canonical, err = dialect.Decode(event, payload)
		}
		if err != nil {
			return Verdict{Deny, "[BLOCKED BY HISS-16] Invalid hook input: " + err.Error()}
		}
	}
	root, err := ResolveRoot(ctx, canonical.Workspaces, in.WorkDir)
	switch {
	case err != nil:
		return Verdict{Deny, "[BLOCKED BY HISS-16] " + err.Error()}
	case root == "":
		return Verdict{Skip, "no repository"}
	case !Governed(root):
		return Verdict{Skip, "workspace not governed"}
	}
	if verdict := Environment(in.Getenv); verdict.Outcome != Allow {
		return verdict
	}
	if canonical.Command == "" {
		return Verdict{Outcome: Allow}
	}
	return in.Policy.Command(canonical.Command)
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
