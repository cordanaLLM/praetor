// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestSandboxLoopbackExecArguments pins argument construction for running in a loopback-only network namespace.
func TestSandboxLoopbackExecArguments(t *testing.T) {
	command := []string{"/tmp/test binary", "-test.run=Loopback", "argument with spaces"}
	inv, err := stubRunner("").LoopbackCommand(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	want := append(namespaceArguments(Spec{Network: NetworkNone}), "--dev-bind", "/", "/", "--proc", "/proc", "--")
	want = append(want, command...)
	if inv.Path != "/usr/bin/bwrap" || !slices.Equal(inv.Args, want) || slices.Contains(inv.Args, "--clearenv") {
		t.Fatalf("wrong loopback invocation: %+v", inv)
	}
}

// TestSandboxLoopbackExecUnavailable pins proper rejection when namespaces or bubblewrap are unavailable.
func TestSandboxLoopbackExecUnavailable(t *testing.T) {
	_, err := stubRunner("bwrap").LoopbackCommand(t.Context(), []string{"/test"})
	assertUnavailable(t, err, "bwrap")
	runner := stubRunner("")
	runner.ProbeBwrap = func(context.Context) error { return errors.New("planted EPERM: user namespaces disabled") }
	err = runner.RunLoopback(t.Context(), []string{"/must-not-execute"}, nil, nil, nil)
	assertUnavailable(t, err, "bwrap user/network namespaces (loopback-only)")
	if !strings.Contains(err.Error(), "planted EPERM") {
		t.Fatalf("cause lost: %v", err)
	}
	for _, goos := range []string{"darwin", "windows"} {
		runner.GOOS = goos
		_, err := runner.LoopbackCommand(t.Context(), []string{"/test"})
		assertUnavailable(t, err, goos)
	}
	for _, command := range [][]string{nil, {""}} {
		if _, err := stubRunner("").LoopbackCommand(t.Context(), command); err == nil {
			t.Fatalf("accepted empty command: %q", command)
		}
	}
}

// TestSandboxLoopbackProbeResolvesTrue pins lookup and execution of the probe binary during capability detection.
func TestSandboxLoopbackProbeResolvesTrue(t *testing.T) {
	if runtime.GOOS == "linux" {
		fakeBwrap := filepath.Join(t.TempDir(), "fake-bwrap")
		script := "#!/bin/sh\nfor last; do :; done\nif [ \"$last\" != \"/bin/true\" ]; then\n  echo \"probe target = $last, want /bin/true\" >&2\n  exit 1\nfi\n"
		if err := os.WriteFile(fakeBwrap, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		runner := Runner{GOOS: "linux", Lookup: func(name string) (string, error) {
			if name == "bwrap" {
				return fakeBwrap, nil
			}
			if name == "true" {
				return "/bin/true", nil
			}
			return "", fmt.Errorf("unexpected lookup %q", name)
		}}
		inv, err := runner.LoopbackCommand(t.Context(), []string{"/test"})
		if err != nil || inv.Path != fakeBwrap {
			t.Fatalf("LoopbackCommand() inv=%+v error=%v", inv, err)
		}
	}

	runner := stubRunner("true")
	runner.ProbeBwrap = nil
	_, err := runner.LoopbackCommand(t.Context(), []string{"/test"})
	assertUnavailable(t, err, "true")

	runner.Lookup = func(name string) (string, error) {
		if name == "true" {
			return "bin/true", nil
		}
		return "/usr/bin/" + name, nil
	}
	_, err = runner.LoopbackCommand(t.Context(), []string{"/test"})
	assertUnavailable(t, err, "true")
}

// Re-executed test helper: own HTTP client succeeds before publishing the port.
func TestSandboxLoopbackServerProcess(t *testing.T) {
	index := slices.Index(os.Args, "sandbox-loopback-server")
	if index < 0 {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, "private test server"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	if err := loopbackRequest(t.Context(), server.URL); err != nil {
		t.Fatalf("in-namespace loopback failed: %v", err)
	}
	fmt.Println(server.URL)
	var stop [1]byte
	if _, err := io.ReadFull(os.Stdin, stop[:]); err != nil {
		t.Fatal(err)
	}
	if os.Args[index+1] == "isolated" {
		assertLoopbackNamespace(t, os.Args[index+2], os.Args[index+3])
	}
}

// assertLoopbackNamespace asserts that the process preserved UID and GID and has only the loopback network interface active.
func assertLoopbackNamespace(t *testing.T, wantUID, wantGID string) {
	t.Helper()
	if gotUID, gotGID := strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()); gotUID != wantUID || gotGID != wantGID {
		t.Fatalf("namespace altered caller identity: got uid=%s gid=%s, want uid=%s gid=%s", gotUID, gotGID, wantUID, wantGID)
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 1 || interfaces[0].Name != "lo" || interfaces[0].Flags&net.FlagUp == 0 {
		t.Fatalf("want only lo, up: %+v error=%v", interfaces, err)
	}
}

// loopbackRequest issues an HTTP GET request to verify reachability and expected payload from the test server.
func loopbackRequest(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 100))
	err = errors.Join(readErr, response.Body.Close())
	if err != nil || string(body) != "private test server" {
		return fmt.Errorf("unexpected loopback response %q: %w", body, err)
	}
	return nil
}

type loopbackTestRunner func(context.Context, io.Reader, io.Writer, io.Writer) error

// loopbackServerCommand prepares a runner for the child server test process in either host or isolated mode.
func loopbackServerCommand(t *testing.T, isolated bool) loopbackTestRunner {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mode := "host"
	if isolated {
		mode = "isolated"
	}
	command := []string{
		executable,
		"-test.run=^TestSandboxLoopbackServerProcess$",
		"--",
		"sandbox-loopback-server",
		mode,
		strconv.Itoa(os.Getuid()),
		strconv.Itoa(os.Getgid()),
	}
	if !isolated {
		return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
			return util.RunCommandPassthrough(ctx, "", executable, stdin, stdout, stderr, command[1:]...)
		}
	}
	_, err = (Runner{}).LoopbackCommand(t.Context(), command)
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("real loopback namespace case unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		return (Runner{}).RunLoopback(ctx, command, stdin, stdout, stderr)
	}
}

// TestSandboxLoopbackForeignRequest pins that requests from outside an isolated namespace cannot reach its loopback server.
func TestSandboxLoopbackForeignRequest(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real loopback isolation requires Linux user/network namespaces")
	}
	loopbackHostCase(t, loopbackServerCommand(t, true), false)
}

// Negative control runs the identical server without the wrapper. The foreign
// request must reach it, demonstrating the original failure class.
func TestSandboxLoopbackPlantedHostNetwork(t *testing.T) {
	loopbackHostCase(t, loopbackServerCommand(t, false), true)
}

// loopbackHostCase executes the child server and validates reachability of its loopback endpoint from the host.
func loopbackHostCase(t *testing.T, run loopbackTestRunner, wantReachable bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stdout, output := io.Pipe()
	stdin, input := io.Pipe()
	stop := context.AfterFunc(ctx, func() {
		if err := errors.Join(stdout.CloseWithError(ctx.Err()), input.CloseWithError(ctx.Err())); err != nil {
			t.Error(err)
		}
	})
	defer stop()
	t.Cleanup(func() {
		if err := errors.Join(stdout.Close(), input.Close()); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	var stderr strings.Builder
	go func() {
		err := run(ctx, stdin, output, &stderr)
		done <- errors.Join(err, output.CloseWithError(err), stdin.CloseWithError(err))
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "http://127.0.0.1:") {
		cancel()
		if err := errors.Join(stdout.Close(), input.Close()); err != nil {
			t.Error(err)
		}
		t.Fatalf("helper did not publish its loopback server: %q error=%v child=%v stderr=%s", line, err, <-done, stderr.String())
	}
	requestErr := loopbackRequest(ctx, strings.TrimSpace(line))
	if _, err := io.WriteString(input, "x"); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	// Drain the testing package's final output before waiting on the child.
	_, drainErr := io.Copy(io.Discard, stdout)
	childErr := <-done
	if (requestErr == nil) != wantReachable {
		t.Fatalf("foreign request reached=%t want=%t error=%v", requestErr == nil, wantReachable, requestErr)
	}
	if err := errors.Join(drainErr, childErr); err != nil {
		t.Fatalf("loopback helper failed: %v stderr=%s", err, stderr.String())
	}
}
