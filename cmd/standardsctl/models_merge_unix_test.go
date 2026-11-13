//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestModelsMergeReplacementFIFO proves the open itself cannot block before descriptor checks.
func TestModelsMergeReplacementFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		file, err := openMergeInput(path)
		if err == nil {
			err = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// Release a regressed blocking open and join the goroutine before failing.
		writer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("opening a replacement FIFO blocked before the descriptor check")
	}
}

// TestModelsMergeFIFODescriptor rejects the opened FIFO before attempting a content read.
func TestModelsMergeFIFODescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := readMergeFile(ctx, path, mergeFileLimit); !errors.Is(err, util.ErrNotRegularFile) {
		t.Fatalf("FIFO descriptor: %v", err)
	}
}
