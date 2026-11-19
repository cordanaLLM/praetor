// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package admission

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

type helperReady struct{ ready chan struct{} }

func (w helperReady) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "helper holds") {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

// TestAdmitSlots_ProcessHelper re-executes only the planted slot holder, with inherited Git variables stripped.
func TestAdmitSlots_ProcessHelper(t *testing.T) {
	if os.Getenv("PRAETOR_ADMIT_TEST_HELPER") != "1" {
		return
	}
	ctx := admitContext(t)
	slots := Slots{Dir: os.Getenv("PRAETOR_ADMIT_TEST_DIR"), MaxHeavy: 1, Poll: time.Millisecond}
	lease, err := slots.Acquire(ctx, "child-helper", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLease(t, lease)
	if _, err := fmt.Fprintln(os.Stdout, "helper holds"); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := io.ReadFull(os.Stdin, data[:]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}

func TestAdmitSlots_TwoProcessesShareSlot(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 1)
	childCtx, executable := helperCommand(t, ctx, slots.Dir)
	reader, writer := io.Pipe()
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	ready := helperReady{make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		_, err := util.RunCommandStream(childCtx, "", executable, reader, ready, 4096, "-test.run=^TestAdmitSlots_ProcessHelper$")
		done <- err
	}()
	waitHelperReady(t, ctx, ready, done)
	short, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	lease, err := slots.Acquire(short, "parent", io.Discard)
	if lease != nil {
		closeLease(t, lease)
		t.Fatal("parent ignored child's slot")
	}
	if err == nil || !strings.Contains(err.Error(), "child-helper") {
		t.Fatalf("parent refusal: %v", err)
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	waitHelperEnd(t, ctx, done)
	lease, err = slots.Acquire(ctx, "parent-after-child", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	closeLease(t, lease)
}

func helperCommand(t *testing.T, ctx context.Context, dir string) (context.Context, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	environment := util.FilterEnvironment(util.InheritedEnvironment([]string{"PRAETOR_ADMIT_TEST_HELPER=1", "PRAETOR_ADMIT_TEST_DIR=" + dir}), func(name string) bool {
		return strings.HasPrefix(name, "GIT_")
	})
	childCtx, err := util.WithCommandEnvironment(ctx, environment)
	if err != nil {
		t.Fatal(err)
	}
	return childCtx, executable
}

func waitHelperReady(t *testing.T, ctx context.Context, ready helperReady, done <-chan error) {
	t.Helper()
	select {
	case <-ready.ready:
	case err := <-done:
		t.Fatalf("helper ended before holding: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitHelperEnd(t *testing.T, ctx context.Context, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
