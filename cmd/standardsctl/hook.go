package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/util"
)

// exitStatusError carries an exit code a command has already explained on stderr. The
// hook dialects need it: a native client reads exit 2 as a deny and exit 1 as a fault.
type exitStatusError struct{ code int }

func (e exitStatusError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// commandExitCode reports a command failure and returns the process exit code. An
// exitStatusError is silent because its command wrote the diagnostic itself; a zero
// status inside an error is a plain failure, never a silent success. A command a signal
// cancelled (util.SignalError) exits with the shell's 128+signal status, so a caller can
// tell an interrupted gate run from a rejected one.
func commandExitCode(stderr io.Writer, err error) int {
	var status exitStatusError
	if errors.As(err, &status) && status.code != 0 {
		return status.code
	}
	code := 1
	var interrupted *util.SignalError
	if errors.As(err, &interrupted) {
		code = interrupted.ExitCode()
	}
	if _, writeErr := fmt.Fprintf(stderr, "Error: %v\n", err); writeErr != nil {
		return code // stderr is gone; the exit code is the only report left
	}
	return code
}

// hookTimeout bounds hook dispatch including settings resolution (HISS-02, BUG-060).
const hookTimeout = 2 * time.Minute

// runHook is `praetorctl hook <client> <event>`: the one string a client registration
// contains. It takes no flags, so the registration needs no shell features; the operator's
// hooks section (command policy deny patterns, scope, interpreter candidates) is instead
// resolved the way S1 designed it, through PRAETOR_FLEET_CONFIG / PRAETOR_WORKSTATION_CONFIG
// and the install manifest (defaultOperatorSettingsFlags): an explicit document named by the
// environment first, the manifest's recorded documents after, the built-in defaults when
// neither names one. hook takes no flags (3.1 of the rollout spec), so only the environment
// and the manifest apply here; audit and gate deliberately never read operator settings
// (install_manifest.go).
func runHook(args []string) error {
	client, event := "", ""
	if len(args) == 2 {
		client, event = args[0], args[1]
	}
	ctx, cancel := commandContext(hookTimeout)
	defer cancel()
	settings, err := defaultOperatorSettingsFlags().load(ctx)
	if err != nil {
		return err
	}
	policy, err := agenthook.BuildPolicy(settings.Hooks)
	if err != nil {
		return err
	}
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}
	response := agenthook.Run(ctx, agenthook.Invocation{
		Client: client, Event: event, Stdin: os.Stdin, Getenv: os.Getenv, WorkDir: workDir,
		Policy: policy, Settings: settings.Hooks,
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
