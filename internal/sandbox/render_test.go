package sandbox

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSandboxRenderNetworkEqualsFull(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		t.Run(lane, func(t *testing.T) {
			assertNetworkEqualsFull(t, lane)
		})
	}
}

func assertNetworkEqualsFull(t *testing.T, lane string) {
	t.Helper()
	ports := []int(nil)
	if lane == "local" {
		ports = []int{1, 4011, 65535}
	}
	spec := networkSpec(t, lane, ports)
	b := testBinaries()
	b.Pasta = "/tools with spaces/pasta"
	inv, err := Build(spec, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := inv.Arguments("network")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if lane != "repair" {
		start := slices.Index(inv.Args, b.Pasta)
		end := slices.Index(inv.Args, b.Bwrap)
		if start < 0 || end <= start || inv.Args[end-1] != "--" {
			t.Fatalf("missing pasta prefix: %q", inv.Args)
		}
		want = inv.Args[start:end]
	}
	if !slices.Equal(got, want) {
		t.Fatalf("network differs from full: got %q want %q", got, want)
	}
}

func TestSandboxRenderNamespacesContainedInFull(t *testing.T) {
	for _, lane := range []string{"repair", "agy", "codex", "local"} {
		t.Run(lane, func(t *testing.T) {
			assertNamespacesContainedInFull(t, lane)
		})
	}
}

func assertNamespacesContainedInFull(t *testing.T, lane string) {
	t.Helper()
	spec := networkSpec(t, lane, nil)
	b := testBinaries()
	b.Pasta = "/usr/bin/pasta"
	inv, err := Build(spec, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := inv.Arguments("namespaces")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--unshare-ipc", "--unshare-pid", "--unshare-uts", "--unshare-cgroup",
		"--unshare-user", "--uid", "1000", "--gid", "1000", "--die-with-parent"}
	if lane == "repair" {
		want = []string{"--unshare-all", "--die-with-parent"}
	}
	bwrapArgs := inv.Args[slices.Index(inv.Args, b.Bwrap)+1:]
	assertArgSequence(t, bwrapArgs, want)
	if spec.Network == NetworkEgress {
		resolver := []string{"--ro-bind", spec.DNS.ResolvConf, "/etc/resolv.conf"}
		assertArgSequence(t, bwrapArgs, resolver)
		want = append(want, resolver...)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("namespaces include unexpected or missing arguments: got %q want %q", got, want)
	}
}

func TestSandboxRenderArgumentsRefuseInvalidPart(t *testing.T) {
	for _, part := range []string{"", "all", "Network", "filesystem"} {
		if args, err := (Invocation{}).Arguments(part); err == nil || len(args) != 0 {
			t.Fatalf("invalid part %q: %q, %v", part, args, err)
		}
	}
}

func TestSandboxRenderMissingResolverEmitsNoArguments(t *testing.T) {
	for _, lane := range []string{"agy", "codex"} {
		runner := stubRunner("")
		runner.resolverFile = filepath.Join(t.TempDir(), "missing-resolver")
		inv, err := runner.Command(t.Context(), networkSpec(t, lane, nil))
		if err == nil || !strings.Contains(err.Error(), runner.resolverFile) {
			t.Fatalf("missing resolver not refused: %v", err)
		}
		if inv.Path != "" || len(inv.Args) != 0 || len(inv.network) != 0 || len(inv.namespaces) != 0 {
			t.Fatalf("failure returned arguments: %+v", inv)
		}
	}
}
