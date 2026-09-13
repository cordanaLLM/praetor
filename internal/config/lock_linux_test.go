//go:build linux

package config

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestValidateLockfileRejectsFIFOWithoutOpening(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, ".standards.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateLockfile(context.Background(), root, &Manifest{Version: 1})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected FIFO refusal before a blocking read, got %v", err)
	}
}
