package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

func TestAgentSandboxLaneFlags(t *testing.T) {
	assertLocalLaneFlags(t)
	for _, lane := range []string{"repair", "agy", "codex"} {
		assertNonLocalLaneFlags(t, lane)
	}
}

func assertLocalLaneFlags(t *testing.T) {
	t.Helper()
	spec, err := parseAgentSandbox([]string{"--lane", "local", "--allow-loopback-port", "1", "--allow-loopback-port=65535", "--", "/usr/bin/true"})
	if os.Getuid() < 0 {
		if !errors.Is(err, sandbox.ErrUnavailable) || !strings.Contains(err.Error(), runtime.GOOS) {
			t.Fatalf("local profile without POSIX user ids = %+v, %v", spec, err)
		}
		return
	}
	if err != nil || spec.Network != sandbox.NetworkLoopback || !slices.Equal(spec.AllowLoopbackPorts, []int{1, 65535}) {
		t.Fatalf("local profile = %+v, %v", spec, err)
	}
}

func assertNonLocalLaneFlags(t *testing.T, lane string) {
	t.Helper()
	_, err := parseAgentSandbox([]string{"--lane", lane, "--", "true"})
	if os.Getuid() < 0 && lane != "repair" {
		if !errors.Is(err, sandbox.ErrUnavailable) || !strings.Contains(err.Error(), runtime.GOOS) {
			t.Fatalf("lane %s without POSIX user ids = %v", lane, err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if _, err := parseAgentSandbox([]string{"--lane", lane, "--allow-loopback-port", "4011", "--", "true"}); err == nil {
		t.Fatalf("accepted local port for %s", lane)
	}
}

func TestAgentSandboxInvalidLaneFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--lane", "unknown"}, {"--lane", ""},
		{"--lane", "local", "--allow-loopback-port", "0"},
		{"--lane", "local", "--allow-loopback-port", "65536"},
		{"--lane", "local", "--allow-loopback-port", "abc"},
	} {
		args = append(args, "--", "true")
		if _, err := parseAgentSandbox(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	args := []string{"--lane", "local"}
	for i := 0; i < 9; i++ {
		args = append(args, "--allow-loopback-port", "4011")
	}
	if _, err := parseAgentSandbox(append(args, "--", "true")); err == nil {
		t.Fatal("accepted ninth port")
	}
}

func TestAgentSandboxRenderArgv(t *testing.T) {
	invocation, err := sandbox.Build(sandbox.Spec{Workspace: "/source", Command: []string{"/usr/bin/printf", "a b", "", "--help"}, HasLib64: func() bool { return true }}, sandbox.Binaries{SystemdRun: "/usr/bin/systemd-run", Prlimit: "/usr/bin/prlimit", Bwrap: "/usr/bin/bwrap"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := renderSandboxInvocation(&output, invocation); err != nil {
		t.Fatal(err)
	}
	want := append([]string{invocation.Path}, invocation.Args...)
	got := bytes.Split(output.Bytes(), []byte{0})
	if len(got) != len(want)+1 || len(got[len(got)-1]) != 0 {
		t.Fatalf("render did not NUL terminate each argument: %q", output.Bytes())
	}
	for i := 0; i < len(want); i++ {
		if string(got[i]) != want[i] {
			t.Fatalf("argv[%d]=%q want=%q", i, got[i], want[i])
		}
	}
	if err := renderSandboxInvocation(failedSandboxWriter{}, invocation); err == nil {
		t.Fatal("render ignored output failure")
	}
}

type failedSandboxWriter struct{}

func (failedSandboxWriter) Write([]byte) (int, error) { return 0, errors.New("planted output failure") }

func TestAgentSandboxRenderRefusesUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	output, err := captureStdout(t, func() error {
		return runAgent([]string{"sandbox", "render", "--lane", "local", "--allow-loopback-port", "4011", "--", "/usr/bin/true"})
	})
	if !errors.Is(err, sandbox.ErrUnavailable) || output != "" {
		t.Fatalf("render emitted unsafe argv: %q, %v", output, err)
	}
	if err := runAgent([]string{"sandbox", "render", "--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("render help = %v", err)
	}
}
