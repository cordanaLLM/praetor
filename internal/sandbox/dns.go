package sandbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const upstreamResolverFile = "/run/systemd/resolve/resolv.conf"

// DNSConfig pairs explicit upstream nameservers with their generated resolv.conf.
// Runner prepares it from upstreamResolverFile; Build only validates and renders it.
type DNSConfig struct {
	Nameservers []string
	ResolvConf  string
}

func validateNetworkRuntime(s Spec, b Binaries) error {
	if s.Network != NetworkEgress && s.Network != NetworkLoopback {
		return nil
	}
	if !absolutePath(b.Pasta) {
		return &UnavailableError{Facility: "pasta", Cause: errors.New("requires a clean absolute executable path")}
	}
	if s.Identity == nil || s.Identity.UID < 0 || s.Identity.GID < 0 {
		return errors.New("sandbox egress and loopback modes require the caller's uid and gid (Identity)")
	}
	if s.Network != NetworkEgress {
		return nil
	}
	if !absolutePath(s.DNS.ResolvConf) {
		return errors.New("sandbox egress requires a generated resolv.conf")
	}
	return validateNameservers(s.DNS.Nameservers)
}

func validateNameservers(servers []string) error {
	if len(servers) == 0 || len(servers) > 8 {
		return errors.New("sandbox resolver requires 1..8 upstream nameservers")
	}
	for i := 0; i < len(servers); i++ {
		address, err := netip.ParseAddr(servers[i])
		if err != nil {
			return fmt.Errorf("sandbox resolver nameserver %q: %w", servers[i], err)
		}
		address = address.Unmap()
		if address.IsLoopback() || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
			return fmt.Errorf("sandbox resolver nameserver %q is not an upstream address", servers[i])
		}
	}
	return nil
}

func resolverNameservers(data []byte) ([]string, error) {
	lines := strings.Split(string(data), "\n")
	servers := make([]string, 0, 3)
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 || fields[0] != "nameserver" {
			continue
		}
		if len(fields) < 2 {
			return nil, errors.New("sandbox resolver has an empty nameserver directive")
		}
		servers = append(servers, fields[1])
	}
	if err := validateNameservers(servers); err != nil {
		return nil, err
	}
	return servers, nil
}

func (r Runner) prepareDNS(ctx context.Context, mode NetworkMode) (DNSConfig, error) {
	if mode != NetworkEgress {
		return DNSConfig{}, nil
	}
	if err := ctx.Err(); err != nil {
		return DNSConfig{}, fmt.Errorf("sandbox resolver: %w", err)
	}
	source := r.resolverFile
	if source == "" {
		source = upstreamResolverFile
	}
	data, err := util.ReadFileLimited(source, 64<<10)
	if err != nil {
		return DNSConfig{}, fmt.Errorf("sandbox upstream resolver %s: %w", source, err)
	}
	servers, err := resolverNameservers(data)
	if err != nil {
		return DNSConfig{}, fmt.Errorf("sandbox upstream resolver %s: %w", source, err)
	}
	directory := r.runtimeDirectory
	if directory == "" {
		directory = filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "praetor-sandbox")
	}
	return writeResolver(directory, servers)
}

func writeResolver(directory string, servers []string) (DNSConfig, error) {
	var content strings.Builder
	for i := 0; i < len(servers); i++ {
		content.WriteString("nameserver " + servers[i] + "\n")
	}
	data := []byte(content.String())
	if err := util.MkdirSecure(directory, 0o700); err != nil {
		return DNSConfig{}, fmt.Errorf("sandbox resolver directory: %w", err)
	}
	file := filepath.Join(directory, fmt.Sprintf("resolv-%x.conf", sha256.Sum256(data)))
	if err := util.WriteFileAtomic(file, data, 0o600); err != nil {
		return DNSConfig{}, fmt.Errorf("sandbox generated resolver: %w", err)
	}
	return DNSConfig{Nameservers: servers, ResolvConf: file}, nil
}
