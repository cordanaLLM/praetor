package util

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// streamHelperArgs selects the helper mode inside the re-executed test binary.
const streamHelperArgs = "-test.run=^TestCommandBytesHelper$"

func TestRunCommandStreamCopiesStdinToStdout(t *testing.T) {
	ctx, binary := bytesHelper(t, "echo")
	payload := strings.Repeat("stream payload ", 1<<14) // far larger than the stderr cap below
	var out bytes.Buffer
	stderr, err := RunCommandStream(ctx, "", binary, strings.NewReader(payload), &out, 64, streamHelperArgs)
	if err != nil {
		t.Fatal(err, string(stderr))
	}
	if out.String() != payload || string(stderr) != "echoed" {
		t.Fatalf("stream changed: %d bytes out, stderr %q", out.Len(), stderr)
	}
}

func TestRunCommandStreamReportsStdinFailure(t *testing.T) {
	ctx, binary := bytesHelper(t, "echo")
	reader, writer := io.Pipe()
	sentinel := errors.New("producer failed")
	go func() {
		if _, err := writer.Write([]byte("partial")); err != nil {
			return
		}
		writer.CloseWithError(sentinel)
	}()
	_, err := RunCommandStream(ctx, "", binary, reader, io.Discard, 64, streamHelperArgs)
	if !errors.Is(err, sentinel) {
		t.Fatalf("stdin failure not reported: %v", err)
	}
	var absent context.Context
	if _, err := RunCommandStream(absent, "", binary, nil, nil, 64); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestRunCommandStreamBounds(t *testing.T) {
	ctx, binary := bytesHelper(t, "stderr")
	stderr, err := RunCommandStream(ctx, "", binary, nil, nil, 16, streamHelperArgs)
	if err == nil || len(stderr) != 16 {
		t.Fatalf("stderr cap not enforced: %q %v", stderr, err)
	}
	for _, limit := range []int{0, 16<<20 + 1} {
		if _, err := RunCommandStream(ctx, "", binary, nil, nil, limit); err == nil {
			t.Fatalf("invalid stderr cap %d accepted", limit)
		}
	}
	ctx, binary = bytesHelper(t, "boundary")
	var out bytes.Buffer
	if _, err := RunCommandStream(ctx, "", binary, nil, &out, 1, streamHelperArgs); err != nil || out.Len() != 16 {
		t.Fatalf("stdout must not be capped by the stderr bound: %d %v", out.Len(), err)
	}
}
