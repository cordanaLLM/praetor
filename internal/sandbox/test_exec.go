// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// loopbackArguments reuses NetworkNone's namespace policy, with the caller's
// filesystem and environment so go test's binary, working directory and fixtures
// remain available. Bubblewrap maps the caller's own UID/GID without capabilities
// and brings up the private namespace's sole interface, lo.
func loopbackArguments(command []string) []string {
	args := namespaceArguments(Spec{Network: NetworkNone})
	args = append(args, "--dev-bind", "/", "/", "--proc", "/proc", "--")
	return append(args, command...)
}

// LoopbackCommand prepares a test-binary invocation without the agent profile's
// filesystem, environment or resource restrictions. Namespace failures never
// return a direct invocation of the requested command.
func (r Runner) LoopbackCommand(ctx context.Context, command []string) (Invocation, error) {
	if ctx == nil || len(command) == 0 || command[0] == "" {
		return Invocation{}, errors.New("loopback namespace requires a context and command")
	}
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos != "linux" {
		return Invocation{}, &UnavailableError{Facility: "Linux network namespaces", Cause: fmt.Errorf("unsupported OS %s", goos)}
	}
	bwrap, err := r.lookup("bwrap")
	if err != nil {
		return Invocation{}, err
	}
	if err := r.probeLoopback(ctx, bwrap); err != nil {
		if errors.Is(err, ErrUnavailable) {
			return Invocation{}, err
		}
		return Invocation{}, &UnavailableError{Facility: "bwrap user/network namespaces (loopback-only)", Cause: err}
	}
	return Invocation{Path: bwrap, Args: loopbackArguments(command)}, nil
}

// probeLoopback checks whether bubblewrap user and network namespaces are available.
func (r Runner) probeLoopback(ctx context.Context, bwrap string) error {
	if r.ProbeBwrap != nil {
		return r.ProbeBwrap(ctx)
	}
	probeTarget, err := r.lookup("true")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, "", bwrap, 4096, loopbackArguments([]string{probeTarget})...)
	return util.CommandDiagnostic(err, result.Stderr)
}

// RunLoopback runs only the compiled test binary in NetworkNone. The caller owns
// the deadline; go test's build and module downloads run before this boundary.
func (r Runner) RunLoopback(ctx context.Context, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	invocation, err := r.LoopbackCommand(ctx, command)
	if err != nil {
		return err
	}
	if err := util.RunCommandPassthrough(ctx, "", invocation.Path, stdin, stdout, stderr, invocation.Args...); err != nil {
		return fmt.Errorf("go test loopback namespace: %w", err)
	}
	return nil
}
