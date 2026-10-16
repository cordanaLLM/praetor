package util

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestRunCommandPassthroughStreamsAndExit(t *testing.T) {
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH", "1")
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH_EXIT", "0")
	var stdout, stderr bytes.Buffer
	err := RunCommandPassthrough(t.Context(), "", os.Args[0], bytes.NewBufferString("request\n"), &stdout, &stderr,
		"-test.run=^TestCommandPassthroughHelper$")
	if err != nil || stdout.String() != "request\nPASS\n" || stderr.String() != "child stderr\n" {
		t.Fatalf("stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
	}
}

func TestRunCommandPassthroughReturnsChildFailure(t *testing.T) {
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH", "1")
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH_EXIT", "23")
	err := RunCommandPassthrough(t.Context(), "", os.Args[0], nil, io.Discard, io.Discard,
		"-test.run=^TestCommandPassthroughHelper$")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child error = %v, want exit 23", err)
	}
}

func TestRunCommandPassthroughBoundaryNilWritersAndCancelledContext(t *testing.T) {
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH", "1")
	t.Setenv("PRAETOR_COMMAND_PASSTHROUGH_EXIT", "0")
	if err := RunCommandPassthrough(t.Context(), "", os.Args[0], nil, nil, nil,
		"-test.run=^TestCommandPassthroughHelper$"); err != nil {
		t.Fatalf("nil writers = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := RunCommandPassthrough(ctx, "", os.Args[0], nil, nil, nil, "-test.run=^TestCommandPassthroughHelper$")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %v", err)
	}
}

func TestCommandPassthroughHelper(t *testing.T) {
	if os.Getenv("PRAETOR_COMMAND_PASSTHROUGH") == "" {
		return
	}
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		os.Exit(24)
	}
	if _, err := io.WriteString(os.Stderr, "child stderr\n"); err != nil {
		os.Exit(24)
	}
	if os.Getenv("PRAETOR_COMMAND_PASSTHROUGH_EXIT") == "23" {
		os.Exit(23)
	}
}
