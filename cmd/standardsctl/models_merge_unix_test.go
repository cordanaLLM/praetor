//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestModelsMergeReplacementFIFO refuses a replaced FIFO through the merge reader without blocking.
func TestModelsMergeReplacementFIFO(t *testing.T) {
	path := mergeReviewFile(t, "review.json", `{}`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var target map[string]any
		remaining := mergeInputLimit
		done <- readMergeJSON(t.Context(), path, &target, &remaining)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "source must be regular") {
			t.Fatalf("replacement FIFO: %v", err)
		}
	case <-time.After(time.Second):
		// Release a regressed blocking open and join the goroutine before failing.
		writer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := <-done; err == nil || !strings.Contains(err.Error(), "source must be regular") {
			t.Errorf("replacement FIFO: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("opening a replacement FIFO blocked before the descriptor check")
	}
}

// TestModelsMergeFIFODescriptor rejects a FIFO before decoding or charging the shared budget.
func TestModelsMergeFIFODescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var target map[string]any
	remaining := mergeInputLimit
	if err := readMergeJSON(ctx, path, &target, &remaining); err == nil || !strings.Contains(err.Error(), "source must be regular") {
		t.Fatalf("FIFO descriptor: %v", err)
	}
	if target != nil || remaining != mergeInputLimit {
		t.Fatalf("FIFO input retained: %v, remaining %d", target, remaining)
	}
}
