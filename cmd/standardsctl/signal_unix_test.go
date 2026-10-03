//go:build unix

// Process groups and their signals are Unix-only; on Windows praetorctl's commands stay in the
// console's group and util.TerminateCommandsOnSignal is a no-op.

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// signalProcessRun selects TestSignalProcessHelper in the re-executed test binary.
const signalProcessRun = "-test.run=^TestSignalProcessHelper$"

// stubGit stands in for git: it holds index.lock and, like git, removes it when interrupted,
// leaving cleaned as evidence. It reports its start in ready, then writes marker only if it
// outlives a one-second pause. A marker present after praetorctl died is an orphaned command.
const stubGit = "#!/bin/sh\ntrap 'rm -f index.lock; touch cleaned; exit 130' INT\n: > index.lock\n" +
	"echo $$ > ready.tmp && mv ready.tmp ready && sleep 1 && touch marker\n"

var catchHangupOnce sync.Once

// catchHangupForExec installs a parent-side SIGHUP handler so subprocesses spawned via exec
// inherit the default signal disposition (SIG_DFL) rather than SIG_IGN when the test runner
// itself was invoked under nohup.
func catchHangupForExec() {
	catchHangupOnce.Do(func() {
		signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP)
	})
}

// TestSignalProcessHelper is the child of the signal tests: it runs main with the arguments
// in PRAETOR_SIGNAL_PROCESS_TEST, as the praetorctl binary does.
func TestSignalProcessHelper(t *testing.T) {
	arguments := os.Getenv("PRAETOR_SIGNAL_PROCESS_TEST")
	if arguments == "" {
		return
	}
	os.Args = append([]string{"praetorctl"}, strings.Fields(arguments)...)
	main()
	os.Exit(0)
}

// A terminal's Ctrl-C reaches praetorctl but not the command it runs, which sits in a
// process group of its own. main must pass the interrupt on, so git removes its lock the way
// it did in praetorctl's own group, and must not leave the command running.
func TestMain_Positive_InterruptReachesRunningCommand(t *testing.T) {
	stubs, repo := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(stubs, "git"), []byte(stubGit), 0o700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(binary, signalProcessRun)
	helper.Env = append(os.Environ(),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRAETOR_SIGNAL_PROCESS_TEST=ci filter --dir "+repo, "GOCOVERDIR="+t.TempDir())
	// Its own group stands in for a shell's foreground job, so the group signal below
	// reaches praetorctl and not the test runner.
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if helper.ProcessState != nil {
			return
		}
		if err := syscall.Kill(-helper.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill helper group: %v", err)
		}
		var exit *exec.ExitError
		if err := helper.Wait(); err != nil && !errors.As(err, &exit) {
			t.Errorf("reap helper: %v", err)
		}
	})
	ready := waitForStubStart(t, filepath.Join(repo, "ready"))
	if err := syscall.Kill(-helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := helper.Wait(); !errors.As(err, &exit) {
		t.Fatalf("praetorctl survived the interrupt: %v", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Fatalf("praetorctl did not end as interrupted: %v", exit)
	}
	if _, err := os.Stat(filepath.Join(repo, "cleaned")); err != nil {
		t.Fatalf("the git command never received the interrupt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "index.lock")); !os.IsNotExist(err) {
		t.Fatalf("the interrupted git command left its lock behind: %v", err)
	}
	time.Sleep(time.Until(ready.Add(2 * time.Second)))
	if _, err := os.Stat(filepath.Join(repo, "marker")); !os.IsNotExist(err) {
		t.Fatalf("the git command outlived the interrupted praetorctl: %v", err)
	}
}

// waitForStubStart polls for path within a bound generous enough for a re-executed race
// binary.
func waitForStubStart(t *testing.T, path string) time.Time {
	t.Helper()
	const attempts = 600
	for range attempts {
		if _, err := os.Stat(path); err == nil {
			return time.Now()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the stub git never started: %s is absent", path)
	return time.Time{}
}

// stubGo stands in for the Go toolchain during gate run subprocess signal tests.
// For "go test" it records its PID into PRAETOR_TEST_READY and waits for cancellation. With
// PRAETOR_TEST_STUBBORN set it ignores every signal and fails two seconds later instead, which
// holds the cancelled run inside the command's grace.
const stubGo = `#!/bin/sh
case "$1" in
	env)
		case "$2" in
			CGO_ENABLED) echo "1" ;;
			CC) echo "cc" ;;
			*) echo "" ;;
		esac
		;;
	mod)
		exit 0
		;;
	list)
		echo "$PRAETOR_TEST_REPO"
		;;
	test)
		if [ -n "$PRAETOR_TEST_STUBBORN" ]; then
			trap '' TERM INT HUP
			echo $$ > "$PRAETOR_TEST_READY"
			sleep 2
			exit 1
		fi
		echo $$ > "$PRAETOR_TEST_READY"
		trap 'exit 143' TERM INT
		while true; do
			sleep 1
		done
		;;
	*)
		exit 0
		;;
esac
`

const stubScanner = "#!/bin/sh\nexit 0\n"

// newHermeticGateRepo creates an isolated git repository with valid lockfiles, manifest,
// module definition and gosec config, committed to the main branch so describeTree finds it clean.
func newHermeticGateRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, string(out))
		}
	}
	runGit("init", "-q", "-b", "main")
	writeFile := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("README.md", "# fixture\n")
	writeFile(".standards.yaml", "version: 1\nrepository:\n  owner: example\n  name: demo\nprofiles:\n  - none\n")
	writeFile(".standards.lock", "{\"version\": 1}\n")
	writeFile("go.mod", "module example.invalid/fixture\n\ngo 1.27\n")
	writeFile(".gosec.json", "{}\n")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "fixture")
	return dir
}

// gateRunHelper is a praetorctl `gate run` subprocess held inside its race stage.
type gateRunHelper struct {
	cmd   *exec.Cmd
	repo  string
	ready string // holds the pid of the stub `go test` once the race stage runs it
	out   strings.Builder
	done  chan struct{}
	err   error // how cmd ended; read only after done is closed
}

// startGateRunHelper starts `gate run` against a hermetic repository with stubbed tools, as the
// leader of its own process group the way a shell starts a foreground job, and returns once the
// race stage's stub `go test` runs in the isolated worktree. ignoreHangup starts it with SIGHUP
// ignored through an empty shell trap, the disposition nohup hands the program it execs. extraEnv
// is added to the helper's environment.
func startGateRunHelper(t *testing.T, ignoreHangup bool, extraEnv ...string) *gateRunHelper {
	t.Helper()
	stubs := t.TempDir()
	for name, script := range map[string]string{
		"go": stubGo, "govulncheck": stubScanner, "gosec": stubScanner, "cc": stubScanner, "gcc": stubScanner,
	} {
		if err := os.WriteFile(filepath.Join(stubs, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !ignoreHangup {
		catchHangupForExec()
	}
	h := &gateRunHelper{repo: newHermeticGateRepo(t), ready: filepath.Join(t.TempDir(), "ready"), done: make(chan struct{})}
	h.cmd = exec.Command(binary, signalProcessRun)
	if ignoreHangup {
		h.cmd = exec.Command("sh", "-c", `trap '' HUP; exec "$0" "$@"`, binary, signalProcessRun)
	}
	h.cmd.Env = append(os.Environ(),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRAETOR_SIGNAL_PROCESS_TEST=gate run --path="+h.repo,
		"PRAETOR_TEST_READY="+h.ready, "PRAETOR_TEST_REPO="+h.repo, "GOCOVERDIR="+t.TempDir())
	h.cmd.Env = append(h.cmd.Env, extraEnv...)
	h.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h.cmd.Stdout, h.cmd.Stderr = &h.out, &h.out
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		h.err = h.cmd.Wait()
		close(h.done)
	}()
	t.Cleanup(func() { h.reap(t) })
	waitForStubStart(t, h.ready)
	return h
}

// reap kills the helper's group if it is still running, waits for it, and logs its output
// when the test failed.
func (h *gateRunHelper) reap(t *testing.T) {
	select {
	case <-h.done:
	default:
		if err := syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill helper group: %v", err)
		}
		<-h.done
	}
	if t.Failed() {
		t.Logf("helper output:\n%s", h.out.String())
	}
}

// signalGroup sends sig to the helper's whole group, as a terminal does to its foreground job.
func (h *gateRunHelper) signalGroup(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(-h.cmd.Process.Pid, sig); err != nil {
		t.Fatal(err)
	}
}

// awaitExit waits, within a bound, for the helper to exit and asserts the shell's 128+sig status,
// which tells an interrupted gate run from a rejected one (exit 1).
func (h *gateRunHelper) awaitExit(t *testing.T, sig syscall.Signal) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(2 * time.Minute):
		t.Fatalf("praetorctl never exited after %v", sig)
	}
	var exit *exec.ExitError
	if !errors.As(h.err, &exit) {
		t.Fatalf("praetorctl must exit non-zero after %v: %v", sig, h.err)
	}
	if want := 128 + int(sig); exit.ExitCode() != want {
		t.Fatalf("praetorctl exited %d after %v, want %d", exit.ExitCode(), sig, want)
	}
}

// isolatedWorktrees lists the worktree directories and wt/* branches gate run left in the repository.
func (h *gateRunHelper) isolatedWorktrees(t *testing.T) (dirs []os.DirEntry, branches string) {
	t.Helper()
	dirs, err := os.ReadDir(filepath.Join(h.repo, ".standards", "worktrees"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", h.repo, "branch", "--list", "wt/*").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	return dirs, strings.TrimSpace(string(out))
}

// requireIsolatedWorktree fails unless the race stage's worktree and branch exist.
func (h *gateRunHelper) requireIsolatedWorktree(t *testing.T, when string) {
	t.Helper()
	if dirs, branches := h.isolatedWorktrees(t); len(dirs) == 0 || branches == "" {
		t.Fatalf("expected the isolated worktree and its wt/* branch %s: dirs %v, branches %q", when, dirs, branches)
	}
}

// requireCleanedUp fails unless gate run removed its isolated worktree and branch.
func (h *gateRunHelper) requireCleanedUp(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if dirs, branches := h.isolatedWorktrees(t); len(dirs) != 0 || branches != "" {
		t.Fatalf("gate run left its isolated worktree after %v: dirs %v, branches %q", sig, dirs, branches)
	}
}

// A terminal's Ctrl-C, kill's default and a hangup (terminal closed, ssh dropped) each cancel a
// running gate run, reach the race stage's command group, and still let it remove its isolated
// worktree and branch: util.TerminateCommandsOnSignal's registry lock would block that cleanup,
// and a SIGHUP left to its default action killed praetorctl before it (BUG-791).
func TestGateRun_Positive_TerminatingSignalCleansUpWorktreeAndBranch(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			h := startGateRunHelper(t, false)
			h.requireIsolatedWorktree(t, "before the signal")
			h.signalGroup(t, sig)
			h.awaitExit(t, sig)
			h.requireCleanedUp(t, sig)
		})
	}
}

// Closing a terminal delivers SIGHUP twice: the shell forwards it to its jobs, and the kernel
// sends it again when the shell exits. The second hangup arrives while the cancelled gate run
// still waits for its race stage, and must not end praetorctl before it removes its isolated
// worktree and branch.
func TestGateRun_Positive_RepeatedHangupStillCleansUp(t *testing.T) {
	h := startGateRunHelper(t, false, "PRAETOR_TEST_STUBBORN=1")
	h.signalGroup(t, syscall.SIGHUP)
	time.Sleep(500 * time.Millisecond) // the first hangup is handled well within this
	h.signalGroup(t, syscall.SIGHUP)
	h.awaitExit(t, syscall.SIGHUP)
	h.requireCleanedUp(t, syscall.SIGHUP)
}

// A gate run started with SIGHUP ignored (nohup, a background job) keeps ignoring it: the run,
// its race-stage command and its worktree survive the hangup, and a later Ctrl-C still cleans up.
func TestGateRun_Negative_IgnoredHangupKeepsTheRunGoing(t *testing.T) {
	h := startGateRunHelper(t, true)
	h.signalGroup(t, syscall.SIGHUP)
	select {
	case <-h.done:
		t.Fatalf("an ignored SIGHUP ended gate run: %v", h.err)
	case <-time.After(1500 * time.Millisecond):
	}
	pid, err := os.ReadFile(h.ready)
	if err != nil {
		t.Fatal(err)
	}
	stub, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(stub, 0); err != nil {
		t.Fatalf("an ignored SIGHUP stopped the race stage's go test: %v", err)
	}
	h.requireIsolatedWorktree(t, "after an ignored SIGHUP")
	h.signalGroup(t, syscall.SIGINT)
	h.awaitExit(t, syscall.SIGINT)
	h.requireCleanedUp(t, syscall.SIGINT)
}
