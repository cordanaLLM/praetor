//go:build unix

package dogfood

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRepairReportFIFORejectedWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := LoadRepairReport(ctx, path); err == nil {
		t.Fatal("FIFO accepted")
	}
	if ctx.Err() != nil {
		t.Fatal("FIFO blocked until deadline")
	}
}
