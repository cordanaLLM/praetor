package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/sandbox"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestAgentSandboxParse(t *testing.T) {
	dir := t.TempDir()
	spec, err := parseAgentSandbox([]string{"--workspace", dir, "--read-only", "--", "/usr/bin/printf", "--help", "help", "a b"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Workspace != dir || !spec.ReadOnly || !slices.Equal(spec.Command, []string{"/usr/bin/printf", "--help", "help", "a b"}) {
		t.Fatalf("parsed = %+v", spec)
	}
	spec, err = parseAgentSandbox([]string{"--", "true"})
	if err != nil || !filepath.IsAbs(spec.Workspace) || spec.ReadOnly {
		t.Fatalf("defaults = %+v, %v", spec, err)
	}
}

func TestAgentSandboxInvalidAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"true"}, {"--"}, {"--", ""}, {"stray", "--", "true"}, {"--unknown", "--", "true"}} {
		if _, err := parseAgentSandbox(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := runAgent([]string{"sandbox", "--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help = %v", err)
	}
}

func TestAgentSandboxExitProcess(t *testing.T) {
	if slices.Contains(os.Args, "sandbox-exit-helper") {
		os.Exit(37)
	}
}

func TestAgentSandboxExitCode(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = util.RunCommandBytes(t.Context(), "", executable, 4096, "-test.run=^TestAgentSandboxExitProcess$", "--", "sandbox-exit-helper")
	result := agentSandboxResult(err)
	var diagnostic bytes.Buffer
	if code := commandExitCode(&diagnostic, result); code != 37 {
		t.Fatalf("exit = %d, error = %v, diagnostic = %s", code, result, diagnostic.String())
	}
	if agentSandboxResult(nil) != nil {
		t.Fatal("success returned an error")
	}
	unavailable := &sandbox.UnavailableError{Facility: "systemd user manager", Cause: err}
	if !errors.Is(agentSandboxResult(unavailable), sandbox.ErrUnavailable) || commandExitCode(&diagnostic, unavailable) == 0 {
		t.Fatal("unavailability lost")
	}
	diagnostic.Reset()
	if code := commandExitCode(&diagnostic, agentSandboxResult(unavailable)); code != 1 || !bytes.Contains(diagnostic.Bytes(), []byte("systemd user manager")) {
		t.Fatalf("probe exit hid unavailable diagnostic: code=%d diagnostic=%s", code, diagnostic.String())
	}
}

func TestAgentSandboxUnavailableNeverExecutes(t *testing.T) {
	// An empty PATH makes the production lookup refuse before any requested command.
	t.Setenv("PATH", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = runAgent([]string{"sandbox", "--workspace", t.TempDir(), "--", executable, "--help"})
	if !errors.Is(err, sandbox.ErrUnavailable) {
		t.Fatalf("unavailable sandbox or command --help misrouted: %v", err)
	}
}
