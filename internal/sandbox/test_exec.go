// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PortRange is an inclusive TCP port range.
type PortRange struct{ First, Last int }

// String renders the range as pasta and procfs spell it, "first-last".
func (p PortRange) String() string { return strconv.Itoa(p.First) + "-" + strconv.Itoa(p.Last) }

const (
	// hostPortRangeFile is the kernel's ephemeral port range; Docker's port allocator
	// (libnetwork portallocator) draws the host ports it publishes for a container from it.
	hostPortRangeFile = "/proc/sys/net/ipv4/ip_local_port_range"
	// minNamespacePorts is the smallest ephemeral range left for the test binary's own sockets.
	minNamespacePorts = 1024
	// namespaceRangeScript sets the private namespace's ephemeral range outside the forwarded one
	// (pasta listens on every forwarded port inside the namespace, so bind(0) found none free), then
	// execs bubblewrap. It runs as root of pasta's user namespace; arguments arrive as positional
	// parameters, never interpolated into the script.
	namespaceRangeScript = `printf '%s %s\n' "$1" "$2" > /proc/sys/net/ipv4/ip_local_port_range || exit 125; shift 2; exec "$@"`
)

// loopbackArguments reuses NetworkNone's namespace policy, with the caller's
// filesystem and environment so go test's binary, working directory and fixtures
// remain available. Bubblewrap maps the caller's own UID/GID without capabilities
// and brings up the private namespace's sole interface, lo.
func loopbackArguments(command []string) []string {
	return append(namespaceArguments(Spec{Network: NetworkNone}), testExecTail(command)...)
}

// testExecTail is the bubblewrap tail both modes share: the caller's filesystem, a private /proc,
// then the command.
func testExecTail(command []string) []string {
	return append([]string{"--dev-bind", "/", "/", "--proc", "/proc", "--"}, command...)
}

// publishArguments is the argv after pasta for a container engine host: pasta forwards connects to
// host-loopback ports of the publish range (and only those) out of the namespace, accepts nothing
// inbound, and a nested user namespace maps the command back to the caller (NetworkLoopback).
func publishArguments(b Binaries, sh string, publish, private PortRange, id Identity, command []string) []string {
	spec := Spec{Network: NetworkLoopback, Identity: &id}
	args := pastaForward(spec, b, publish.String())[1:]
	args = append(args, sh, "-c", namespaceRangeScript, "praetor-test-netns",
		strconv.Itoa(private.First), strconv.Itoa(private.Last), b.Bwrap)
	args = append(args, namespaceArguments(spec)...)
	return append(args, testExecTail(command)...)
}

// privateRange is the namespace's own ephemeral range: the ports above the publish range, else
// those below it down to 1024; fewer than minNamespacePorts free ports is unavailable.
func privateRange(publish PortRange) (PortRange, error) {
	if publish.First < 1 || publish.Last > 65535 || publish.First > publish.Last {
		return PortRange{}, fmt.Errorf("host port range %s is not within 1-65535", publish)
	}
	if 65535-publish.Last >= minNamespacePorts {
		return PortRange{First: publish.Last + 1, Last: 65535}, nil
	}
	if publish.First-minNamespacePorts >= minNamespacePorts {
		return PortRange{First: minNamespacePorts, Last: publish.First - 1}, nil
	}
	return PortRange{}, fmt.Errorf("host port range %s leaves fewer than %d ports for the namespace", publish, minNamespacePorts)
}

// parsePortRange reads ip_local_port_range's "first<whitespace>last".
func parsePortRange(text string) (PortRange, error) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return PortRange{}, fmt.Errorf("port range %q: want two numbers", strings.TrimSpace(text))
	}
	first, errFirst := strconv.Atoi(fields[0])
	last, errLast := strconv.Atoi(fields[1])
	if err := errors.Join(errFirst, errLast); err != nil {
		return PortRange{}, fmt.Errorf("port range %q: %w", strings.TrimSpace(text), err)
	}
	return PortRange{First: first, Last: last}, nil
}

// readHostPortRange reads the host's ephemeral port range from procfs.
func readHostPortRange() (PortRange, error) {
	data, err := util.ReadFileLimited(hostPortRangeFile, 64)
	if err != nil {
		return PortRange{}, err
	}
	return parsePortRange(string(data))
}

// containerSocket names the container engine socket test binaries reach, as testcontainers finds
// it, or "" when there is none on this host: a set DOCKER_HOST alone decides (its unix:// path when
// present; any other scheme is an engine whose published ports are not on this host's loopback),
// else the first present of the system socket, rootless Docker's and Podman's.
func containerSocket(getenv func(string) string, present func(string) bool) string {
	if host := getenv("DOCKER_HOST"); host != "" {
		if path, unix := strings.CutPrefix(host, "unix://"); unix && present(path) {
			return path
		}
		return ""
	}
	candidates := []string{"/var/run/docker.sock"}
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		// Unix socket paths: path, not filepath, so a Windows build keeps forward slashes.
		candidates = append(candidates, path.Join(dir, "docker.sock"), path.Join(dir, "podman", "podman.sock"))
	}
	for i := 0; i < len(candidates); i++ {
		if present(candidates[i]) {
			return candidates[i]
		}
	}
	return ""
}

// socketPresent reports whether path exists and is a Unix socket.
func socketPresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// containerHost reports the container engine socket the runner sees.
func (r Runner) containerHost() string {
	if r.ContainerSocket != nil {
		return r.ContainerSocket()
	}
	return containerSocket(os.Getenv, socketPresent)
}

// hostPortRange reports the host range container engines publish into.
func (r Runner) hostPortRange() (PortRange, error) {
	if r.HostPortRange != nil {
		return r.HostPortRange()
	}
	return readHostPortRange()
}

// LoopbackCommand prepares a test-binary invocation without the agent profile's
// filesystem, environment or resource restrictions. Namespace failures never
// return a direct invocation of the requested command. With a container engine
// socket present the namespace also reaches the host ports the engine publishes
// (publishCommand); without one it is NetworkNone.
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
	invocation, facility, err := r.testExecInvocation(command)
	if err != nil {
		return Invocation{}, err
	}
	if err := r.probeLoopback(ctx, invocation, command); err != nil {
		if errors.Is(err, ErrUnavailable) {
			return Invocation{}, err
		}
		return Invocation{}, &UnavailableError{Facility: facility, Cause: err}
	}
	return invocation, nil
}

// testExecInvocation picks the namespace for a test binary and names its facility: NetworkNone
// under bubblewrap, or pasta forwarding the container publish range when a container engine
// socket is present (publishCommand).
func (r Runner) testExecInvocation(command []string) (Invocation, string, error) {
	bwrap, err := r.lookup("bwrap")
	if err != nil {
		return Invocation{}, "", err
	}
	socket := r.containerHost()
	if socket == "" {
		return Invocation{Path: bwrap, Args: loopbackArguments(command)}, "bwrap user/network namespaces (loopback-only)", nil
	}
	invocation, err := r.publishCommand(bwrap, command)
	return invocation, "pasta and bwrap namespaces forwarding container ports (container socket " + socket + ")", err
}

// publishCommand is the container engine host's invocation: pasta owns the network namespace and
// forwards the host's publish range out; a missing pasta or an unusable range fails closed.
func (r Runner) publishCommand(bwrap string, command []string) (Invocation, error) {
	pasta, err := r.lookup("pasta")
	if err != nil {
		return Invocation{}, err
	}
	sh, err := r.lookup("sh")
	if err != nil {
		return Invocation{}, err
	}
	publish, err := r.hostPortRange()
	if err == nil {
		var private PortRange
		if private, err = privateRange(publish); err == nil {
			uid, gid := currentIdentity()
			b := Binaries{Bwrap: bwrap, Pasta: pasta}
			return Invocation{Path: pasta, Args: publishArguments(b, sh, publish, private, Identity{UID: uid, GID: gid}, command)}, nil
		}
	}
	return Invocation{}, &UnavailableError{Facility: "container publish port range", Cause: err}
}

// probeLoopback checks that the chosen invocation starts a trivial command in its namespaces.
func (r Runner) probeLoopback(ctx context.Context, invocation Invocation, command []string) error {
	if r.ProbeBwrap != nil {
		return r.ProbeBwrap(ctx)
	}
	probeTarget, err := r.lookup("true")
	if err != nil {
		return err
	}
	args := append([]string{}, invocation.Args[:len(invocation.Args)-len(command)]...)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, "", invocation.Path, 4096, append(args, probeTarget)...)
	return util.CommandDiagnostic(err, result.Stderr)
}

// RunLoopback runs only the compiled test binary in its namespace. The caller owns
// the deadline; go test's build and module downloads run before this boundary.
func (r Runner) RunLoopback(ctx context.Context, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	invocation, err := r.LoopbackCommand(ctx, command)
	if err != nil {
		return err
	}
	sh, err := r.lookup("sh")
	if err != nil {
		return err
	}
	if err := runSession(ctx, invocation, sh, command, stdin, stdout, stderr); err != nil {
		return fmt.Errorf("go test loopback namespace: %w", err)
	}
	return nil
}
