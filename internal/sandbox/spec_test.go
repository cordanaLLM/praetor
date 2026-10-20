package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func testBinaries() Binaries {
	return Binaries{SystemdRun: "/usr/bin/systemd-run", Prlimit: "/usr/bin/prlimit", Bwrap: "/usr/bin/bwrap"}
}

func repairProfile() Spec {
	return Spec{Workspace: "/source", ReadOnly: true,
		HasLib64:      func() bool { return true },
		ReadOnlyBinds: []Bind{{Source: "/cache", Target: "/deps"}, {Source: "/toolchain", Target: "/toolchain"}},
		Environment: []Variable{{"HOME", "/home"}, {"PATH", "/usr/bin:/bin"}, {"TMPDIR", "/tmp"},
			{"GOCACHE", "/tmp/go-cache"}, {"GOMODCACHE", "/deps"}, {"GOPATH", "/tmp/go"}, {"GOPROXY", "off"},
			{"GOSUMDB", "off"}, {"GOTOOLCHAIN", "local"}, {"GOTELEMETRY", "off"}, {"GOMAXPROCS", "2"}, {"CGO_ENABLED", "0"}},
		Command: []string{"/toolchain/bin/go", "test", "-json", "-count=1", "-mod=readonly", "-p=2", "-timeout=60s", "./internal/fixture"}}
}

func TestSandboxBuilderRepairGolden(t *testing.T) {
	invocation, err := Build(repairProfile(), testBinaries(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "repairrun.args"))
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := util.NormalizeLineEndings(string(want))
	if got := strings.Join(invocation.Args, "\n") + "\n"; got != expected {
		t.Fatalf("repair profile changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if invocation.Path != "/usr/bin/systemd-run" {
		t.Fatalf("unexpected executable %q", invocation.Path)
	}
}

func TestSandboxBuilderEnvironmentAndWorkspace(t *testing.T) {
	spec := Spec{Workspace: "/source", Command: []string{"/usr/bin/env"}}
	invocation, err := Build(spec, testBinaries(), []string{"SECRET=hidden"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(invocation.Args, "--setenv") || !slices.Contains(invocation.Args, "--clearenv") {
		t.Fatalf("empty pass list inherited environment: %v", invocation.Args)
	}
	if !slices.Contains(invocation.Args, "--bind") || !slices.Contains(invocation.Args, "--unshare-all") || slices.Contains(invocation.Args, "--share-net") {
		t.Fatalf("default workspace/network changed: %v", invocation.Args)
	}
	spec.PassEnvironment = []string{"VISIBLE", "ABSENT"}
	invocation, err = Build(spec, testBinaries(), []string{"SECRET=hidden", "VISIBLE=value"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(invocation.Args, "value") || slices.Contains(invocation.Args, "hidden") || slices.Contains(invocation.Args, "ABSENT") {
		t.Fatalf("pass by name failed: %v", invocation.Args)
	}
}

func TestSandboxBuilderRejectsInvalidSpecs(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Spec)
	}{
		{"empty command", func(s *Spec) { s.Command = nil }},
		{"empty executable", func(s *Spec) { s.Command = []string{""} }},
		{"relative workspace", func(s *Spec) { s.Workspace = "relative" }},
		{"egress unavailable", func(s *Spec) { s.Network = "egress" }},
		{"host network refused", func(s *Spec) { s.Network = "host" }},
		{"partial limits", func(s *Spec) { s.Limits = Limits{Tasks: 1} }},
		{"invalid env", func(s *Spec) { s.PassEnvironment = []string{"BAD=VALUE"} }},
		{"duplicate env", func(s *Spec) { s.Environment = []Variable{{"A", "a"}, {"A", "b"}} }},
		{"reserved mount", func(s *Spec) { s.ReadOnlyBinds = []Bind{{"/source", "/proc/fake"}} }},
		{"relative bind", func(s *Spec) { s.ReadOnlyBinds = []Bind{{"relative", "/deps"}} }},
		{"too many binds", func(s *Spec) { s.ReadOnlyBinds = make([]Bind, 65) }},
		{"too many env", func(s *Spec) { s.PassEnvironment = make([]string, 257) }},
		{"too many args", func(s *Spec) { s.Command = make([]string, 257); s.Command[0] = "true" }},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			spec := repairProfile()
			cases[i].edit(&spec)
			if _, err := Build(spec, testBinaries(), nil); err == nil {
				t.Fatal("invalid spec accepted")
			}
		})
	}
}

func TestSandboxBuilderCustomLimits(t *testing.T) {
	spec := repairProfile()
	spec.Limits = Limits{MemoryBytes: 1024, AddressSpaceBytes: 2048, FileBytes: 512, TmpBytes: 4096, Tasks: 1, CPUPercent: 50, CPUSeconds: 1, OpenFiles: 3}
	invocation, err := Build(spec, testBinaries(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"--property=MemoryMax=1024", "--property=TasksMax=1", "--property=CPUQuota=50%", "--as=2048", "--cpu=1", "--fsize=512", "--nofile=3", "4096"} {
		if !slices.Contains(invocation.Args, arg) {
			t.Fatalf("missing limit %s", arg)
		}
	}
}

func assertLib64Target(t *testing.T, args []string, want string) {
	t.Helper()
	idx := slices.Index(args, "/lib64")
	if idx <= 0 || args[idx-1] != want {
		t.Fatalf("want /lib64 linked to %s, got %v", want, args)
	}
}

func TestSandboxRootFilesystemLayout(t *testing.T) {
	t.Run("positive has usr lib64", func(t *testing.T) {
		assertLib64Target(t, RootFilesystemArguments(true), "usr/lib64")
		spec := Spec{Workspace: "/source", Command: []string{"true"}, HasLib64: func() bool { return true }}
		inv, err := Build(spec, testBinaries(), nil)
		if err != nil {
			t.Fatal(err)
		}
		assertLib64Target(t, inv.Args, "usr/lib64")
	})

	t.Run("negative lacks usr lib64", func(t *testing.T) {
		assertLib64Target(t, RootFilesystemArguments(false), "usr/lib")
		spec := Spec{Workspace: "/source", Command: []string{"true"}, HasLib64: func() bool { return false }}
		inv, err := Build(spec, testBinaries(), nil)
		if err != nil {
			t.Fatal(err)
		}
		assertLib64Target(t, inv.Args, "usr/lib")
	})

	t.Run("boundary host default", func(t *testing.T) {
		expectedTarget := "usr/lib"
		if defaultHasLib64() {
			expectedTarget = "usr/lib64"
		}
		spec := Spec{Workspace: "/source", Command: []string{"true"}}
		inv, err := Build(spec, testBinaries(), nil)
		if err != nil {
			t.Fatal(err)
		}
		assertLib64Target(t, inv.Args, expectedTarget)
	})
}
