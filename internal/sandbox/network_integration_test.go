package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The same probe runs in both contained profiles and the planted host-network shape.
func TestSandboxNetworkProbeProcess(t *testing.T) {
	index := slices.Index(os.Args, "sandbox-network-probe")
	if index < 0 {
		return
	}
	host, port, want := os.Args[index+1], os.Args[index+2], os.Args[index+3]
	assertPrivateNamespaces(t)
	assertUnprivilegedIdentity(t)
	if host == "gateway" {
		host = probeDefaultGateway(t)
	}
	if host == "no-route" {
		assertNoExternalRoute(t)
		host = "192.0.2.1"
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	reached := err == nil
	if connection != nil {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if reached != (want == "reachable") {
		t.Fatalf("probe %s:%s reached=%t want=%s error=%v", host, port, reached, want, err)
	}
	fmt.Println("SANDBOX_NETWORK_PROBE_OK")
}

// assertUnprivilegedIdentity checks, inside the sandbox, that the command runs as the caller
// (SANDBOX_EXPECT_UID) with no effective capability and cannot unmount a profile mount.
func assertUnprivilegedIdentity(t *testing.T) {
	t.Helper()
	if want := os.Getenv("SANDBOX_EXPECT_UID"); want != "" && strconv.Itoa(os.Getuid()) != want {
		t.Fatalf("sandboxed command runs as uid %d, want the caller's %s", os.Getuid(), want)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "CapEff:" && strings.Trim(fields[1], "0") != "" {
			t.Fatalf("sandboxed command holds capabilities %s", fields[1])
		}
	}
	if err := syscall.Unmount("/usr", syscall.MNT_DETACH); err == nil {
		t.Fatal("sandboxed command unmounted /usr")
	}
}

func probeDefaultGateway(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i := 1; i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		if len(fields) < 4 || fields[1] != "00000000" || fields[2] == "00000000" {
			continue
		}
		gateway, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		var address [4]byte
		binary.LittleEndian.PutUint32(address[:], uint32(gateway))
		return netip.AddrFrom4(address).String()
	}
	t.Fatal("egress namespace has no default gateway; gateway refusal was not exercised")
	return ""
}

func assertNoExternalRoute(t *testing.T) {
	t.Helper()
	for _, file := range []string{"/proc/net/route", "/proc/net/ipv6_route"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i := 0; i < len(lines); i++ {
			fields := strings.Fields(lines[i])
			if len(fields) == 0 || fields[0] == "Iface" {
				continue
			}
			if fields[0] != "lo" && fields[len(fields)-1] != "lo" {
				t.Fatalf("loopback namespace has external route: %s", lines[i])
			}
		}
	}
}

func realNetworkBinaries(t *testing.T) Binaries {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("real network refusal tests require Linux user namespaces")
	}
	r := Runner{}
	b, err := r.resolve(NetworkEgress)
	if err != nil {
		t.Skipf("real network refusal test prerequisite unavailable: %v", err)
	}
	if err := r.probeBwrap(t.Context(), b.Bwrap); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "not permitted") || strings.Contains(message, "permission denied") || strings.Contains(message, "user namespaces are not enabled") {
			t.Skipf("unprivileged user namespaces unavailable: %v", err)
		}
		t.Fatalf("real bwrap prerequisite failed: %v", err)
	}
	return b
}

func hostLoopbackListener(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		t.Fatalf("invalid ephemeral range: %q", data)
	}
	first, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatal(err)
	}
	start := firstUnprivilegedPort(t)
	// At most 4096 attempts below the ephemeral range, starting where pasta can bind.
	for port := start; port < min(first, start+4096); port++ {
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		t.Cleanup(func() {
			if err := listener.Close(); err != nil {
				t.Error(err)
			}
		})
		return port
	}
	t.Fatal("no free loopback TCP port below the ephemeral range")
	return 0
}

func runNetworkProbe(t *testing.T, b Binaries, spec Spec, host string, port int, want string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec.ReadOnlyBinds = append(spec.ReadOnlyBinds, Bind{executable, "/probe"})
	recordHostNamespaces(t, &spec)
	spec.Environment = append(spec.Environment, Variable{Name: "SANDBOX_EXPECT_UID", Value: strconv.Itoa(os.Getuid())})
	spec.Command = []string{"/probe", "-test.run=^TestSandboxNetworkProbeProcess$", "--", "sandbox-network-probe", host, strconv.Itoa(port), want}
	inv, err := Build(spec, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise prlimit -> pasta -> bwrap even on hosts without a systemd user bus.
	// Full scope execution remains covered independently by TestSandboxRealIntegration.
	start := slices.Index(inv.Args, b.Prlimit)
	if start < 0 {
		t.Fatal("profile lost prlimit")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	stderr := &tailWriter{limit: 4096}
	err = util.RunCommandPassthrough(ctx, "", b.Prlimit, nil, &stdout, stderr, inv.Args[start+1:]...)
	if err != nil {
		t.Fatalf("network probe: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "SANDBOX_NETWORK_PROBE_OK") {
		t.Fatalf("probe never executed: %s %s", stdout.String(), stderr.String())
	}
}

func realNetworkSpec(t *testing.T, lane string, ports []int) Spec {
	t.Helper()
	spec := networkSpec(t, lane, ports)
	spec.Workspace = t.TempDir()
	// Isolate the port policy from descriptor exhaustion in the all-ports mutant.
	// The default descriptor cap is pinned separately by the repair argv golden.
	spec.Limits.OpenFiles = 262144
	if spec.Network == NetworkEgress {
		dns, err := (Runner{runtimeDirectory: t.TempDir()}).prepareDNS(t.Context(), spec.Network)
		if err != nil {
			t.Fatal(err)
		}
		spec.DNS = dns
	}
	return spec
}

func TestSandboxRealNetworkRefusal(t *testing.T) {
	b := realNetworkBinaries(t)
	port := hostLoopbackListener(t)
	cases := []struct {
		name, lane, host, want string
		ports                  []int
	}{
		{"egress-loopback", "agy", "127.0.0.1", "refused", nil},
		{"egress-gateway", "codex", "gateway", "refused", nil},
		{"loopback-denied", "local", "127.0.0.1", "refused", nil},
		{"loopback-allowed", "local", "127.0.0.1", "reachable", []int{port}},
		{"loopback-no-route", "local", "no-route", "refused", []int{port}},
	}
	for i := 0; i < len(cases); i++ {
		test := cases[i]
		t.Run(test.name, func(t *testing.T) {
			runNetworkProbe(t, b, realNetworkSpec(t, test.lane, test.ports), test.host, port, test.want)
		})
	}
}

func TestSandboxRealPlantedHostNetworkReachesListener(t *testing.T) {
	b := realNetworkBinaries(t)
	port := hostLoopbackListener(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--unshare-pid", "--die-with-parent"}
	args = append(args, RootFilesystemArguments(defaultHasLib64())...)
	args = append(args, "--ro-bind", executable, "/probe", "--", "/probe", "-test.run=^TestSandboxNetworkProbeProcess$", "--", "sandbox-network-probe", "127.0.0.1", strconv.Itoa(port), "reachable")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, filepath.Dir(executable), b.Bwrap, 8192, args...)
	if err != nil || !strings.Contains(string(result.Stdout), "SANDBOX_NETWORK_PROBE_OK") {
		t.Fatalf("planted host-network shape did not reach listener: %v %s %s", err, result.Stdout, result.Stderr)
	}
}

func TestSandboxRealMissingLaneBinaries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real executable lookup requires Linux")
	}
	for _, missing := range []string{"bwrap", "pasta"} {
		for _, lane := range []string{"repair", "agy", "codex", "local"} {
			if missing == "pasta" && lane == "repair" {
				continue
			}
			t.Run(missing+"/"+lane, func(t *testing.T) { realMissingLaneBinary(t, missing, lane) })
		}
	}
}

func recordHostNamespaces(t *testing.T, spec *Spec) {
	t.Helper()
	for _, name := range []string{"user", "net", "pid", "mnt", "ipc", "uts", "cgroup"} {
		value, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			t.Fatal(err)
		}
		spec.Environment = append(spec.Environment, Variable{"HOST_NS_" + name, value})
	}
}

func assertPrivateNamespaces(t *testing.T) {
	t.Helper()
	for _, name := range []string{"user", "net", "pid", "mnt", "ipc", "uts", "cgroup"} {
		host := os.Getenv("HOST_NS_" + name)
		if host == "" {
			continue
		} // Planted profile deliberately retains host namespaces.
		value, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if value == host {
			t.Fatalf("profile retained host %s namespace %s", name, value)
		}
	}
}

func realMissingLaneBinary(t *testing.T, missing, lane string) {
	t.Helper()
	runner := Runner{Lookup: func(name string) (string, error) {
		if name == missing {
			return "", os.ErrNotExist
		}
		return (Runner{}).lookup(name)
	}}
	assertMissingLaneBinary(t, runner, missing, lane)
}

func firstUnprivilegedPort(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return max(1, port)
}
