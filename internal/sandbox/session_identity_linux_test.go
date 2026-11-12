// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"os"
	"path/filepath"
	"testing"
)

// TestSessionReservationCollision plants a duplicate tick reservation and requires the second live launch to be refused.
func TestSessionReservationCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	ok, err := reserveSessionTick(t.Context(), path, 42, os.Getpid())
	if err != nil || !ok {
		t.Fatalf("first reservation: %v %v", ok, err)
	}
	ok, err = reserveSessionTick(t.Context(), path, 42, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second live launch acquired the same start tick")
	}
}

// TestSessionReservationStaleAndReadError plants a dead owner and malformed registry and requires stale reclamation and invalid data rejection.
func TestSessionReservationStaleAndReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	if err := os.WriteFile(path, []byte(`{"42":{"owner":2147483647,"released":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ok, err := reserveSessionTick(t.Context(), path, 42, os.Getpid())
	if err != nil || !ok {
		t.Fatalf("stale owner: %v %v", ok, err)
	}
	if err := os.WriteFile(path, []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveSessionTick(t.Context(), path, 43, os.Getpid()); err == nil {
		t.Fatal("invalid registry accepted")
	}
}

// TestSessionRegistryHelperErrors exercises load, clock, validation and owner-identity failures.
func TestSessionRegistryHelperErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	clockErr := errors.New("clock failed")
	if _, err := loadSessionRegistry(path, func() (uint64, error) { return 0, clockErr }); !errors.Is(err, clockErr) {
		t.Fatalf("clock error: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"bad":{"owner":1,"released":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionRegistry(path, func() (uint64, error) { return 1, nil }); err == nil {
		t.Fatal("invalid registry key accepted")
	}
	if _, err := mutateSessionRegistry(map[string]sessionReservation{}, 1, -1, false); err == nil {
		t.Fatal("missing owner accepted")
	}
}

// TestSessionReservationLockDeadline holds the registry lock and requires a competing reservation to honor its deadline.
func TestSessionReservationLockDeadline(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	release, busy, err := util.LockPrivateFile(root, "registry.lock", os.O_RDWR|os.O_CREATE, "test")
	if err != nil || busy {
		t.Fatalf("lock: %v %v", busy, err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := reserveSessionTick(ctx, filepath.Join(dir, "registry"), 1, os.Getpid()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock error: %v", err)
	}
}

// TestSessionChildrenWalk plants a process tree with namespace IDs and requires the matching init or an error for a missing match.
func TestSessionChildrenWalk(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct{ path, data string }{
		{"10/task/10/children", "11"}, {"11/status", "Name: bwrap\nNSpid:\t11\t1\n"},
		{"11/task/11/children", "12"}, {"12/status", "Name: init\nNSpid:\t12\t2\t1\n"}, {"12/task/12/children", ""},
	} {
		path := filepath.Join(root, item.path)
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a fake /proc directory needs the owner search bit to hold entries
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(item.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pid, err := findSessionInit(root, 10, 2)
	if err != nil || pid != 12 {
		t.Fatalf("walk: %d %v", pid, err)
	}
	if _, err := findSessionInit(root, 10, 3); err == nil || !strings.Contains(err.Error(), "no NSpid match") {
		t.Fatalf("missing match: %v", err)
	}
}

// TestSessionInvocationArguments exercises direct and container invocations and requires both handshake layouts to be valid.
func TestSessionInvocationArguments(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(strconv.FormatBool(container), func(t *testing.T) { checkSessionInvocation(t, container) })
	}
}

// checkSessionInvocation builds an invocation for the selected mode and requires its FIFO arguments and container PID option.
func checkSessionInvocation(t *testing.T, container bool) {
	t.Helper()
	command := []string{"/test", "--flag"}
	inv := Invocation{Path: "/usr/bin/bwrap", Args: loopbackArguments(command)}
	if container {
		var err error
		inv, err = publishRunner("").publishCommand("/usr/bin/bwrap", command)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, actual, err := sessionInvocation(inv, "/usr/bin/sh", "/info", "/block", "/pid", command)
	if err != nil || actual != container {
		t.Fatalf("mode: %v %v", actual, err)
	}
	for _, v := range []string{"/info", "/block", "--info-fd", "--block-fd"} {
		if !slices.Contains(got.Args, v) {
			t.Errorf("missing %q in %q", v, got.Args)
		}
	}
	checkSessionDescriptors(t, got.Args)
	if slices.Contains(got.Args, "--pid") != container {
		t.Errorf("pid mode: %q", got.Args)
	}
	script := fifoOpener
	if container {
		script = strings.Replace(namespaceRangeScript, `shift 2; exec "$@"`, `shift 2; `+fifoOpener, 1)
	}
	if !slices.Contains(got.Args, script) {
		t.Errorf("missing opener: %q", got.Args)
	}
}

// checkSessionDescriptors verifies the values associated with the FIFO flags.
func checkSessionDescriptors(t *testing.T, args []string) {
	t.Helper()
	for flag, value := range map[string]string{"--info-fd": "3", "--block-fd": "4"} {
		index := slices.Index(args, flag)
		if index < 0 || index+1 >= len(args) || args[index+1] != value {
			t.Errorf("%s must be paired with %s: %q", flag, value, args)
		}
	}
}

// TestSessionLogicalLaunchRetries plants tick collisions and requires a successful retry or failure after sixteen attempts.
func TestSessionLogicalLaunchRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	ctx := t.Context()
	owner := os.Getpid()
	first, err := reserveSessionTick(ctx, path, 42, owner)
	if err != nil || !first {
		t.Fatalf("first launch: %v %v", first, err)
	}
	ticks := []uint64{42, 43}
	count := 0
	err = retrySession(ctx, func() (bool, error) {
		tick := ticks[count]
		count++
		reserved, err := reserveSessionTick(ctx, path, tick, owner)
		return !reserved, err
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil || count != 2 {
		t.Fatalf("second launch: attempts=%d err=%v", count, err)
	}
	count = 0
	err = retrySession(ctx, func() (bool, error) {
		count++
		reserved, err := reserveSessionTick(ctx, path, 42, owner)
		return !reserved, err
	}, func(context.Context, time.Duration) error { return nil })
	if err == nil || count != 16 {
		t.Fatalf("collision bound: attempts=%d err=%v", count, err)
	}
}

// TestSessionHandshakeWithFakeSandbox plants a fake sandbox and requires the FIFO handshake to release it after reservation.
func TestSessionHandshakeWithFakeSandbox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	fake := filepath.Join(dir, "sandbox")
	script := fmt.Sprintf(`#!/bin/sh
umask 077
printf '{"child-pid":%%s}\n' "$$" >&3
tick=$(awk '{print $22}' /proc/$$/stat)
i=0
while ! grep -Fq "\"$tick\":" %q 2>/dev/null; do
  i=$((i+1)); [ "$i" -lt 400 ] || exit 2; sleep 0.01
done
dd bs=1 count=1 <&4 >/dev/null 2>/dev/null || exit 1
printf released
`, filepath.Join(dir, fmt.Sprintf("praetor-testexec-%d.registry", os.Getuid())))
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- the fake sandbox script runs through PATH and needs the owner execute bit
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runSession(ctx, Invocation{Path: fake, Args: []string{"--", "/test"}}, "/bin/sh", []string{"/test"}, nil, &out, io.Discard)
	if err != nil || out.String() != "released" {
		t.Fatalf("handshake output=%q error=%v", out.String(), err)
	}
}

// TestSessionRetryReplaysInput requires a collision retry to receive the complete original input.
func TestSessionRetryReplaysInput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	fake := filepath.Join(dir, "sandbox")
	registry := filepath.Join(dir, fmt.Sprintf("praetor-testexec-%d.registry", os.Getuid()))
	script := fmt.Sprintf(`#!/bin/sh
umask 077
if [ ! -f %q ]; then
  : > %q
  tick=$(awk '{print $22}' /proc/$$/stat)
  printf '{"%%s":{"owner":%%s,"owner_start_tick":%%s,"released":false}}' "$tick" "$$" "$tick" > %q
fi
printf '{"child-pid":%%s}\n' "$$" >&3
dd bs=1 count=1 <&4 >/dev/null 2>/dev/null || exit 1
cat
`, filepath.Join(dir, "first"), filepath.Join(dir, "first"), registry)
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- the fake sandbox must be executable by its owner
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runSession(ctx, Invocation{Path: fake, Args: []string{"--", "/test"}}, "/bin/sh", []string{"/test"}, strings.NewReader("whole input"), &out, io.Discard)
	if err != nil || out.String() != "whole input" {
		t.Fatalf("retry output=%q error=%v", out.String(), err)
	}
}

// TestSessionInputFiles preserves file descriptors.
func TestSessionInputFiles(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if sessionInput(t.Context(), file, make(chan struct{})) != file {
		t.Fatal("file input was replaced")
	}
}

// TestSessionReleaseError requires a failed release to cancel and collect the child.
func TestSessionReleaseError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	childErr := errors.New("child failed")
	done := make(chan error, 1)
	done <- childErr
	cancelled := false
	err = releaseSessionChild(file, done, func() { cancelled = true }, make(chan struct{}))
	if !cancelled || !errors.Is(err, childErr) || !strings.Contains(err.Error(), "release session sandbox") {
		t.Fatalf("cancelled=%v error=%v", cancelled, err)
	}
}

// TestSessionHandshakeError verifies cancellation when the child exits before reporting its identity.
func TestSessionHandshakeError(t *testing.T) {
	files, err := newSessionFiles()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := files.close(); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	childErr := errors.New("child failed")
	done <- childErr
	cancelled := false
	_, collision, err := sessionHandshake(t.Context(), files, false, done, func() { cancelled = true })
	if collision || !cancelled || !errors.Is(err, childErr) {
		t.Fatalf("collision=%v cancelled=%v error=%v", collision, cancelled, err)
	}
}

// TestSessionReadDeadline requires a stalled read to return when its context expires.
func TestSessionReadDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	unblock := make(chan struct{})
	go func() { time.Sleep(100 * time.Millisecond); close(unblock) }()
	_, err := sessionRead(ctx, func() (int, error) { <-unblock; return 0, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled read error: %v", err)
	}
}

// TestSessionStartTickReader plants a stat record with nested parentheses and requires its tick while rejecting a missing record.
func TestSessionStartTickReader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "123")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a fake /proc/<pid> directory needs the owner search bit to hold entries
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	stat := "123 (name ) nested) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 98 0"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0600); err != nil {
		t.Fatal(err)
	}
	tick, err := procStartTick(root, 123)
	if err != nil || tick != 98 {
		t.Fatalf("tick=%d error=%v", tick, err)
	}
	if _, err := procStartTick(root, 124); err == nil {
		t.Fatal("missing stat accepted")
	}
}

// TestSessionReleasedTickHeld plants a released reservation and requires it to remain unavailable through the retention boundary.
func TestSessionReleasedTickHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	ctx := t.Context()
	now := uint64(42)
	clock := func() (uint64, error) { return now, nil }
	update := func(owner int, release bool) (bool, error) {
		return updateSessionRegistryWithClock(ctx, path, 42, owner, release, clock)
	}
	ok, err := update(os.Getpid(), false)
	checkSessionReservation(t, ok, err, true)
	if _, err = update(os.Getpid(), true); err != nil {
		t.Fatal(err)
	}
	ok, err = update(os.Getpid(), false)
	checkSessionReservation(t, ok, err, false)
	now += sessionTickHold
	ok, err = update(os.Getpid(), false)
	checkSessionReservation(t, ok, err, false)
	now++
	ok, err = update(os.Getpid(), false)
	checkSessionReservation(t, ok, err, true)
	entries, err := readSessionRegistry(path)
	if err != nil || entries["42"].Owner != os.Getpid() || entries["42"].Released {
		t.Fatalf("replacement: %v %v", entries, err)
	}
}

// TestSessionLateReservationRefused rejects attempts delayed beyond the safe retention window.
func TestSessionLateReservationRefused(t *testing.T) {
	if sessionTickHold <= sessionMaxReservationAge {
		t.Fatal("retention must exceed the reservation age limit")
	}
	path := filepath.Join(t.TempDir(), "registry")
	tick := uint64(42)
	now := tick + sessionMaxReservationAge
	ok, err := reserveFreshSessionTickWithClock(t.Context(), path, tick, os.Getpid(), func() (uint64, error) { return now, nil })
	if err != nil || ok {
		t.Fatalf("late reservation: %v %v", ok, err)
	}
	now--
	ok, err = reserveFreshSessionTickWithClock(t.Context(), path, tick, os.Getpid(), func() (uint64, error) { return now, nil })
	if err != nil || !ok {
		t.Fatalf("timely reservation: %v %v", ok, err)
	}
}

// checkSessionReservation checks a reservation result and requires the expected outcome without an error.
func checkSessionReservation(t *testing.T, got bool, err error, want bool) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("reservation: got %v want %v error %v", got, want, err)
	}
}

// TestSessionRetryBeyondFourCollisions plants repeated collisions and requires jittered retries through success and the sixteen-attempt limit.
func TestSessionRetryBeyondFourCollisions(t *testing.T) {
	count, waits := 0, 0
	waiter := func(_ context.Context, delay time.Duration) error {
		waits++
		if delay < 10*time.Millisecond || delay > 40*time.Millisecond {
			t.Errorf("delay: %v", delay)
		}
		return nil
	}
	err := retrySession(t.Context(), func() (bool, error) { count++; return count <= 6, nil }, waiter)
	if err != nil || count != 7 || waits != 6 {
		t.Fatalf("attempts=%d waits=%d error=%v", count, waits, err)
	}
	count, waits = 0, 0
	err = retrySession(t.Context(), func() (bool, error) { count++; return true, nil }, waiter)
	if err == nil || !strings.Contains(err.Error(), "collision") || !strings.Contains(err.Error(), "16 attempts") || count != 16 || waits != 15 {
		t.Fatalf("attempts=%d waits=%d error=%v", count, waits, err)
	}
}

// TestSessionReusedOwnerPruned rejects an old reservation whose live PID has a different start tick.
func TestSessionReusedOwnerPruned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	owner := os.Getpid()
	start, err := procStartTick("/proc", owner)
	if err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"42":{"owner":%d,"owner_start_tick":%d,"released":false}}`, owner, start+1)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	ok, err := reserveSessionTick(t.Context(), path, 42, owner)
	if err != nil || !ok {
		t.Fatalf("reused owner was retained: %v %v", ok, err)
	}
}

// TestSessionPrivateTempDirectory rejects a pre-planted link in the temporary root.
func TestSessionPrivateTempDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", root)
	private := filepath.Join(root, fmt.Sprintf("praetor-testexec-%d", os.Getuid()))
	if err := os.Symlink(t.TempDir(), private); err != nil {
		t.Fatal(err)
	}
	files, err := newSessionFiles()
	if err == nil {
		if closeErr := files.close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("pre-planted private directory symlink accepted")
	}
	if !strings.Contains(err.Error(), "private session directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSessionLiveOwnerRetained preserves an old reservation when its owner identity still matches.
func TestSessionLiveOwnerRetained(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	owner := os.Getpid()
	start, err := procStartTick("/proc", owner)
	if err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"42":{"owner":%d,"owner_start_tick":%d,"released":false}}`, owner, start)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	ok, err := reserveSessionTick(t.Context(), path, 42, owner)
	if err != nil || ok {
		t.Fatalf("live owner not retained: %v %v", ok, err)
	}
}

// TestSessionRegistryWriteLimit preserves the previous registry when the encoded limit is exceeded.
func TestSessionRegistryWriteLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	entry := sessionReservation{Owner: 1}
	prefix := `{"":` + `{"owner":1,"owner_start_tick":0,"released":false}}`
	key := strings.Repeat("1", 65536-len(prefix))
	exact := map[string]sessionReservation{key: entry}
	if err := writeSessionRegistry(path, exact); err != nil {
		t.Fatal(err)
	}
	if _, err := readSessionRegistry(path); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionRegistry(path, map[string]sessionReservation{key + "1": entry}); err == nil {
		t.Fatal("oversized registry accepted")
	}
	got, err := readSessionRegistry(path)
	if err != nil || len(got) != 1 || got[key] != entry {
		t.Fatalf("previous registry lost: %v", err)
	}
}

// TestSessionStreamingInput starts the child before supplying input and keeps the pipe open until then.
func TestSessionStreamingInput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	fake := filepath.Join(dir, "sandbox")
	script := fmt.Sprintf(`#!/bin/sh
: > %q
printf '{"child-pid":%%s}\n' "$$" >&3
dd bs=1 count=1 <&4 >/dev/null 2>/dev/null || exit 1
cat
`, filepath.Join(dir, "started"))
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- fake sandbox requires owner execution
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { closeSessionTestPipe(t, reader, writer) })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	var out strings.Builder
	go func() {
		done <- runSession(ctx, Invocation{Path: fake, Args: []string{"--", "/test"}}, "/bin/sh", []string{"/test"}, reader, &out, io.Discard)
	}()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); err != nil {
		cancel()
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err == nil {
			t.Error("expected cancellation")
		}
		t.Fatal("child did not start before input")
	}
	if _, err := writer.Write([]byte("stream")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || out.String() != "stream" {
		t.Fatalf("output=%q error=%v", out.String(), err)
	}
}

// TestSessionRegistryWriteDeadline preserves the old registry after a stalled replacement expires.
func TestSessionRegistryWriteDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry")
	old := map[string]sessionReservation{"1": {Owner: 1}}
	if err := writeSessionRegistry(path, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	entered, unblock, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- writeSessionRegistryWithContext(ctx, path, map[string]sessionReservation{"2": {Owner: 2}}, func() { close(entered); <-unblock })
	}()
	<-entered
	cancel()
	close(unblock)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("write error: %v", err)
	}
	got, err := readSessionRegistry(path)
	if err != nil || len(got) != 1 || got["1"] != old["1"] {
		t.Fatalf("previous registry lost: %v %v", got, err)
	}
}

// closeSessionTestPipe closes both ends of the streaming fixture.
func closeSessionTestPipe(t *testing.T, reader *io.PipeReader, writer *io.PipeWriter) {
	t.Helper()
	if err := reader.Close(); err != nil {
		t.Error(err)
	}
	if err := writer.Close(); err != nil {
		t.Error(err)
	}
}

// TestSessionInputWaitsForRelease checks that a cancelled attempt never consumes its input.
func TestSessionInputWaitsForRelease(t *testing.T) {
	input := strings.NewReader("data")
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	gated := sessionInput(ctx, input, release)
	done := make(chan error, 1)
	go func() { _, err := gated.Read(make([]byte, 4)); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || input.Len() != 4 {
		t.Fatalf("cancelled read: %v, remaining %d", err, input.Len())
	}
	close(release)
	buf := make([]byte, 4)
	if n, err := sessionInput(t.Context(), input, release).Read(buf); err != nil || n != 4 || string(buf) != "data" {
		t.Fatalf("released read: %d %q %v", n, buf, err)
	}
}
