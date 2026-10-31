package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSandboxResolverSourceAndGeneratedBind(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("generated resolver bind paths require the Linux sandbox runtime")
	}
	source := filepath.Join(t.TempDir(), "upstream")
	if err := os.WriteFile(source, []byte("# upstream\nnameserver 192.0.2.53\nsearch ignored\nnameserver 2001:db8::53 # comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := stubRunner("")
	r.resolverFile, r.runtimeDirectory = source, t.TempDir()
	dns, err := r.prepareDNS(t.Context(), NetworkEgress)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dns.Nameservers, []string{"192.0.2.53", "2001:db8::53"}) {
		t.Fatalf("nameservers = %v", dns.Nameservers)
	}
	data, err := os.ReadFile(dns.ResolvConf)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nameserver 192.0.2.53\nnameserver 2001:db8::53\n" {
		t.Fatalf("generated resolver = %q", data)
	}
	spec := networkSpec(t, "agy", nil)
	spec.DNS = dns
	b := testBinaries()
	b.Pasta = "/usr/bin/pasta"
	inv, err := Build(spec, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPastaArgv(t, inv.Args, spec)
}

func TestSandboxResolverRefusesInvalidSource(t *testing.T) {
	cases := []string{"", "# no nameservers\n", "nameserver\n", "nameserver bogus\n", "nameserver 127.0.0.53\n", "nameserver ::1\n", "nameserver ::ffff:127.0.0.1\n", "nameserver 0.0.0.0\n", "nameserver ff02::1\n", strings.Repeat("nameserver 192.0.2.53\n", 9)}
	for i := 0; i < len(cases); i++ {
		if _, err := resolverNameservers([]byte(cases[i])); err == nil {
			t.Fatalf("accepted resolver %q", cases[i])
		}
	}
	r := Runner{resolverFile: filepath.Join(t.TempDir(), "empty"), runtimeDirectory: t.TempDir()}
	if err := os.WriteFile(r.resolverFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prepareDNS(t.Context(), NetworkEgress); err == nil || !strings.Contains(err.Error(), r.resolverFile) {
		t.Fatalf("empty resolver not named: %v", err)
	}
}

func TestSandboxResolverNameserverBoundaries(t *testing.T) {
	for _, count := range []int{1, 8} {
		servers, err := resolverNameservers([]byte(strings.Repeat("nameserver 192.0.2.53\n", count)))
		if err != nil || len(servers) != count {
			t.Fatalf("%d servers: %v, %v", count, servers, err)
		}
	}
}
