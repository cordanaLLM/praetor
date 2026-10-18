package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestHelpOutputFramesTribunusAsGraphRouter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	res, err := util.RunCommandBytes(ctx, "", "go", util.MaxCommandOutputBytes, "run", ".", "--help")
	output := append(res.Stdout, res.Stderr...)
	if err != nil {
		t.Fatalf("tribunusctl --help: %v\n%s", err, output)
	}
	if want := []byte("tribunusctl - model data sync for the Tribunus graph router\n"); !bytes.HasPrefix(output, want) {
		t.Fatalf("tribunusctl --help = %q, want banner %q", output, want)
	}
	if old := []byte("Tribunus model catalog"); bytes.Contains(output, old) {
		t.Fatalf("tribunusctl --help retains old framing %q: %q", old, output)
	}
	if !bytes.Contains(output, []byte("\nUsage:\n")) {
		t.Fatalf("tribunusctl --help lost usage section: %q", output)
	}
}
