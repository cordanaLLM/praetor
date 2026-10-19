package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func stubRunner(missing string) Runner {
	return Runner{GOOS: "linux", Lookup: func(name string) (string, error) {
		if name == missing {
			return "", errors.New("planted missing binary")
		}
		return "/usr/bin/" + name, nil
	}, CheckUserManager: func(context.Context) error { return nil },
		ProbeBwrap: func(context.Context) error { return nil }}
}

func assertUnavailable(t *testing.T, err error, facility string) {
	t.Helper()
	var unavailable *UnavailableError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || !strings.Contains(err.Error(), facility) {
		t.Fatalf("error = %v; want typed unavailable naming %s", err, facility)
	}
}

func TestSandboxMissingBwrapNeverStartsCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Workspace: t.TempDir(), Command: []string{executable, "-test.run=^TestSandboxMarkerProcess$", "--", "sandbox-marker", marker}}
	err = stubRunner("bwrap").Run(t.Context(), spec, nil, nil, nil)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncontained command started: marker stat = %v", err)
	}
	assertUnavailable(t, err, "bwrap")
}

func TestSandboxDeniedNamespaceReportsUnavailableAndNeverStartsCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Workspace: t.TempDir(), Command: []string{executable, "-test.run=^TestSandboxMarkerProcess$", "--", "sandbox-marker", marker}}
	runner := stubRunner("")
	runner.ProbeBwrap = func(context.Context) error {
		return errors.New("setting up uid map: Permission denied")
	}
	err = runner.Run(t.Context(), spec, nil, nil, nil)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncontained command started: marker stat = %v", err)
	}
	assertUnavailable(t, err, "bwrap")
	if !strings.Contains(err.Error(), "setting up uid map: Permission denied") {
		t.Fatalf("error = %v; want denied namespace diagnostic", err)
	}
}

// This helper also runs under the direct-execution mutant, on every supported OS.
func TestSandboxMarkerProcess(t *testing.T) {
	index := slices.Index(os.Args, "sandbox-marker")
	if index < 0 {
		return
	}
	if err := os.WriteFile(os.Args[index+1], []byte("executed"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxUnavailableRuntime(t *testing.T) {
	for _, missing := range []string{"systemd-run", "prlimit"} {
		_, err := stubRunner(missing).Command(t.Context(), repairProfile())
		assertUnavailable(t, err, missing)
	}
	for _, goos := range []string{"darwin", "windows", "plan9"} {
		runner := stubRunner("")
		runner.GOOS = goos
		_, err := runner.Command(t.Context(), repairProfile())
		assertUnavailable(t, err, goos)
	}
}

func TestSandboxMissingWorkspaceAndEmptyCommand(t *testing.T) {
	for _, spec := range []Spec{
		{Workspace: t.TempDir()},
		{Workspace: filepath.Join(t.TempDir(), "missing"), Command: []string{"true"}},
	} {
		if _, err := stubRunner("").Command(t.Context(), spec); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestSandboxUnusableUserManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("user-manager environment requires Linux; OS refusal tested via injection")
	}
	runner := stubRunner("")
	runner.CheckUserManager = func(context.Context) error { return errors.New("manager offline") }
	_, err := runner.Command(t.Context(), Spec{Workspace: t.TempDir(), Command: []string{"true"}})
	assertUnavailable(t, err, "systemd user manager")
	runner = stubRunner("systemctl")
	runner.CheckUserManager = nil
	_, err = runner.Command(t.Context(), Spec{Workspace: t.TempDir(), Command: []string{"true"}})
	assertUnavailable(t, err, "systemctl")
}

func TestSandboxNilContextRefused(t *testing.T) {
	var ctx context.Context
	if _, err := stubRunner("").Command(ctx, repairProfile()); err == nil {
		t.Fatal("nil command context accepted")
	}
	if err := stubRunner("").Run(ctx, repairProfile(), nil, nil, nil); err == nil {
		t.Fatal("nil run context accepted")
	}
}

func TestSandboxRealIntegration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real sandbox requires Linux namespaces and a systemd user manager")
	}
	runner := Runner{}
	spec := Spec{Workspace: t.TempDir(), Command: []string{"/usr/bin/true"}}
	if _, err := runner.Command(t.Context(), spec); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("real sandbox runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), spec, nil, nil, nil); err != nil {
		t.Fatalf("sandbox true: %v", err)
	}
	spec.Command = []string{"/usr/bin/touch", "/workspace/allowed"}
	if err := runner.Run(t.Context(), spec, nil, nil, nil); err != nil {
		t.Fatalf("workspace write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spec.Workspace, "allowed")); err != nil {
		t.Fatal(err)
	}
	spec.Command = []string{"/usr/bin/touch", "/usr/sandbox-forbidden"}
	if err := runner.Run(t.Context(), spec, nil, nil, nil); err == nil {
		t.Fatal("write outside workspace succeeded")
	}
	spec.ReadOnly = true
	spec.Command = []string{"/usr/bin/touch", "/workspace/forbidden"}
	if err := runner.Run(t.Context(), spec, nil, nil, nil); err == nil {
		t.Fatal("write to read-only workspace succeeded")
	}
}

func TestSandboxProbeSuccessReportsAvailable(t *testing.T) {
	runner := stubRunner("")
	runner.ProbeBwrap = func(context.Context) error { return nil }
	invocation, err := runner.Command(t.Context(), Spec{Workspace: t.TempDir(), Command: []string{"/usr/bin/true"}})
	if err != nil {
		t.Fatalf("Command failed: %v; want runtime available", err)
	}
	if invocation.Path != "/usr/bin/systemd-run" {
		t.Fatalf("invocation path = %q, want /usr/bin/systemd-run", invocation.Path)
	}
}

func TestSandboxRealBwrapProbeWhenInstalled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("probe requires Linux")
	}
	runner := Runner{
		CheckUserManager: func(context.Context) error { return nil },
	}
	spec := Spec{Workspace: t.TempDir(), Command: []string{"/usr/bin/true"}}
	_, err := runner.Command(t.Context(), spec)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Logf("probe reported unavailable on this host: %v", err)
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSandboxExecutionFailureCarriesStderrTail(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-systemd-run")
	oversize := strings.Repeat("A", maxExecutionStderrBytes*2)
	const tailMarker = "unique failure diagnostic tail"
	content := "#!/bin/sh\n" +
		"cat << 'EOF' >&2\n" +
		oversize + "\n" +
		tailMarker + "\n" +
		"EOF\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := stubRunner("")
	runner.Lookup = func(name string) (string, error) {
		if name == "systemd-run" {
			return script, nil
		}
		return "/usr/bin/" + name, nil
	}
	var captured bytes.Buffer
	spec := Spec{Workspace: t.TempDir(), Command: []string{"/usr/bin/true"}}
	err := runner.Run(t.Context(), spec, nil, nil, &captured)
	if err == nil {
		t.Fatal("Run succeeded; want execution failure")
	}
	if !strings.Contains(err.Error(), tailMarker) {
		t.Fatalf("error = %q; want tail marker %q", err.Error(), tailMarker)
	}
	if strings.Contains(err.Error(), oversize) {
		t.Fatalf("error contains full oversize stderr; want bounded tail only")
	}
	if !strings.Contains(captured.String(), oversize) || !strings.Contains(captured.String(), tailMarker) {
		t.Fatalf("captured stderr missing expected output")
	}
}

func assertTailWrite(t *testing.T, w *tailWriter, input []byte, wantN int, wantStr string) {
	t.Helper()
	n, err := w.Write(input)
	if err != nil {
		t.Fatalf("Write(%q) err = %v", input, err)
	}
	if n != wantN {
		t.Fatalf("Write(%q) n = %d, want %d", input, n, wantN)
	}
	if got := w.String(); got != wantStr {
		t.Fatalf("Write(%q) String() = %q, want %q", input, got, wantStr)
	}
}

func TestTailWriter(t *testing.T) {
	w := &tailWriter{limit: 10}
	assertTailWrite(t, w, []byte("hello"), 5, "hello")
	assertTailWrite(t, w, []byte("12345"), 5, "hello12345")
	assertTailWrite(t, w, []byte("XYZ"), 3, "lo12345XYZ")
	assertTailWrite(t, w, []byte("0123456789ABCDEF"), 16, "6789ABCDEF")

	wZero := &tailWriter{limit: 0}
	assertTailWrite(t, wZero, []byte("test"), 4, "")
}
