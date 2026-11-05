// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// publishRunner is a stub runner on a container engine host with the default Linux port range.
func publishRunner(missing string) Runner {
	r := stubRunner(missing)
	r.ContainerSocket = func() string { return "/run/docker.sock" }
	r.HostPortRange = func() (PortRange, error) { return PortRange{First: 32768, Last: 60999}, nil }
	return r
}

// TestSandboxPublishExecArguments pins the container host invocation: pasta forwards only the publish
// range out, nothing in, and the nested bubblewrap maps the caller back.
func TestSandboxPublishExecArguments(t *testing.T) {
	command := []string{"/tmp/test binary", "-test.run=Publish"}
	inv, err := publishRunner("").LoopbackCommand(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := currentIdentity()
	want := []string{"-t", "none", "-u", "none", "-T", "32768-60999", "-U", "none", "--map-host-loopback", "none", "-D", "none", "--",
		"/usr/bin/sh", "-c", namespaceRangeScript, "praetor-test-netns", "61000", "65535", "/usr/bin/bwrap"}
	want = append(want, namespaceArguments(Spec{Network: NetworkLoopback, Identity: &Identity{UID: uid, GID: gid}})...)
	want = append(want, "--dev-bind", "/", "/", "--proc", "/proc", "--", "/tmp/test binary", "-test.run=Publish")
	if inv.Path != "/usr/bin/pasta" || !slices.Equal(inv.Args, want) {
		t.Fatalf("wrong publish invocation:\n got %q\nwant %q", inv.Args, want)
	}
	if slices.Contains(inv.Args, "--config-net") || slices.Contains(inv.Args, "--unshare-net") {
		t.Fatalf("publish invocation configures a route out or a second network namespace: %q", inv.Args)
	}
}

// TestSandboxPublishUnavailable pins fail-closed results on a container host: no NetworkNone or
// direct invocation is returned when pasta, the range or the namespaces are missing.
func TestSandboxPublishUnavailable(t *testing.T) {
	for _, missing := range []string{"pasta", "sh", "bwrap"} {
		inv, err := publishRunner(missing).LoopbackCommand(t.Context(), []string{"/test"})
		assertUnavailable(t, err, missing)
		if inv.Path != "" {
			t.Fatalf("missing %s returned an invocation: %+v", missing, inv)
		}
	}
	runner := publishRunner("")
	runner.HostPortRange = func() (PortRange, error) { return PortRange{}, errors.New("planted unreadable range") }
	_, err := runner.LoopbackCommand(t.Context(), []string{"/test"})
	assertUnavailable(t, err, "container publish port range")
	runner = publishRunner("")
	runner.HostPortRange = func() (PortRange, error) { return PortRange{First: 1024, Last: 65535}, nil }
	_, err = runner.LoopbackCommand(t.Context(), []string{"/test"})
	assertUnavailable(t, err, "fewer than 1024 ports")
	runner = publishRunner("")
	runner.ProbeBwrap = func(context.Context) error { return errors.New("planted EPERM") }
	_, err = runner.LoopbackCommand(t.Context(), []string{"/must-not-execute"})
	assertUnavailable(t, err, "container socket /run/docker.sock")
}

// TestSandboxPublishPrivateRange pins the namespace's own range: above the publish range, else below,
// else unavailable; malformed ranges are refused.
func TestSandboxPublishPrivateRange(t *testing.T) {
	cases := []struct {
		publish, want PortRange
		ok            bool
	}{
		{PortRange{32768, 60999}, PortRange{61000, 65535}, true},
		{PortRange{32768, 64511}, PortRange{64512, 65535}, true},
		{PortRange{32768, 64512}, PortRange{1024, 32767}, true},
		{PortRange{2048, 65535}, PortRange{1024, 2047}, true},
		{PortRange{2047, 65535}, PortRange{}, false},
		{PortRange{1, 65535}, PortRange{}, false},
		{PortRange{0, 60999}, PortRange{}, false},
		{PortRange{40000, 39999}, PortRange{}, false},
		{PortRange{32768, 65536}, PortRange{}, false},
	}
	for _, c := range cases {
		got, err := privateRange(c.publish)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("privateRange(%s) = %s, %v; want %s ok=%t", c.publish, got, err, c.want, c.ok)
		}
	}
}

// TestSandboxPublishParseRange pins the procfs reader's accepted shape.
func TestSandboxPublishParseRange(t *testing.T) {
	if got, err := parsePortRange("32768\t60999\n"); err != nil || got != (PortRange{32768, 60999}) {
		t.Fatalf("parsePortRange = %v, %v", got, err)
	}
	for _, bad := range []string{"", "32768", "32768 60999 1", "a 60999", "32768 b"} {
		if _, err := parsePortRange(bad); err == nil {
			t.Errorf("parsePortRange(%q) accepted", bad)
		}
	}
}

// TestSandboxPublishContainerSocket pins socket discovery: a set DOCKER_HOST alone decides (its unix
// path when present, nothing for another scheme or a missing path, even with the system socket
// present); unset, the system, rootless Docker and Podman sockets in that order.
func TestSandboxPublishContainerSocket(t *testing.T) {
	present := func(paths ...string) func(string) bool {
		return func(path string) bool { return slices.Contains(paths, path) }
	}
	all := []string{"/custom/docker.sock", "/var/run/docker.sock", "/run/user/1000/docker.sock", "/run/user/1000/podman/podman.sock"}
	cases := []struct {
		dockerHost string
		present    []string
		want       string
	}{
		{"unix:///custom/docker.sock", all, "/custom/docker.sock"},
		{"unix:///missing/docker.sock", all, ""},
		{"tcp://127.0.0.1:2375", all, ""},
		{"ssh://builder", all, ""},
		{"", all, "/var/run/docker.sock"},
		{"", []string{"/run/user/1000/docker.sock", "/run/user/1000/podman/podman.sock"}, "/run/user/1000/docker.sock"},
		{"", []string{"/run/user/1000/podman/podman.sock"}, "/run/user/1000/podman/podman.sock"},
		{"", nil, ""},
	}
	for _, c := range cases {
		env := map[string]string{"DOCKER_HOST": c.dockerHost, "XDG_RUNTIME_DIR": "/run/user/1000"}
		if got := containerSocket(func(name string) string { return env[name] }, present(c.present...)); got != c.want {
			t.Errorf("DOCKER_HOST=%q present=%q: containerSocket = %q, want %q", c.dockerHost, c.present, got, c.want)
		}
	}
}

// Re-executed test helper: dials the two host ports given and prints which ones answered.
func TestSandboxPublishDialProcess(t *testing.T) {
	index := slices.Index(os.Args, "sandbox-publish-dial")
	if index < 0 {
		return
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	for _, port := range os.Args[index+1 : index+3] {
		conn, err := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
		if err == nil {
			err = conn.Close()
			fmt.Printf("%s reached %v\n", port, err)
			continue
		}
		fmt.Printf("%s refused\n", port)
	}
}

// hostListener listens on host loopback: port 0 draws from the kernel's ephemeral range, as Docker's
// published ports do; a fixed port outside the range stands for the workstation's own services.
func hostListener(t *testing.T, port int) (net.Listener, bool) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, false
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	go func() {
		for i := 0; i < 64; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if err := conn.Close(); err != nil {
				return
			}
		}
	}()
	return ln, true
}

// publishDial runs the dial helper through runner and returns its output. A runner refusing the
// command fails the test: the caller checked the facilities it needs (publishFacilities).
func publishDial(t *testing.T, runner Runner, inside, outside int) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := []string{executable, "-test.run=^TestSandboxPublishDialProcess$", "--", "sandbox-publish-dial", strconv.Itoa(inside), strconv.Itoa(outside)}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var stdout, stderr strings.Builder
	if err := runner.RunLoopback(ctx, command, nil, &stdout, &stderr); err != nil {
		t.Fatalf("dial helper: %v stderr=%s", err, stderr.String())
	}
	return stdout.String()
}

// publishFacilities skips the real case only when the host lacks a facility the publish path
// needs: Linux, pasta on PATH, or bubblewrap namespaces (a NetworkNone probe). With those present,
// any refusal of the publish invocation is a defect and fails the case.
func publishFacilities(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the publish namespace needs Linux")
	}
	if _, err := (Runner{}).lookup("pasta"); err != nil {
		t.Skipf("pasta is not installed: %v", err)
	}
	if _, err := (Runner{ContainerSocket: noContainer}).LoopbackCommand(t.Context(), []string{"/bin/true"}); err != nil {
		t.Skipf("bubblewrap namespaces unavailable: %v", err)
	}
}

// insideHostPort is a host-loopback listener's port drawn from the kernel's range, as Docker draws
// published ports.
func insideHostPort(t *testing.T) int {
	t.Helper()
	publish, err := readHostPortRange()
	if err != nil {
		t.Fatalf("host port range: %v", err)
	}
	ln, ok := hostListener(t, 0)
	if !ok {
		t.Fatal("host loopback listener on port 0 failed")
	}
	addr, isTCP := ln.Addr().(*net.TCPAddr)
	if !isTCP || addr.Port < publish.First || addr.Port > publish.Last {
		t.Fatalf("port 0 drew %v, outside the kernel's range %s", ln.Addr(), publish)
	}
	return addr.Port
}

// outsideHostPort is a host-loopback listener on a free fixed port below the kernel's range,
// standing for a workstation service.
func outsideHostPort(t *testing.T) int {
	t.Helper()
	publish, err := readHostPortRange()
	if err != nil {
		t.Fatalf("host port range: %v", err)
	}
	for p := publish.First - 1; p >= 1024 && p > publish.First-200; p-- {
		if _, ok := hostListener(t, p); ok {
			return p
		}
	}
	t.Fatalf("no free host port among the 199 below %d", publish.First)
	return 0
}

// TestSandboxPublishReachesContainerPorts runs a real test binary on a container host: a host port
// from the publish range answers, a host service outside it does not. The planted NetworkNone
// control is the failure #1770 shipped (testcontainers: wait for reaper, connection refused).
func TestSandboxPublishReachesContainerPorts(t *testing.T) {
	publishFacilities(t)
	inside, outside := insideHostPort(t), outsideHostPort(t)
	socket := func() string { return "/planted/container.sock" }
	got := publishDial(t, Runner{ContainerSocket: socket}, inside, outside)
	want := fmt.Sprintf("%d reached <nil>\n%d refused\n", inside, outside)
	if !strings.HasPrefix(got, want) {
		t.Fatalf("publish namespace dials: got %q, want prefix %q", got, want)
	}
	got = publishDial(t, Runner{ContainerSocket: noContainer}, inside, outside)
	if want := fmt.Sprintf("%d refused\n%d refused\n", inside, outside); !strings.HasPrefix(got, want) {
		t.Fatalf("planted NetworkNone control: got %q, want prefix %q", got, want)
	}
}
