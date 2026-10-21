package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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
	lane := flags.String("lane", "repair", "sandbox lane: repair, agy, codex, local")
	var ports loopbackPorts
	flags.Var(&ports, "allow-loopback-port", "allowed host-loopback TCP port (repeatable, local lane only)")
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
	spec, err := sandbox.LaneProfile(*lane, ports)
	if err != nil {
		return sandbox.Spec{}, fmt.Errorf("agent sandbox lane: %w", err)
	}
	spec.Workspace, spec.ReadOnly, spec.Command = absolute, *readOnly, slices.Clone(args[separator+1:])
	return spec, nil
}

func runAgentSandbox(args []string) error {
	render := len(args) > 0 && args[0] == "render"
	if render {
		args = args[1:]
	}
	spec, err := parseAgentSandbox(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(agentTimeout)
	defer cancel()
	if render {
		invocation, err := (sandbox.Runner{}).Command(ctx, spec)
		if err != nil {
			return err
		}
		return renderSandboxInvocation(os.Stdout, invocation)
	}
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

// loopbackPorts retains flag order; LaneProfile validates the shared port policy.
type loopbackPorts []int

func (p *loopbackPorts) String() string { return fmt.Sprint([]int(*p)) }

func (p *loopbackPorts) Set(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("sandbox loopback port: %w", err)
	}
	if len(*p) >= 8 {
		return errors.New("sandbox allows at most 8 loopback TCP ports")
	}
	*p = append(*p, port)
	return nil
}

func renderSandboxInvocation(w io.Writer, invocation sandbox.Invocation) error {
	argv := append([]string{invocation.Path}, invocation.Args...)
	if _, err := io.WriteString(w, strings.Join(argv, "\x00")+"\x00"); err != nil {
		return fmt.Errorf("agent sandbox render: %w", err)
	}
	return nil
}
