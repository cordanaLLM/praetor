// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"errors"
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

func TestSessionChildrenWalk(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct{ path, data string }{
		{"10/task/10/children", "11"}, {"11/status", "Name: bwrap\nNSpid:\t11\t1\n"},
		{"11/task/11/children", "12"}, {"12/status", "Name: init\nNSpid:\t12\t2\t1\n"}, {"12/task/12/children", ""},
	} {
		path := filepath.Join(root, item.path)
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

func TestSessionInvocationArguments(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(strconv.FormatBool(container), func(t *testing.T) { checkSessionInvocation(t, container) })
	}
}
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

func TestSessionHandshakeWithFakeSandbox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	fake := filepath.Join(dir, "sandbox")
	script := "#!/bin/sh\nprintf '{\"child-pid\":%s}\\n' \"$$\" >&3\ndd bs=1 count=1 <&4 >/dev/null 2>/dev/null || exit 1\nprintf released\n"
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

func TestSessionStartTickReader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "123")
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
	ok, err = update(os.Getpid()+1, false)
	checkSessionReservation(t, ok, err, false)
	now += 600
	ok, err = update(os.Getpid()+1, false)
	checkSessionReservation(t, ok, err, false)
	now++
	ok, err = update(os.Getpid()+1, false)
	checkSessionReservation(t, ok, err, true)
	entries, err := readSessionRegistry(path)
	if err != nil || entries["42"].Owner != os.Getpid()+1 || entries["42"].Released {
		t.Fatalf("replacement: %v %v", entries, err)
	}
}

func checkSessionReservation(t *testing.T, got bool, err error, want bool) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("reservation: got %v want %v error %v", got, want, err)
	}
}

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
