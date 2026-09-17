package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/agenthook"
)

// exitStatusError carries an exit code a command has already explained on stderr. The
// hook dialects need it: a native client reads exit 2 as a deny and exit 1 as a fault.
type exitStatusError struct{ code int }

func (e exitStatusError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// commandExitCode reports a command failure and returns the process exit code. An
// exitStatusError is silent because its command wrote the diagnostic itself; a zero
// status inside an error is a plain failure, never a silent success.
func commandExitCode(stderr io.Writer, err error) int {
	var status exitStatusError
	if errors.As(err, &status) && status.code != 0 {
		return status.code
	}
	fmt.Fprintf(stderr, "Error: %v\n", err)
	return 1
}

// runHook is `praetorctl hook <client> <event>`: the one string a client registration
// contains. It takes no flags, so the registration needs no shell features.
func runHook(args []string) error {
	client, event := "", ""
	if len(args) == 2 {
		client, event = args[0], args[1]
	}
	// The settings wiring for `hooks.command_policy.deny` is a later change; the built-in
	// policy cannot fail to compile, and an error here still fails closed below.
	policy, err := agenthook.NewPolicy(nil)
	if err != nil {
		return err
	}
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}
	response := agenthook.Run(context.Background(), agenthook.Invocation{
		Client: client, Event: event, Stdin: os.Stdin, Getenv: os.Getenv, WorkDir: workDir, Policy: policy,
	})
	return writeHookResponse(os.Stdout, os.Stderr, response)
}

// writeHookResponse emits the dialect's bytes. A deny keeps its exit code even when a
// stream is closed: the code is the verdict, the text only explains it. An allow whose
// bytes cannot be delivered is a failure, so silence is never read as a delivered allow.
func writeHookResponse(stdout, stderr io.Writer, response agenthook.Response) error {
	_, diagnosticErr := stderr.Write(response.Stderr)
	_, responseErr := stdout.Write(response.Stdout)
	if response.ExitCode != 0 {
		return exitStatusError{code: response.ExitCode}
	}
	if err := errors.Join(diagnosticErr, responseErr); err != nil {
		return fmt.Errorf("write hook response: %w", err)
	}
	return nil
}
