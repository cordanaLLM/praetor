package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// CommandBytes retains exact bounded stdout and stderr, including partial failure output.
type CommandBytes struct{ Stdout, Stderr []byte }

type commandBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	overflow bool
}

func (b *commandBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) <= remaining {
		return b.buffer.Write(p)
	}
	n, err := b.buffer.Write(p[:remaining])
	b.overflow = true
	b.cancel()
	return n, errors.Join(err, fmt.Errorf("command output exceeds %d bytes", b.limit))
}

// commandStdinKey scopes child standard input to one operation tree.
type commandStdinKey struct{}

// MaxCommandStdinBytes bounds the input WithCommandStdin accepts: the cap of an output stream.
const MaxCommandStdinBytes = 16 << 20

// WithCommandStdin makes RunCommandBytes feed exactly input to the child's standard input.
// It copies the input. Without it the child reads an empty stream, as before. RunCommandStream
// ignores it: its caller already supplies an explicit stdin reader.
func WithCommandStdin(ctx context.Context, input []byte) (context.Context, error) {
	if ctx == nil {
		return nil, errors.New("command stdin requires a context")
	}
	if len(input) > MaxCommandStdinBytes {
		return nil, fmt.Errorf("command stdin exceeds %d bytes", MaxCommandStdinBytes)
	}
	return context.WithValue(ctx, commandStdinKey{}, bytes.Clone(input)), nil
}

// commandStreams attaches caller streams to a bounded command. A nil stdout selects the
// bounded buffer returned in CommandBytes.Stdout; a nil stdin falls back to WithCommandStdin's
// context-supplied bytes, then to the null device.
type commandStreams struct {
	stdin  io.Reader
	stdout io.Writer
}

// RunCommandBytes executes fixed argv with a deadline and a 1..16 MiB cap per stream.
// It respects WithCommandEnvironment and WithCommandStdin, cancels on overflow, and
// preserves whitespace.
func RunCommandBytes(ctx context.Context, dir, name string, maxBytes int, args ...string) (CommandBytes, error) {
	return runBoundedCommand(ctx, dir, name, maxBytes, commandStreams{}, args)
}

// RunCommandStream executes fixed argv with stdin and stdout attached to caller streams, for
// transfers too large to buffer. Stderr is retained up to maxStderr (1..16 MiB) bytes for
// diagnostics. It shares RunCommandBytes' deadline, environment and process-group cleanup, and
// a failure to copy stdin is reported even when the command itself exits zero.
func RunCommandStream(ctx context.Context, dir, name string, stdin io.Reader, stdout io.Writer, maxStderr int, args ...string) ([]byte, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	result, err := runBoundedCommand(ctx, dir, name, maxStderr, commandStreams{stdin: stdin, stdout: stdout}, args)
	return result.Stderr, err
}

func runBoundedCommand(ctx context.Context, dir, name string, maxBytes int, streams commandStreams, args []string) (result CommandBytes, resultErr error) {
	if ctx == nil {
		return CommandBytes{}, errors.New("command bytes requires a context")
	}
	if maxBytes < 1 || maxBytes > 16<<20 {
		return CommandBytes{}, errors.New("command byte cap must be 1..16777216")
	}
	ctx, deadlineCancel := ensureDeadline(ctx, DefaultCommandTimeout)
	defer deadlineCancel()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// #nosec G204 -- audited execution boundary; internal callers supply validated executable/argv without a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = CommandWaitDelay
	if environment, ok := ctx.Value(commandEnvironmentKey{}).([]string); ok {
		cmd.Env = append([]string{}, environment...)
	}
	cmd.Stdin = streams.stdin
	if cmd.Stdin == nil {
		if input, ok := ctx.Value(commandStdinKey{}).([]byte); ok {
			cmd.Stdin = bytes.NewReader(input)
		}
	}
	cleanup := commandBytesCleanup(cmd)
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	out := commandBuffer{limit: maxBytes, cancel: cancel}
	diagnostic := commandBuffer{limit: maxBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if streams.stdout != nil {
		cmd.Stdout = streams.stdout
	}
	err := cmd.Run()
	if out.overflow || diagnostic.overflow {
		err = errors.Join(err, fmt.Errorf("command output exceeds %d bytes per stream", maxBytes))
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return CommandBytes{Stdout: out.buffer.Bytes(), Stderr: diagnostic.buffer.Bytes()}, err
}
