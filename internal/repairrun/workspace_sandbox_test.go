package repairrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/sandbox"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Exercise the actual repair caller against the pre-extraction full argv oracle,
// including its discovered toolchain, module cache and explicit environment.
func TestRepairSandboxProfileGolden(t *testing.T) {
	skipWithoutPosixLinks(t)
	dir := t.TempDir()
	root, cache := filepath.Join(dir, "toolchain"), filepath.Join(dir, "mod")
	actualRoot := filepath.Join(dir, "real-toolchain")
	if err := os.MkdirAll(filepath.Join(actualRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualRoot, root); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(root, "bin", "go")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$2\" in\nGOROOT) printf '%%s\\n' '%s';;\nGOMODCACHE) printf '%%s\\n' '%s';;\n*) exit 1;;\nesac\n", root, cache)
	writeFixture(t, goPath, []byte(script))
	if err := os.Chmod(goPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(goPath))
	spec, err := verificationSpec(t.Context(), Config{TestPackages: []string{"./internal/fixture"}}, "/source")
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := sandbox.Build(spec, sandbox.Binaries{SystemdRun: "/usr/bin/systemd-run", Prlimit: "/usr/bin/prlimit", Bwrap: "/usr/bin/bwrap"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "sandbox", "testdata", "repairrun.args"))
	if err != nil {
		t.Fatal(err)
	}
	golden, _ := util.NormalizeLineEndings(string(want))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.NewReplacer("/toolchain", resolvedRoot, "/cache", cache).Replace(golden)
	if got := strings.Join(invocation.Args, "\n") + "\n"; got != expected {
		t.Fatalf("repair sandbox profile changed:\ngot:\n%s\nwant:\n%s", got, expected)
	}
}
