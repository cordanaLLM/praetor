package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/config"
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
	if _, writeErr := fmt.Fprintf(stderr, "Error: %v\n", err); writeErr != nil {
		return 1 // stderr is gone; the exit code is the only report left
	}
	return 1
}

// runHook is `praetorctl hook <client> <event>`: the one string a client registration
// contains. It takes no flags, so the registration needs no shell features; the operator's
// hooks section (command policy deny patterns, scope, interpreter candidates) is instead
// resolved the way S1 designed it, through the install manifest and PRAETOR_FLEET_CONFIG /
// PRAETOR_WORKSTATION_CONFIG (loadHookSettings).
func runHook(args []string) error {
	client, event := "", ""
	if len(args) == 2 {
		client, event = args[0], args[1]
	}
	ctx := context.Background()
	settings, err := loadHookSettings(ctx)
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

// loadHookSettings resolves the operator's hooks section the way it is meant to be read
// (config.SelectOperatorSettings, then config.LoadOperatorSettings): an explicit document
// named by PRAETOR_FLEET_CONFIG / PRAETOR_WORKSTATION_CONFIG first, the install manifest's
// recorded documents after, the built-in defaults when neither names one. hook takes no
// flags (3.1 of the rollout spec), so only the environment and the manifest apply here; audit
// and gate deliberately never call this path (install_manifest.go). A host with no per-user
// config directory (no home, a minimal container) falls back to the built-in defaults instead
// of failing every hook call over a directory the install manifest does not need to exist.
func loadHookSettings(ctx context.Context) (config.OperatorSettings, error) {
	manifestPath, err := config.DefaultInstallManifestPath()
	if err != nil {
		manifestPath = ""
	}
	selection, err := config.SelectOperatorSettings(ctx, config.SettingsRequest{
		Getenv: os.Getenv, ManifestPath: manifestPath,
	})
	if err != nil {
		return config.OperatorSettings{}, err
	}
	return config.LoadOperatorSettings(ctx, selection)
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
