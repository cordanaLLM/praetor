// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sandbox"
	"github.com/cordanaLLM/praetor/internal/util"
)

// bwrapOnly is the wrapper's NetworkNone path: no container socket, so no pasta, which finds no
// IPv4 route when this suite itself runs inside the engine's wrapper. Both paths give the test
// binary a user namespace of its own, the property the real planted case measures.
var bwrapOnly = sandbox.Runner{ContainerSocket: func() string { return "" }}

// serveTestExec makes this test binary its own go test -exec wrapper, as praetorctl go test-exec
// is in production: goTestExec names os.Executable(), so the race command of the real planted case
// invokes "<gating.test> go test-exec BINARY ARGS". It reports the exit code and whether args
// asked for the wrapper; TestMain calls it first, so the wrapper never reruns this suite.
func serveTestExec(args []string) (int, bool) {
	if len(args) < 3 || args[1] != "go" || args[2] != "test-exec" {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), MaxTestStageTimeout)
	defer cancel()
	if err := bwrapOnly.RunLoopback(ctx, args[3:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

// Fixture tests of the real planted case. needsRootTest is the shape of the knowledge-retrieval
// ssh-keygen owner check: the filesystem root must belong to the uid the gate itself sees, in the
// gate's own user namespace; the loopback wrapper's user namespace maps only the caller and shows
// the overflow uid. siblingTest passes only inside the wrapper, so it proves the sibling ran
// wrapped.
const (
	needsRootTest = `package needsroot

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

func TestRootOwnsTheFilesystemRoot(t *testing.T) {
	info, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no stat_t")
	}
	if got := strconv.FormatUint(uint64(stat.Uid), 10); got != os.Getenv("PRAETOR_FIXTURE_ROOT_UID") {
		t.Fatalf("filesystem root owner uid %s, want the gate's %s", got, os.Getenv("PRAETOR_FIXTURE_ROOT_UID"))
	}
	if ns, err := os.Readlink("/proc/self/ns/user"); err != nil || ns != os.Getenv("PRAETOR_FIXTURE_USERNS") {
		t.Fatalf("user namespace %s (%v), want the gate's", ns, err)
	}
}
`
	siblingTest = `package sibling

import (
	"os"
	"testing"
)

func TestRunsInsideTheWrapper(t *testing.T) {
	if ns, err := os.Readlink("/proc/self/ns/user"); err != nil || ns == os.Getenv("PRAETOR_FIXTURE_USERNS") {
		t.Fatalf("user namespace %s (%v) is the gate's own: the wrapper did not run", ns, err)
	}
}
`
)

// realNetnsRepo writes the two fixture modules with their tests, and the manifest when not empty.
func realNetnsRepo(t *testing.T, manifest string) string {
	t.Helper()
	root := netnsRepo(t, manifest, netnsModule, "tools/sibling/go.mod")
	for name, source := range map[string]string{"tools/needs-root/owner_test.go": needsRootTest, "tools/sibling/sibling_test.go": siblingTest} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// realRaceConfig is a stage configuration backed by the real toolchain and runner on local Linux,
// or skips when this host cannot run the real case (HISS-21: stated reason, never a pass).
func realRaceConfig(t *testing.T, root string) *stageConfig {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the loopback wrapper exists on Linux only: other hosts run no wrapper to opt out of")
	}
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GOFLAGS", "-count=1")
	if _, err := bwrapOnly.LoopbackCommand(t.Context(), []string{"true"}); errors.Is(err, sandbox.ErrUnavailable) {
		t.Skipf("real loopback namespace unavailable on this host: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	cfg, _ := newTestConfig(t, root, false)
	cfg.goos, cfg.run, cfg.lookPath = "linux", util.RunCommand, exec.LookPath
	if available, absent, err := raceDetectorAvailable(t.Context(), cfg); err != nil || !available {
		t.Skipf("race detector unavailable (%s): %v", absent, err)
	}
	return cfg
}

// setGateIdentity hands the fixture tests the uid that owns / and the user namespace, as this
// process sees them.
func setGateIdentity(t *testing.T) {
	t.Helper()
	info, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	userns, nsErr := os.Readlink("/proc/self/ns/user")
	if !ok || nsErr != nil {
		t.Fatalf("read gate identity: stat_t=%v namespace error=%v", ok, nsErr)
	}
	t.Setenv("PRAETOR_FIXTURE_ROOT_UID", strconv.FormatUint(uint64(stat.Uid), 10))
	t.Setenv("PRAETOR_FIXTURE_USERNS", userns)
}

// realRace runs one fixture module's race command for real.
func realRace(t *testing.T, root, module string) (string, error) {
	t.Helper()
	cfg := realRaceConfig(t, root)
	dir := filepath.Join(root, filepath.Dir(filepath.FromSlash(module)))
	return runGoRaceCommand(t.Context(), cfg, goModule{file: module, dir: dir}, dir, 2*time.Minute, raceScopeConfig{})
}

// TestGoRaceRealNamespacePlanted is the planted case on real namespaces: the root-owner fixture
// test fails under the wrapper without a declaration and passes with one, the undeclared sibling
// runs wrapped in both repositories, and an expired declaration fails the module.
func TestGoRaceRealNamespacePlanted(t *testing.T) {
	setGateIdentity(t)
	undeclared := realNetnsRepo(t, "")
	if _, err := realRace(t, undeclared, netnsModule); err == nil || !strings.Contains(err.Error(), "TestRootOwnsTheFilesystemRoot") {
		t.Fatalf("root-owner test passed under the wrapper, the planted failure is gone: %v", err)
	}
	declared := realNetnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, 30)))
	if msg, err := realRace(t, declared, netnsModule); err != nil || !strings.Contains(msg, "exception go-test-netns") {
		t.Fatalf("declared module did not pass in the gate's namespaces: message=%q error=%v", msg, err)
	}
	for _, root := range []string{undeclared, declared} {
		if msg, err := realRace(t, root, "tools/sibling/go.mod"); err != nil || !strings.Contains(msg, "loopback-only network namespace") {
			t.Fatalf("undeclared sibling did not run wrapped: message=%q error=%v", msg, err)
		}
	}
	expired := realNetnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, -1)))
	if _, err := realRace(t, expired, netnsModule); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired declaration did not fail the module: %v", err)
	}
}
