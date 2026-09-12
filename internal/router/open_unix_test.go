//go:build unix

package router

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRoutingInputRejectsReplacementFIFOWithoutBlocking(t *testing.T) {
	path := routingInput(t, routingFixture)
	inspected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := readInspectedRoutingFile(ctx, path, inspected)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("replacement FIFO accepted")
		}
	case <-ctx.Done():
		// Release a regressed blocking reader before the fixture is removed.
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			if err := writer.Close(); err != nil {
				t.Error(err)
			}
		}
		t.Fatal("FIFO replacement blocked the read")
	}
	if _, err := LoadRoutingConfigContext(ctx, path); err == nil {
		t.Fatal("FIFO accepted by public config loader")
	}
	if _, err := LoadUsageSnapshot(ctx, path); err == nil {
		t.Fatal("FIFO accepted by public usage loader")
	}
}
