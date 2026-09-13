package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// RunCommandBytes executes fixed argv with a deadline and a 1..16 MiB cap per stream.
// It respects WithCommandEnvironment, cancels on overflow, and preserves whitespace.
func RunCommandBytes(ctx context.Context, dir, name string, maxBytes int, args ...string) (result CommandBytes, resultErr error) {
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
	cleanup := commandBytesCleanup(cmd)
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	out := commandBuffer{limit: maxBytes, cancel: cancel}
	diagnostic := commandBuffer{limit: maxBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	err := cmd.Run()
	if out.overflow || diagnostic.overflow {
		err = errors.Join(err, fmt.Errorf("command output exceeds %d bytes per stream", maxBytes))
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return CommandBytes{Stdout: out.buffer.Bytes(), Stderr: diagnostic.buffer.Bytes()}, err
}
