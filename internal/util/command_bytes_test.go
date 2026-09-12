package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

func bytesHelper(t *testing.T, mode string) (context.Context, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Instrumented subprocesses need a private output directory to keep stderr exact.
	ctx, err := WithCommandEnvironment(context.Background(), []string{
		"PRAETOR_COMMAND_BYTES_TEST=" + mode, "GOCOVERDIR=" + t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, binary
}

func TestRunCommandBytesExactAndFailureOutput(t *testing.T) {
	ctx, binary := bytesHelper(t, "exact")
	r, err := RunCommandBytes(ctx, "", binary, 128, "-test.run=^TestCommandBytesHelper$")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Stdout) != " \t exact\n\x00\n" || string(r.Stderr) != "diagnostic\n" {
		t.Fatalf("bytes changed: %q / %q", r.Stdout, r.Stderr)
	}
	ctx, binary = bytesHelper(t, "failure")
	r, err = RunCommandBytes(ctx, "", binary, 128, "-test.run=^TestCommandBytesHelper$")
	if err == nil || string(r.Stdout) != "partial" || string(r.Stderr) != "failure evidence" {
		t.Fatalf("failure lost: %+v %v", r, err)
	}
}

func TestRunCommandBytesCapsAndCancellation(t *testing.T) {
	ctx, binary := bytesHelper(t, "overflow")
	r, err := RunCommandBytes(ctx, "", binary, 16, "-test.run=^TestCommandBytesHelper$")
	if err == nil || len(r.Stdout) != 16 {
		t.Fatalf("overflow accepted or evidence lost: %+v %v", r, err)
	}
	for _, limit := range []int{0, -1, 16<<20 + 1} {
		if _, err := RunCommandBytes(ctx, "", binary, limit); err == nil {
			t.Fatal("invalid cap accepted")
		}
	}
	var absent context.Context
	if _, err := RunCommandBytes(absent, "", binary, 10); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, binary = bytesHelper(t, "delay")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = RunCommandBytes(ctx, "", binary, 128, "-test.run=^TestCommandBytesHelper$")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("timeout failed", err)
	}
}

func TestRunCommandBytesStreamBoundary(t *testing.T) {
	ctx, binary := bytesHelper(t, "stderr")
	r, err := RunCommandBytes(ctx, "", binary, 16, "-test.run=^TestCommandBytesHelper$")
	if err == nil || len(r.Stderr) != 16 {
		t.Fatalf("stderr overflow: %+v %v", r, err)
	}
	ctx, binary = bytesHelper(t, "boundary")
	r, err = RunCommandBytes(ctx, "", binary, 16, "-test.run=^TestCommandBytesHelper$")
	if err != nil || len(r.Stdout) != 16 {
		t.Fatalf("exact boundary: %+v %v", r, err)
	}
}

func TestCommandBytesHelper(t *testing.T) {
	mode := os.Getenv("PRAETOR_COMMAND_BYTES_TEST")
	if mode == "" {
		return
	}
	switch mode {
	case "exact":
		if _, err := fmt.Fprint(os.Stdout, " \t exact\n\x00\n"); err != nil {
			os.Exit(7)
		}
		if _, err := fmt.Fprintln(os.Stderr, "diagnostic"); err != nil {
			os.Exit(7)
		}
	case "failure":
		if _, err := fmt.Fprint(os.Stdout, "partial"); err != nil {
			os.Exit(7)
		}
		if _, err := fmt.Fprint(os.Stderr, "failure evidence"); err != nil {
			os.Exit(7)
		}
		os.Exit(8)
	case "overflow":
		if _, err := fmt.Fprint(os.Stdout, "01234567890123456789"); err != nil {
			os.Exit(7)
		}
	case "stderr":
		if _, err := fmt.Fprint(os.Stderr, "01234567890123456789"); err != nil {
			os.Exit(7)
		}
	case "boundary":
		if _, err := fmt.Fprint(os.Stdout, "0123456789012345"); err != nil {
			os.Exit(7)
		}
	case "delay":
		time.Sleep(3 * time.Second)
	default:
		t.Fatal("unexpected helper mode")
	}
	os.Exit(0)
}

func TestRunCommandBytesCleansDescendants(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process-group cleanup probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	root := t.TempDir()
	_, err := RunCommandBytes(ctx, root, "sh", 1024, "-c", "(sleep 0.2; touch leaked) & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(root + "/leaked"); !os.IsNotExist(err) {
		t.Fatal("descendant survived cancellation")
	}
}
