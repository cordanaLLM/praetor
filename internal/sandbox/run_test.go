package sandbox

import (
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
	}, CheckUserManager: func(context.Context) error { return nil }}
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
