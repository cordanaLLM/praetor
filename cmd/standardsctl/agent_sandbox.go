package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/sandbox"
)

func parseAgentSandbox(args []string) (sandbox.Spec, error) {
	separator := slices.Index(args, "--")
	if separator < 0 {
		if slices.ContainsFunc(args, isHelpToken) {
			printAgentUsage()
			return sandbox.Spec{}, flag.ErrHelp
		}
		return sandbox.Spec{}, errors.New("agent sandbox requires -- COMMAND [ARGS...]")
	}
	flags := flag.NewFlagSet("agent sandbox", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspace := flags.String("workspace", ".", "host directory mounted at /workspace")
	readOnly := flags.Bool("read-only", false, "mount workspace read-only")
	if _, err := parseInterspersed(flags, args[:separator]); err != nil {
		return sandbox.Spec{}, fmt.Errorf("agent sandbox flags: %w", err)
	}
	if flags.NArg() != 0 || separator == len(args)-1 || args[separator+1] == "" {
		return sandbox.Spec{}, errors.New("agent sandbox requires flags followed by -- COMMAND [ARGS...]")
	}
	absolute, err := filepath.Abs(*workspace)
	if err != nil {
		return sandbox.Spec{}, fmt.Errorf("agent sandbox workspace: %w", err)
	}
	return sandbox.Spec{Workspace: absolute, ReadOnly: *readOnly, Command: slices.Clone(args[separator+1:])}, nil
}

func runAgentSandbox(args []string) error {
	spec, err := parseAgentSandbox(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(agentTimeout)
	defer cancel()
	err = (sandbox.Runner{}).Run(ctx, spec, os.Stdin, os.Stdout, os.Stderr)
	return agentSandboxResult(err)
}

func agentSandboxResult(err error) error {
	// A failed manager probe may itself contain an ExitError. Preserve its
	// unavailable diagnostic instead of treating it as the agent's exit status.
	if errors.Is(err, sandbox.ErrUnavailable) {
		return err
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exitStatusError{code: exit.ExitCode()}
	}
	return err
}
