// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/enginepin"
)

// pinnedEngineEnv marks a hook call one praetorctl handed to the engine the repository pins,
// so that engine judges it instead of handing it on again.
const pinnedEngineEnv = "PRAETOR_HOOK_PINNED_ENGINE"

// pinnedEngineProbeTimeout bounds the question whether the pinned engine serves a row.
const pinnedEngineProbeTimeout = 5 * time.Second

// pinnedEngine is the EngineHandoff of `praetorctl hook` (#1173). Agent hooks resolve the
// engine as the Git hooks' launcher does (enginepin, held equal to engine.sh): a pinned and
// installed engine judges the call; a pin whose engine is not installed is judged by this
// engine and the substitution is printed with the command that installs the pin, because a
// guard is never skipped and nothing installs on a tool call; a pin that cannot be used
// refuses the call, as the launcher refuses it; without a pin this engine judges and says so.
type pinnedEngine struct {
	getenv     func(string) string
	executable func() (string, error)
	resolve    func(string) (enginepin.Resolution, error)
}

func defaultPinnedEngine() pinnedEngine {
	return pinnedEngine{getenv: os.Getenv, executable: os.Executable, resolve: enginepin.Resolve}
}

func (p pinnedEngine) handoff(ctx context.Context, root string, row agenthook.Registration, payload []byte) agenthook.HandoffResult {
	if p.getenv(pinnedEngineEnv) != "" {
		return agenthook.HandoffResult{}
	}
	self, err := p.executable()
	if err != nil {
		self = "praetorctl"
	}
	resolution, err := p.resolve(root)
	switch {
	case err != nil:
		return agenthook.HandoffResult{Deny: fmt.Sprintf("the engine pin of %s cannot be read: %v", root, err)}
	case resolution.Fault != "":
		return agenthook.HandoffResult{Deny: resolution.Fault}
	case resolution.Pin == "":
		return agenthook.HandoffResult{Note: fmt.Sprintf("praetor hook: %s pins no engine (PRAETOR_REF); judged by %s", root, self)}
	}
	directory, err := enginepin.CacheDirectory(resolution.Pin, p.getenv)
	engine := ""
	if err == nil {
		engine = enginepin.CachedEngine(directory)
	}
	if engine == "" {
		return agenthook.HandoffResult{Note: fmt.Sprintf("praetor hook: the engine PRAETOR_REF=%s (declared in %s) is not installed in %s; judged by %s instead; install it with: %s",
			resolution.Pin, resolution.Source, directory, self, enginepin.InstallCommand(resolution.Pin, directory))}
	}
	if sameExecutable(engine, self) {
		return agenthook.HandoffResult{}
	}
	pair := []string{row.Client, string(row.Event)}
	if !engineServes(ctx, engine, pair) {
		return agenthook.HandoffResult{Skip: fmt.Sprintf("the pinned engine %s (PRAETOR_REF=%s) serves no %s row; update the pin to judge it",
			engine, resolution.Pin, strings.Join(pair, " "))}
	}
	return runPinnedEngine(ctx, engine, resolution.Pin, pair, payload)
}

// engineServes reports whether engine lists `praetorctl hook <client> <event>` among the pairs
// it serves, as the skew guard asks (praetor_hook.py serves).
func engineServes(ctx context.Context, engine string, pair []string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, pinnedEngineProbeTimeout)
	defer cancel()
	// #nosec G204 -- engine is the cached binary of the repository's own pin.
	cmd := exec.CommandContext(probeCtx, engine, "hook")
	var listing bytes.Buffer
	cmd.Stdout, cmd.Stderr = &listing, &listing
	_ = cmd.Run() //nolint:errcheck // the listing is read whatever the exit code; usage exits 2
	wanted := "praetorctl hook " + strings.Join(pair, " ")
	for _, line := range strings.Split(listing.String(), "\n") {
		if strings.TrimSpace(line) == wanted {
			return true
		}
	}
	return false
}

// runPinnedEngine hands the call to engine with the payload on its standard input and returns
// its answer unchanged. An engine that cannot start refuses the call: the guard fails closed.
func runPinnedEngine(ctx context.Context, engine, pin string, pair []string, payload []byte) agenthook.HandoffResult {
	// #nosec G204 -- engine is the cached binary of the repository's own pin.
	cmd := exec.CommandContext(ctx, engine, append([]string{"hook"}, pair...)...)
	cmd.Env = append(os.Environ(), pinnedEngineEnv+"="+pin)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	response := agenthook.Response{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		response.ExitCode = exit.ExitCode()
	default:
		return agenthook.HandoffResult{Deny: fmt.Sprintf("the pinned engine %s (PRAETOR_REF=%s) could not run: %v", engine, pin, err)}
	}
	return agenthook.HandoffResult{Handled: true, Response: response}
}

func sameExecutable(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && os.SameFile(a, b)
}
