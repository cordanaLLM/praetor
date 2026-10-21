package sandbox

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// NetworkMode selects a private network namespace's connectivity.
type NetworkMode string

const (
	// NetworkNone isolates all networking, including host loopback.
	NetworkNone NetworkMode = "none"
	// NetworkEgress configures pasta networking without host loopback mappings.
	NetworkEgress NetworkMode = "egress"
	// NetworkLoopback forwards only explicitly allowed host-loopback TCP ports.
	NetworkLoopback NetworkMode = "loopback"
)

// LaneProfile selects the shared limits and connectivity. Callers supply workspace,
// command, mounts and environment. Local ports are explicit, with no implicit proxy.
func LaneProfile(lane string, ports []int) (Spec, error) {
	spec := Spec{Limits: DefaultLimits()}
	switch lane {
	case "repair":
		spec.Network = NetworkNone
	case "agy", "codex":
		spec.Network = NetworkEgress
	case "local":
		spec.Network = NetworkLoopback
	default:
		return Spec{}, fmt.Errorf("unknown sandbox lane %q", lane)
	}
	spec.AllowLoopbackPorts = slices.Clone(ports)
	if err := validateNetwork(spec); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func validateNetwork(s Spec) error {
	switch s.Network {
	case "", NetworkNone, NetworkEgress, NetworkLoopback:
	default:
		return fmt.Errorf("unsupported sandbox network mode %q", s.Network)
	}
	if len(s.AllowLoopbackPorts) > 8 {
		return errors.New("sandbox allows at most 8 loopback TCP ports")
	}
	if len(s.AllowLoopbackPorts) != 0 && s.Network != NetworkLoopback {
		return errors.New("loopback ports require the local lane or loopback network mode")
	}
	for i := 0; i < len(s.AllowLoopbackPorts); i++ {
		if s.AllowLoopbackPorts[i] < 1 || s.AllowLoopbackPorts[i] > 65535 {
			return errors.New("sandbox loopback TCP ports must be in 1..65535")
		}
	}
	return nil
}

func pastaArguments(s Spec, b Binaries) []string {
	ports := make([]string, len(s.AllowLoopbackPorts))
	for i := 0; i < len(ports); i++ {
		ports[i] = strconv.Itoa(s.AllowLoopbackPorts[i])
	}
	tcp := "none"
	if len(ports) != 0 {
		tcp = strings.Join(ports, ",")
	}
	args := []string{b.Pasta, "-t", "none", "-u", "none", "-T", tcp, "-U", "none", "--map-host-loopback", "none"}
	if s.Network == NetworkEgress {
		args = append(args, "--config-net")
		for i := 0; i < len(s.DNS.Nameservers); i++ {
			args = append(args, "-D", s.DNS.Nameservers[i])
		}
	} else {
		args = append(args, "-D", "none") // no route out, so no resolver either (pasta: "-D none disables usage of DNS addresses")
	}
	return append(args, "--")
}

func networkArguments(s Spec, b Binaries) []string {
	if s.Network == NetworkEgress || s.Network == NetworkLoopback {
		return pastaArguments(s, b)
	}
	return nil
}

func executionArguments(s Spec, b Binaries) []string {
	return append(networkArguments(s, b), b.Bwrap)
}

func namespaceArguments(s Spec) []string {
	args := []string{"--unshare-all"}
	if s.Network == NetworkEgress || s.Network == NetworkLoopback {
		// pasta owns the private user and network namespaces; retain its mapping.
		args = []string{"--unshare-ipc", "--unshare-pid", "--unshare-uts", "--unshare-cgroup"}
	}
	return append(args, "--die-with-parent")
}

func resolverArguments(s Spec) []string {
	if s.Network == NetworkEgress {
		return []string{"--ro-bind", s.DNS.ResolvConf, "/etc/resolv.conf"}
	}
	return nil
}
