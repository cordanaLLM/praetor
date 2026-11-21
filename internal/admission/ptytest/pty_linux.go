// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build linux

package ptytest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ioctl applies a terminal request to a live descriptor using the standard library.
func ioctl[T uint32 | int32 | syscall.Termios](f *os.File, request uintptr, value *T) error {
	// SAFETY: value points to a live uint32, int32 or Termios owned by the caller for the whole synchronous
	// call; the pointer is converted to uintptr inside the Syscall argument list (unsafe.Pointer rule 4), and
	// each request reads or writes exactly that type's size.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), request, uintptr(unsafe.Pointer(value))) // #nosec G103 -- synchronous kernel ioctl; typed live uint32, int32 or Termios buffer.
	if errno != 0 {
		return errno
	}
	return nil
}

// requirePTY skips unavailable kernel facilities and fails unexpected fixture errors.
func requirePTY(t *testing.T, piece string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, missing := range []error{syscall.ENOENT, syscall.ENODEV, syscall.ENOSYS, syscall.ENOTTY, syscall.EOPNOTSUPP, syscall.EPERM, syscall.EACCES} {
		if errors.Is(err, missing) {
			t.Skipf("controlling PTY unavailable: %s: %v", piece, err)
		}
	}
	t.Fatalf("controlling PTY fixture: %s: %v", piece, err)
}

// openPTY opens and unlocks a Linux PTY, retaining its master until the test ends.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	requirePTY(t, "open /dev/ptmx", err)
	closeAfterTest(t, master)
	var unlock, number uint32
	requirePTY(t, "ioctl TIOCSPTLCK", ioctl(master, syscall.TIOCSPTLCK, &unlock))
	requirePTY(t, "ioctl TIOCGPTN", ioctl(master, syscall.TIOCGPTN, &number))
	path := fmt.Sprintf("/dev/pts/%d", number)
	// #nosec G304 -- fixed /dev/pts prefix plus the kernel-returned uint32 PTY number.
	slave, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	requirePTY(t, "open "+path, err)
	closeAfterTest(t, slave)
	return slave
}

// Run re-executes one test as a session leader with a controlling PTY on child fd 0.
func Run(t *testing.T, helper string) {
	t.Helper()
	slave := openPTY(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	//nolint:forbidigo // This test fixture needs Setsid/Setctty; util's runner forces a separate process group.
	// #nosec G204 -- re-executes only os.Executable, with a test name supplied by the two fixture callers.
	cmd := exec.CommandContext(ctx, self, "-test.run=^"+helper+"$")
	cmd.Env = util.InheritedEnvironment(nil)
	cmd.Stdin = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.WaitDelay = util.CommandWaitDelay
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		requirePTY(t, "setsid/TIOCSCTTY", err)
	}
	if err != nil || ctx.Err() != nil {
		t.Fatalf("test under controlling PTY failed or stalled: %v (context %v)\n%s", err, ctx.Err(), out)
	}
	t.Logf("controlling PTY child: %s", out)
}

// Check exercises terminal setup on stdin and /dev/tty in the admitted background group.
func Check(t *testing.T) {
	t.Helper()
	var attrs syscall.Termios
	err := ioctl(os.Stdin, syscall.TCGETS, &attrs)
	if err == nil {
		if setErr := ioctl(os.Stdin, syscall.TCSETS, &attrs); setErr != nil {
			t.Fatal(setErr)
		}
		t.Fatal("admitted child inherited terminal stdin")
	}
	if !errors.Is(err, syscall.ENOTTY) {
		t.Fatalf("stdin TCGETS: %v", err)
	}
	if input, readErr := io.ReadAll(io.LimitReader(os.Stdin, 1)); readErr != nil || len(input) != 0 {
		t.Fatalf("stdin did not return EOF: %q, %v", input, readErr)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	closeAfterTest(t, tty)
	var foreground int32
	if err := ioctl(tty, syscall.TIOCGPGRP, &foreground); err != nil {
		t.Fatal(err)
	}
	if int(foreground) == syscall.Getpgrp() {
		t.Fatal("admitted child is not in a background process group")
	}
	if err := ioctl(tty, syscall.TCGETS, &attrs); err != nil {
		t.Fatal(err)
	}
	if err := ioctl(tty, syscall.TCSETS, &attrs); err != nil {
		t.Fatal(err)
	}
}
