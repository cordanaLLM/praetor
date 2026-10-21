package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLaneProfile(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		t.Run(lane, func(t *testing.T) {
			spec, err := LaneProfile(lane, nil)
			if err != nil || spec.Limits != DefaultLimits() {
				t.Fatalf("profile = %+v, %v", spec, err)
			}
			want := map[string]NetworkMode{"repair": NetworkNone, "agy": NetworkEgress, "codex": NetworkEgress, "local": NetworkLoopback}
			if spec.Network != want[lane] {
				t.Fatalf("network = %q", spec.Network)
			}
		})
	}
	for _, lane := range []string{"", "unknown", "host", "AGY"} {
		if _, err := LaneProfile(lane, nil); err == nil {
			t.Fatalf("accepted lane %q", lane)
		}
	}
}

func TestLaneProfilePorts(t *testing.T) {
	for _, ports := range [][]int{nil, {1}, {65535}, {1, 2, 3, 4, 5, 6, 7, 65535}} {
		spec, err := LaneProfile("local", ports)
		if err != nil || !slices.Equal(spec.AllowLoopbackPorts, ports) {
			t.Fatalf("ports %v: %+v, %v", ports, spec, err)
		}
	}
	for _, ports := range [][]int{{0}, {-1}, {65536}, {1, 2, 3, 4, 5, 6, 7, 8, 9}} {
		if _, err := LaneProfile("local", ports); err == nil {
			t.Fatalf("accepted ports %v", ports)
		}
	}
	for _, lane := range []string{"repair", "agy", "codex"} {
		if _, err := LaneProfile(lane, []int{4011}); err == nil {
			t.Fatalf("accepted ports for %s", lane)
		}
	}
}

func networkSpec(t *testing.T, lane string, ports []int) Spec {
	t.Helper()
	spec, err := LaneProfile(lane, ports)
	if err != nil {
		t.Fatal(err)
	}
	spec.Workspace, spec.Command = "/source", []string{"/usr/bin/true"}
	spec.HasLib64 = func() bool { return true }
	if spec.Network == NetworkEgress {
		spec.DNS = DNSConfig{Nameservers: []string{"192.0.2.53", "2001:db8::53"}, ResolvConf: "/generated/resolv.conf"}
	}
	return spec
}

func assertArgSequence(t *testing.T, args, want []string) {
	t.Helper()
	if !strings.Contains("\x00"+strings.Join(args, "\x00")+"\x00", "\x00"+strings.Join(want, "\x00")+"\x00") {
		t.Fatalf("missing sequence %q in %q", want, args)
	}
}

func TestSandboxNetworkArgv(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		t.Run(lane, func(t *testing.T) {
			spec := networkSpec(t, lane, nil)
			binaries := testBinaries()
			binaries.Pasta = "/usr/bin/pasta"
			inv, err := Build(spec, binaries, nil)
			if err != nil {
				t.Fatal(err)
			}
			if lane == "repair" {
				assertArgSequence(t, inv.Args, []string{"--unshare-all"})
				if slices.Contains(inv.Args, binaries.Pasta) || slices.Contains(inv.Args, "--share-net") {
					t.Fatal("repair gained network")
				}
				return
			}
			assertPastaArgv(t, inv.Args, spec)
		})
	}
}

func assertPastaArgv(t *testing.T, args []string, spec Spec) {
	t.Helper()
	assertArgSequence(t, args, []string{"-t", "none", "-u", "none", "-T", "none", "-U", "none", "--map-host-loopback", "none"})
	assertArgSequence(t, args, []string{"--unshare-ipc", "--unshare-pid", "--unshare-uts", "--unshare-cgroup"})
	if slices.Contains(args, "--unshare-net") || slices.Contains(args, "--unshare-all") {
		t.Fatal("bwrap replaced pasta network namespace")
	}
	if spec.Network == NetworkLoopback {
		if slices.Contains(args, "--config-net") {
			t.Fatal("loopback configured external networking")
		}
		assertArgSequence(t, args, []string{"--map-host-loopback", "none", "-D", "none"})
		return
	}
	assertArgSequence(t, args, []string{"--config-net", "-D", "192.0.2.53", "-D", "2001:db8::53"})
	assertArgSequence(t, args, []string{"--ro-bind", spec.DNS.ResolvConf, "/etc/resolv.conf"})
}

func TestSandboxLoopbackArgv(t *testing.T) {
	spec := networkSpec(t, "local", []int{1, 4011, 65535})
	b := testBinaries()
	b.Pasta = "/usr/bin/pasta"
	inv, err := Build(spec, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertArgSequence(t, inv.Args, []string{"-T", "1,4011,65535", "-U", "none"})
}

func TestSandboxMissingLaneBinariesNeverStartsCommand(t *testing.T) {
	for _, missing := range []string{"bwrap", "pasta"} {
		for _, lane := range []string{"repair", "agy", "codex", "local"} {
			if missing == "pasta" && lane == "repair" {
				continue
			}
			t.Run(missing+"/"+lane, func(t *testing.T) { assertMissingLaneBinary(t, stubRunner(missing), missing, lane) })
		}
	}
}

func assertMissingLaneBinary(t *testing.T, runner Runner, missing, lane string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "executed")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := networkSpec(t, lane, nil)
	spec.Command = []string{executable, "-test.run=^TestSandboxMarkerProcess$", "--", "sandbox-marker", marker}
	err = runner.Run(t.Context(), spec, nil, nil, nil)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("uncontained command started: %v", statErr)
	}
	assertUnavailable(t, err, missing)
}

func TestSandboxDNSRefusalPreventsExecution(t *testing.T) {
	runner := stubRunner("")
	runner.resolverFile = filepath.Join(t.TempDir(), "missing-upstream")
	_, err := runner.Command(t.Context(), networkSpec(t, "agy", nil))
	if err == nil || !strings.Contains(err.Error(), runner.resolverFile) {
		t.Fatalf("missing upstream not named: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runner.prepareDNS(ctx, NetworkEgress); err == nil {
		t.Fatal("cancelled DNS read accepted")
	}
}

func TestSandboxNetworkBuildRefusals(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		spec := networkSpec(t, lane, nil)
		b := testBinaries()
		b.Pasta = "/usr/bin/pasta"
		b.Bwrap = ""
		_, err := Build(spec, b, nil)
		assertUnavailable(t, err, "bwrap")
		if lane == "repair" {
			continue
		}
		b.Bwrap, b.Pasta = "/usr/bin/bwrap", ""
		_, err = Build(spec, b, nil)
		assertUnavailable(t, err, "pasta")
	}
	spec := networkSpec(t, "agy", nil)
	b := testBinaries()
	b.Pasta = "/usr/bin/pasta"
	spec.DNS = DNSConfig{}
	if _, err := Build(spec, b, nil); err == nil {
		t.Fatal("egress built without DNS")
	}
	spec = networkSpec(t, "repair", nil)
	spec.Command = []string{"/usr/bin/printf", "embedded\x00argument"}
	if _, err := Build(spec, b, nil); err == nil {
		t.Fatal("NUL command would corrupt rendered argv")
	}
}
