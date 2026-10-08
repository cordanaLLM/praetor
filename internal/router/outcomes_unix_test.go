//go:build unix

package router

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadOutcomesRefusesFIFOWithoutBlocking(t *testing.T) {
	fifoPath := filepath.Join(t.TempDir(), "test.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skip("mkfifo not supported")
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadOutcomes(context.Background(), fifoPath)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("expected regular file error for FIFO, got: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ReadOutcomes blocked on FIFO")
	}
}

func TestAppendOutcomeRefusesFIFOWithoutBlocking(t *testing.T) {
	fifoPath := filepath.Join(t.TempDir(), "test.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skip("mkfifo not supported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- AppendOutcome(ctx, fifoPath, sampleOutcome("stubs", OutcomeOK))
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error appending to FIFO, got nil")
		}
	case <-ctx.Done():
		t.Fatal("AppendOutcome blocked on FIFO")
	}
}
