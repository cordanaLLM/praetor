package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/sandbox"
)

func TestAgentSandboxRenderPartFlags(t *testing.T) {
	for _, part := range []string{"network", "namespaces"} {
		assertSandboxRenderPartParse(t, part)
	}
	for _, args := range [][]string{
		nil, {"--part=full"}, {"--part="}, {"--part=unknown"},
		{"--part=network", "--"}, {"--part=namespaces", "--", ""},
		{"stray", "--part=network"},
	} {
		if _, _, err := parseAgentSandboxOptions(args, true); err == nil {
			t.Fatalf("accepted invalid render: %q", args)
		}
	}
	if _, err := parseAgentSandbox([]string{"--part=network", "--", "true"}); err == nil {
		t.Fatal("execution accepted render-only flag")
	}
	_, part, err := parseAgentSandboxOptions([]string{"--", "true", "--part=network"}, true)
	if err != nil || part != "full" {
		t.Fatalf("command flag changed default full: %q, %v", part, err)
	}
}

func assertSandboxRenderPartParse(t *testing.T, part string) {
	t.Helper()
	if _, got, err := parseAgentSandboxOptions([]string{"--part=" + part}, true); err != nil || got != part {
		t.Fatalf("repair partial parse: %q, %v", got, err)
	}
	spec, got, err := parseAgentSandboxOptions([]string{"--lane", "local", "--allow-loopback-port", "4011", "--part=" + part}, true)
	if os.Getuid() < 0 {
		if !errors.Is(err, sandbox.ErrUnavailable) || !strings.Contains(err.Error(), runtime.GOOS) {
			t.Fatalf("partial parse without POSIX user ids: %+v, %q, %v", spec, got, err)
		}
		return
	}
	if err != nil || got != part || !slices.Equal(spec.AllowLoopbackPorts, []int{4011}) {
		t.Fatalf("partial parse: %+v, %q, %v", spec, got, err)
	}
}

func TestAgentSandboxRenderPartsNUL(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		if (runtime.GOOS == "windows" || os.Getuid() < 0) && lane != "repair" {
			assertSandboxRenderMissingPOSIXIDs(t, lane)
			continue
		}
		spec, err := sandbox.LaneProfile(lane, nil)
		if err != nil {
			t.Fatal(err)
		}
		spec.Workspace, spec.Command = "/source", []string{"/usr/bin/printf", "a b", "", "--part=network"}
		spec.DNS = sandbox.DNSConfig{Nameservers: []string{"192.0.2.53"}, ResolvConf: "/resolver with spaces.conf"}
		inv, err := sandbox.Build(spec, sandbox.Binaries{SystemdRun: "/usr/bin/systemd-run", Prlimit: "/usr/bin/prlimit", Bwrap: "/usr/bin/bwrap", Pasta: "/usr/bin/pasta"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range []string{"full", "network", "namespaces"} {
			assertSandboxPartNUL(t, inv, part)
		}
	}
}

func assertSandboxRenderMissingPOSIXIDs(t *testing.T, lane string) {
	t.Helper()
	for _, part := range []string{"full", "network", "namespaces"} {
		var diagnostic bytes.Buffer
		output, err := captureStdout(t, func() error {
			return runAgent([]string{"sandbox", "render", "--lane", lane, "--part", part, "--", "/usr/bin/true"})
		})
		code := commandExitCode(&diagnostic, err)
		if code == 0 || output != "" || !errors.Is(err, sandbox.ErrUnavailable) || !strings.Contains(diagnostic.String(), "POSIX user ids") || !strings.Contains(diagnostic.String(), runtime.GOOS) {
			t.Fatalf("missing POSIX user ids lane %s part %s: exit=%d stdout=%q stderr=%q err=%v", lane, part, code, output, diagnostic.String(), err)
		}
	}
}

func assertSandboxPartNUL(t *testing.T, inv sandbox.Invocation, part string) {
	t.Helper()
	args, err := inv.Arguments(part)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := renderSandboxPart(&output, inv, part); err != nil {
		t.Fatal(err)
	}
	want := ""
	if len(args) != 0 {
		want = strings.Join(args, "\x00") + "\x00"
		if err := renderSandboxPart(failedSandboxWriter{}, inv, part); err == nil {
			t.Fatal("ignored writer failure")
		}
	}
	if output.String() != want {
		t.Fatalf("part %s NUL encoding: %q want %q", part, output.String(), want)
	}
	output.Reset()
	if err := renderSandboxPart(&output, inv, "unknown"); err == nil || output.Len() != 0 {
		t.Fatalf("invalid part emitted output: %q, %v", output.String(), err)
	}
}

func TestAgentSandboxRenderPartsMissingBinaryNoOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binary resolution checks require the Linux sandbox runtime")
	}
	for _, missing := range []string{"bwrap", "pasta"} {
		for _, lane := range []string{"repair", "agy", "codex", "local"} {
			if missing == "pasta" && lane == "repair" {
				continue
			}
			t.Run(missing+"/"+lane, func(t *testing.T) {
				assertSandboxRenderMissingBinary(t, missing, lane)
			})
		}
	}
}

func assertSandboxRenderMissingBinary(t *testing.T, missing, lane string) {
	t.Helper()
	dir := t.TempDir()
	for _, binary := range []string{"bwrap", "pasta", "systemd-run", "prlimit"} {
		if binary != missing {
			if err := os.WriteFile(filepath.Join(dir, binary), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", dir)
	for _, part := range []string{"full", "network", "namespaces"} {
		var diagnostic bytes.Buffer
		output, err := captureStdout(t, func() error {
			return runAgent([]string{"sandbox", "render", "--lane", lane, "--part", part, "--", "/usr/bin/true"})
		})
		code := commandExitCode(&diagnostic, err)
		if code == 0 || output != "" || !strings.Contains(diagnostic.String(), missing) {
			t.Fatalf("missing %s part %s: exit=%d stdout=%q stderr=%q", missing, part, code, output, diagnostic.String())
		}
	}
}
