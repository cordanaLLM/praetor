// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/util"
)

// needShell skips a test whose step is a POSIX shell command where no sh is on PATH (a Windows
// runner without Git's usr/bin); the admission logic itself is covered by internal/admission.
func needShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH on %s: %v", runtime.GOOS, err)
	}
}

// Positive: the flags set the policy, the limits and the bounds; the label defaults to the
// command's name.
func TestParseAdmit_Positive_FlagsAndDefaults(t *testing.T) {
	opts, err := parseAdmit([]string{"--memory-high", "8G", "--memory-max", "12G", "--memory-swap-max", "0", "--min-available-gib", "20",
		"--wait", "1m", "--", "make", "verify-all"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.label != "make" || opts.policy.MinAvailableBytes != 20<<30 || opts.limits.MemorySwapMax != "0" || opts.wait != time.Minute ||
		strings.Join(opts.argv, " ") != "make verify-all" {
		t.Fatalf("options %+v", opts)
	}
	// Past `--` a word that looks like a flag of admit belongs to the command.
	opts, err = parseAdmit([]string{"--label", "build", "--", "make", "--wait", "-j4"})
	if err != nil || opts.wait != 6*time.Hour || strings.Join(opts.argv, " ") != "make --wait -j4" {
		t.Fatalf("command flags after --: %+v, %v", opts, err)
	}
}

// Negative: no command, a non-positive bound, a threshold past the bound and a limit systemd
// would misread are each refused before anything runs.
func TestParseAdmit_Negative_Refused(t *testing.T) {
	for _, args := range [][]string{
		{"--label", "x"},
		{"--wait", "0s", "--", "true"},
		{"--min-available-gib", "99999999", "--", "true"},
		{"--memory-max", "1.5G", "--", "true"},
		{"--memory-swap-max", "lots", "--", "true"},
	} {
		if _, err := parseAdmit(args); err == nil {
			t.Errorf("parseAdmit(%q) accepted", args)
		}
	}
}

// Negative (planted): a heavy step asked to start under a threshold the host cannot meet is
// held and then refused, and the command never runs. Where the host has no memory source
// (internal/sentinel memory_other.go, macOS among them) admission admits unjudged by design, so
// there the step runs; the branch follows the same reading admission takes, not the system name.
func TestRunAdmit_Negative_UnderThresholdRefused(t *testing.T) {
	needShell(t)
	admitStubTools(t)
	stats, readErr := admission.HostReader(".")()
	out, err := captureStdout(t, func() error {
		return runAdmit([]string{"--local", "--min-available-gib", "1000000", "--wait", "1s", "--poll", "200ms", "--", "sh", "-c", "echo step-ran"})
	})
	if readErr == nil && stats.RAMTotalBytes == 0 {
		if err != nil || !strings.Contains(out, "step-ran") {
			t.Fatalf("on %s memory is not measurable and the step is admitted unjudged: %v, output %q", runtime.GOOS, err, out)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "still held") || strings.Contains(out, "step-ran") {
		t.Fatalf("an under-threshold step was not refused: %v, output %q", err, out)
	}
}

// Boundary: the step's own exit status comes back as praetorctl's, and a passing step returns nil.
func TestRunAdmit_Boundary_ExitStatusPassesThrough(t *testing.T) {
	needShell(t)
	admitStubTools(t)
	err := runAdmit([]string{"--local", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--wait", "10s", "--",
		"sh", "-c", "exit 3"})
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("exit 3 came back as %v", err)
	}
	if err := runAdmit([]string{"--local", "--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "exit 0"}); err != nil {
		t.Fatalf("a passing step: %v", err)
	}
}

func TestRunAdmit_RemoteHeadroomAndGateStatus(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "adequate", "best", "busy", "down")
	for _, status := range []int{0, 3, 255} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			out, log, code := runAdmitCaptured(t, "--label", "gate-fixture", "--", "sh", "-ec", `
test "$(git rev-parse HEAD)" = "$1"
test "$(git write-tree)" = "$2"
test "$(cat file)" = staged
test ! -e untracked
test "$4" = "argument with 'quotes' and \$literal"
printf 'planted gate stdout %s\n' "$ADMIT_STUB_HOST"
printf 'planted gate stderr\n' >&2
exit "$3"`, "gate", f.head, f.tree, fmt.Sprint(status), "argument with 'quotes' and $literal")
			if code != status || !strings.Contains(out, "planted gate stdout best") {
				t.Fatalf("wanted best executor with exit %d, got %d, stdout %q, stderr %q", status, code, out, log)
			}
			for _, want := range []string{"admitted gate-fixture on best", "busy held: load", "down skipped: probe failed", "planted gate stderr", "planted scope limits"} {
				if !strings.Contains(log, want) {
					t.Errorf("stderr lacks %q: %q", want, log)
				}
			}
			f.assertClean(t)
		})
	}
}

func TestRunAdmit_RemoteTieUsesLoadPerCore(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "tie-small", "tie-large")
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", `printf '%s' "$ADMIT_STUB_HOST"`)
	if code != 0 || out != "tie-large" || !strings.Contains(log, "on tie-large") {
		t.Fatalf("tie result: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

func TestRunAdmit_RemoteRefusesDownAndHeldByName(t *testing.T) {
	for _, name := range []string{"down", "busy", "incomplete", "slow"} {
		t.Run(name, func(t *testing.T) {
			f := newAdmitFixture(t)
			f.inventory(t, name)
			out, log, code := runAdmitCaptured(t, "--wait", "150ms", "--", "sh", "-c", "echo ran")
			if code != 75 || out != "" || !strings.Contains(log, name) || !strings.Contains(log, "no remote executor admitted") {
				t.Fatalf("refusal for %s: %d %q %q", name, code, out, log)
			}
			f.assertClean(t)
		})
	}
}

func TestRunAdmit_RemoteSlowExecutorDoesNotBlockHealthy(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "slow", "best")
	start := time.Now()
	out, log, code := runAdmitCaptured(t, "--wait", "8s", "--", "sh", "-c", `printf '%s' "$ADMIT_STUB_HOST"`)
	if code != 0 || out != "best" || !strings.Contains(log, "slow skipped: probe failed") || time.Since(start) >= 6*time.Second {
		t.Fatalf("slow executor blocked a healthy peer: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

func TestRunAdmit_RemoteTimeoutCleansWorkspace(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "best")
	out, log, code := runAdmitCaptured(t, "--timeout", "150ms", "--", "sh", "-c", "echo started; sleep 15")
	if code != 124 || out != "started\n" {
		t.Fatalf("timeout: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

func TestRunAdmit_RemoteCancellationCleansWorkspace(t *testing.T) {
	f := newAdmitFixture(t)
	f.inventory(t, "best")
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	rootCtx = ctx
	marker := filepath.Join(t.TempDir(), "started")
	started := make(chan bool, 1)
	go func() {
		for range 500 {
			if _, err := os.Stat(marker); err == nil {
				started <- true
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		started <- false
		cancel()
	}()
	start := time.Now()
	out, log, code := runAdmitCaptured(t, "--", "sh", "-c", `: > "$1"; exec sleep 15`, "step", marker)
	if !<-started || code != 1 || !strings.Contains(log, "executor best: SSH run") ||
		!strings.Contains(log, "planted scope stopped") || time.Since(start) >= 8*time.Second {
		t.Fatalf("cancellation: %d %q %q", code, out, log)
	}
	f.assertClean(t)
}

func TestRunAdmit_ExecutorModeIsExplicit(t *testing.T) {
	admitStubTools(t)
	t.Setenv(admitExecutorEnv, "")
	t.Setenv(admitExecutorsEnv, "")
	step := []string{"--min-available-gib", "0", "--min-swap-free-gib", "0", "--max-load", "0", "--", "sh", "-c", "echo planted-local"}
	out, log, code := runAdmitCaptured(t, step...)
	if code != 1 || out != "" || !strings.Contains(log, admitExecutorsEnv) {
		t.Fatalf("missing list: %d %q %q", code, out, log)
	}
	missing := filepath.Join(t.TempDir(), "unreadable.json")
	t.Setenv(admitExecutorsEnv, missing)
	out, log, code = runAdmitCaptured(t, step...)
	if code != 1 || out != "" || !strings.Contains(log, missing) {
		t.Fatalf("unreadable list: %d %q %q", code, out, log)
	}
	assertNamedLocal(t, append([]string{"--local"}, step...))
	t.Setenv(admitExecutorEnv, "local")
	assertNamedLocal(t, step)
}

func assertNamedLocal(t *testing.T, args []string) {
	t.Helper()
	out, log, code := runAdmitCaptured(t, args...)
	if code != 0 || out != "planted-local\n" || !strings.Contains(log, "on local (named)") {
		t.Fatalf("named local: %d %q %q", code, out, log)
	}
}

func runAdmitCaptured(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var captureErr error
	stderr, captureErr = captureStderr(t, func() error {
		var runErr error
		stdout, runErr = captureStdout(t, func() error {
			return dispatchCommandFrom(t.Context(), "admit", args)
		})
		if runErr != nil {
			code = commandExitCode(os.Stderr, runErr)
		}
		return nil
	})
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	return stdout, stderr, code
}

type admitFixture struct {
	root, head, tree string
}

func newAdmitFixture(t *testing.T) admitFixture {
	t.Helper()
	admitStubTools(t)
	for _, tool := range []string{"git", "timeout", "head", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("fake executor requires %s: %v", tool, err)
		}
	}
	t.Setenv(admitExecutorEnv, "")
	root := filepath.Join(t.TempDir(), "remote 'workspace'")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	work := filepath.Join(repo, "sub dir")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string { return admitFixtureGit(t, repo, args...) }
	git("init", "-q", "--template=")
	writeFixtureFile(t, work, "file", "committed\n")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false",
		"-c", "maintenance.auto=false", "commit", "-qm", "fixture")
	head := git("rev-parse", "HEAD")
	writeFixtureFile(t, work, "file", "staged\n")
	git("add", ".")
	tree := git("write-tree")
	writeFixtureFile(t, work, "file", "unstaged\n")
	writeFixtureFile(t, work, "untracked", "untracked\n")
	t.Chdir(work)
	return admitFixture{root: root, head: head, tree: tree}
}

func admitFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, admitFixtureEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatalf("fixture git %v: %v", args, err)
	}
	return out
}

func admitFixtureEnvironment() []string {
	env := util.FilterEnvironment(os.Environ(), func(name string) bool {
		return strings.HasPrefix(strings.ToUpper(name), "GIT_")
	})
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

func admitStubTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake SSH and systemd-run require POSIX execution")
	}
	bin := t.TempDir()
	writeAdmitExecutable(t, bin, "ssh", admitFakeSSH)
	writeAdmitExecutable(t, bin, "systemd-run", admitFakeScope)
	writeAdmitExecutable(t, bin, "systemctl", admitFakeSystemctl)
	t.Setenv("ADMIT_STUB_SCOPES", t.TempDir())
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, err := util.WithCommandEnvironment(t.Context(), admitFixtureEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	previous := rootCtx
	t.Cleanup(func() { rootCtx = previous })
	rootCtx = ctx
}

func writeAdmitExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	file := writeFixtureFile(t, dir, name, body)
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatal(err)
	}
}

func (f admitFixture) inventory(t *testing.T, names ...string) {
	t.Helper()
	var entries []string
	for _, name := range names {
		cores := 8
		if name == "tie-large" {
			cores = 16
		}
		entries = append(entries, fmt.Sprintf(`{"name":%q,"ssh_host":%q,"ssh_user":"fixture","workspace_root":%q,"cores":%d,"memory_gib":64,"class":"cpu-offload"}`,
			name, name, f.root, cores))
	}
	file := writeFixtureFile(t, t.TempDir(), "executors.json", "["+strings.Join(entries, ",")+"]")
	t.Setenv(admitExecutorsEnv, file)
}

func (f admitFixture) assertClean(t *testing.T) {
	t.Helper()
	if files, err := os.ReadDir(f.root); err != nil || len(files) != 0 {
		t.Fatalf("workspace was not cleaned: %v %v", files, err)
	}
}

const admitFakeSSH = `#!/bin/sh
set -eu
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24; do
    if [ "$1" = -- ]; then shift; break; fi
    shift
done
host=$1 command=$2
case "$command" in
    *'/proc/loadavg /proc/meminfo')
        load=1
        available=16777216
        case "$host" in
            adequate) ;;
            best) available=33554432 ;;
            busy) available=50331648; load=11 ;;
            tie-small) load=2 ;;
            tie-large) load=3 ;;
            down) echo 'fixture connection refused' >&2; exit 255 ;;
            incomplete) printf '1 0 0\nMemTotal: 67108864 kB\n'; exit 0 ;;
            slow) exec sleep 15 ;;
            *) exit 99 ;;
        esac
        printf '%s 0 0 1/10 123\nMemTotal: 67108864 kB\nMemAvailable: %s kB\nSwapTotal: 33554432 kB\nSwapFree: 33554432 kB\n' "$load" "$available"
        ;;
    *) export ADMIT_STUB_HOST="$host"; exec sh -c "exec $command" ;;
esac
`

const admitFakeScope = `#!/bin/sh
set -eu
high= max= swap= unit=
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16; do
    case "$1" in
        --property=MemoryHigh=16G) high=yes ;;
        --property=MemoryMax=24G) max=yes ;;
        --property=MemorySwapMax=4G) swap=yes ;;
        --unit=*) unit=${1#--unit=} ;;
        --) shift; break ;;
        true) exit 0 ;;
    esac
    shift
done
test "$high$max$swap" = yesyesyes
printf '%s\n' "$$" > "$ADMIT_STUB_SCOPES/$unit.scope"
echo 'planted scope limits' >&2
exec "$@"
`

const admitFakeSystemctl = `#!/bin/sh
set -eu
test "$1" = --user && test "$2" = stop
pid=$(cat "$ADMIT_STUB_SCOPES/$3")
kill -KILL "-$pid" 2>/dev/null || :
echo 'planted scope stopped' >&2
`
