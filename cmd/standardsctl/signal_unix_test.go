//go:build unix

// Process groups and their signals are Unix-only; on Windows praetorctl's commands stay in the
// console's group and util.TerminateCommandsOnSignal is a no-op.

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalProcessRun selects TestSignalProcessHelper in the re-executed test binary.
const signalProcessRun = "-test.run=^TestSignalProcessHelper$"

// stubGit stands in for git: it holds index.lock and, like git, removes it when interrupted,
// leaving cleaned as evidence. It reports its start in ready, then writes marker only if it
// outlives a one-second pause. A marker present after praetorctl died is an orphaned command.
const stubGit = "#!/bin/sh\ntrap 'rm -f index.lock; touch cleaned; exit 130' INT\n: > index.lock\n" +
	"echo $$ > ready.tmp && mv ready.tmp ready && sleep 1 && touch marker\n"

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
// For "go test" it records its PID into PRAETOR_TEST_READY and waits for cancellation.
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
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_AUTHOR_NAME=praetor-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=praetor-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
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

// TestGateRun_Positive_InterruptCleansUpWorktreeAndBranch asserts that when gate run is
// interrupted by SIGINT or SIGTERM while running race tests in an isolated worktree, the
// cancellation propagates and removeWorktree cleans up both the worktree directory and git
// branch without being blocked by TerminateCommandsOnSignal (BUG-791).
func TestGateRun_Positive_InterruptCleansUpWorktreeAndBranch(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			stubs := t.TempDir()
			repo := newHermeticGateRepo(t)
			readyPath := filepath.Join(t.TempDir(), "ready")

			writeStub := func(name, script string) {
				if err := os.WriteFile(filepath.Join(stubs, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeStub("go", stubGo)
			writeStub("govulncheck", stubScanner)
			writeStub("gosec", stubScanner)
			writeStub("cc", stubScanner)
			writeStub("gcc", stubScanner)

			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			helper := exec.Command(binary, signalProcessRun)
			helper.Env = append(os.Environ(),
				"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PRAETOR_SIGNAL_PROCESS_TEST=gate run --path="+repo,
				"PRAETOR_TEST_READY="+readyPath,
				"PRAETOR_TEST_REPO="+repo,
				"GOCOVERDIR="+t.TempDir(),
			)
			helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var helperOut strings.Builder
			helper.Stdout = &helperOut
			helper.Stderr = &helperOut
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if helper.ProcessState == nil {
					if err := syscall.Kill(-helper.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
						t.Errorf("kill helper group: %v", err)
					}
					var exit *exec.ExitError
					if err := helper.Wait(); err != nil && !errors.As(err, &exit) {
						t.Errorf("reap helper: %v", err)
					}
				}
				if t.Failed() {
					t.Logf("helper output:\n%s", helperOut.String())
				}
			})

			waitForStubStart(t, readyPath)

			wtDir := filepath.Join(repo, ".standards", "worktrees")
			entries, err := os.ReadDir(wtDir)
			if err != nil || len(entries) == 0 {
				t.Fatalf("expected worktree to exist in %s before interrupt: %v", wtDir, err)
			}
			branchOut, err := exec.Command("git", "-C", repo, "branch", "--list", "wt/*").CombinedOutput()
			if err != nil {
				t.Fatalf("git branch --list: %v", err)
			}
			if strings.TrimSpace(string(branchOut)) == "" {
				t.Fatal("expected wt/* branch to exist before interrupt")
			}

			if err := syscall.Kill(-helper.Process.Pid, sig); err != nil {
				t.Fatal(err)
			}

			var exit *exec.ExitError
			if err := helper.Wait(); !errors.As(err, &exit) {
				t.Fatalf("praetorctl must exit on interrupt: %v", err)
			}

			if remaining, err := os.ReadDir(wtDir); err == nil && len(remaining) != 0 {
				t.Fatalf("worktree directory was not cleaned up after %v: found %v", sig, remaining)
			}

			afterBranches, err := exec.Command("git", "-C", repo, "branch", "--list", "wt/*").CombinedOutput()
			if err != nil {
				t.Fatalf("git branch --list: %v", err)
			}
			if trimmed := strings.TrimSpace(string(afterBranches)); trimmed != "" {
				t.Fatalf("git branch was not deleted after %v: %q", sig, trimmed)
			}
		})
	}
}
