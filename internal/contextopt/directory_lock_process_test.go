// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cross-process cases of #820 against a real second process: this test binary run again,
// as TestDirectoryLockHolderProcess, holding a directory through LockDirectory until the test
// that started it lets go.

const (
	// lockHolderRole is the argument that makes TestDirectoryLockHolderProcess hold a lock.
	lockHolderRole = "praetor-directory-lock-holder"
	// lockHolderReady starts the line the holder prints, with its process id, once it holds.
	lockHolderReady = "holding"
	// lockHolderLifetime bounds the holder's whole life (HISS-02).
	lockHolderLifetime = 2 * time.Minute
)

// TestDirectoryLockHolderProcess holds a directory for the tests below and does nothing in an
// ordinary run. Started with lockHolderRole and a directory after "--", it locks the
// directory, prints lockHolderReady and its process id, and keeps the lock until its standard
// input closes; the parent's deadline kills it otherwise.
func TestDirectoryLockHolderProcess(t *testing.T) {
	args := flag.Args()
	if len(args) != 2 || args[0] != lockHolderRole {
		return
	}
	root, err := OpenDirectory(t.Context(), args[1])
	if err != nil {
		t.Fatal(err)
	}
	release, err := LockDirectory(t.Context(), root)
	if err != nil {
		t.Fatal(errors.Join(err, root.Close()))
	}
	if _, err := fmt.Printf("%s %d\n", lockHolderReady, os.Getpid()); err != nil {
		t.Error(err)
	}
	_, waitErr := io.Copy(io.Discard, io.LimitReader(os.Stdin, 1<<10))
	if err := errors.Join(waitErr, release(), root.Close()); err != nil {
		t.Fatal(err)
	}
}

// lockHolder is a running TestDirectoryLockHolderProcess.
type lockHolder struct {
	pid    int
	cmd    *exec.Cmd
	stdin  io.Closer
	output <-chan string
	stderr *bytes.Buffer
	cancel context.CancelFunc
	once   sync.Once
}

// startLockHolder starts a second process holding dir and returns once it holds it. The
// test's cleanup stops it.
func startLockHolder(t *testing.T, dir string) *lockHolder {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lockHolderLifetime)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestDirectoryLockHolderProcess$", "-test.count=1", "--", lockHolderRole, dir)
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+t.TempDir())
	holder := &lockHolder{cmd: cmd, stderr: &bytes.Buffer{}, cancel: cancel}
	cmd.Stderr = holder.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	holder.stdin = stdin
	holder.output = readLines(stdout)
	t.Cleanup(func() { holder.stop(t) })
	holder.pid = holder.awaitReady(t)
	return holder
}

// readLines sends the first line r yields, then everything after it, and closes the channel
// at EOF, so the pipe is drained before the process is waited for.
func readLines(r io.Reader) <-chan string {
	lines := make(chan string, 2)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(r)
		first, err := reader.ReadString('\n')
		lines <- first
		if err != nil {
			return
		}
		rest, err := io.ReadAll(reader)
		if err != nil {
			rest = append(rest, []byte(err.Error())...)
		}
		lines <- string(rest)
	}()
	return lines
}

// awaitReady returns the process id the holder prints once it holds the directory.
func (h *lockHolder) awaitReady(t *testing.T) int {
	t.Helper()
	select {
	case line := <-h.output:
		pid, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), lockHolderReady+" "))
		if err != nil || pid <= 0 {
			h.cancel()
			h.stop(t)
			t.Fatalf("lock holder did not report holding the directory: %q", line)
		}
		return pid
	case <-time.After(time.Minute):
		h.cancel()
		h.stop(t)
		t.Fatal("lock holder did not hold the directory within a minute")
		return 0
	}
}

// stop closes the holder's standard input, which makes it release the directory and exit,
// and waits for it; it runs once.
func (h *lockHolder) stop(t *testing.T) {
	t.Helper()
	h.once.Do(func() {
		defer h.cancel()
		closeErr := h.stdin.Close()
		var rest []string
		for line := range h.output {
			rest = append(rest, line)
		}
		if err := errors.Join(closeErr, h.cmd.Wait()); err != nil {
			t.Errorf("lock holder %d: %v\nstdout: %q\nstderr: %s", h.pid, err, rest, h.stderr.String())
		}
	})
}

// Negative: a directory another process holds past the budget fails the writer naming that
// process, where the platform reports it, and the budget.
func TestLockDirectory_Negative_OtherProcessPastBudget(t *testing.T) {
	root, dir := pinnedDirectory(t)
	holder := startLockHolder(t, dir)
	_, err := LockDirectory(budgetContext(t, 200*time.Millisecond), root)
	if err == nil || !strings.Contains(err.Error(), "past the 200ms wait budget") {
		t.Fatalf("lock taken from, or not refused for, another process: %v", err)
	}
	requireHolderNamed(t, err, holder.pid)
	holder.stop(t)
	if err := mustLock(t, budgetContext(t, 0), root)(); err != nil {
		t.Fatalf("lock after the other process released: %v", err)
	}
}

// Positive: a directory another process releases within the budget lets the writer publish.
func TestLockDirectory_Positive_OtherProcessReleasesWithinBudget(t *testing.T) {
	_, dir := pinnedDirectory(t)
	holder := startLockHolder(t, dir)
	done := make(chan error, 1)
	go func() {
		done <- WriteSnapshotIn(budgetContext(t, 20*time.Second), dir, "after-wait", []byte("published\n"), 0o600)
	}()
	select {
	case err := <-done:
		t.Fatalf("writer returned while another process held the directory: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	holder.stop(t)
	if err := <-done; err != nil {
		t.Fatalf("writer after the other process released: %v", err)
	}
	if got := readFileOrEmpty(t, filepath.Join(dir, "after-wait")); got != "published\n" {
		t.Fatalf("writer published %q", got)
	}
}
