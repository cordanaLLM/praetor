//go:build unix

// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTranscriptOpenDoesNotBlockOnReplacementFIFO(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.jsonl")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	result := make(chan error, 1)
	go func() {
		file, err := openStableTranscriptFile(root, "source.jsonl", before)
		if err == nil {
			err = file.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("replacement FIFO accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement FIFO open blocked")
	}
	if file, _, err := openTranscript(path); err == nil {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("FIFO accepted by source reader")
	}
}
