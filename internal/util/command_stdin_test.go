package util

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

const stdinHelperRun = "-test.run=^TestCommandStdinHelper$"

func stdinHelper(t *testing.T) (context.Context, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithCommandEnvironment(context.Background(), []string{
		"PRAETOR_COMMAND_STDIN_TEST=echo", "GOCOVERDIR=" + t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, binary
}

func TestWithCommandStdinFeedsExactBytes(t *testing.T) {
	ctx, binary := stdinHelper(t)
	input := []byte(" \t exact\x00\n{\"k\":1}\n")
	ctx, err := WithCommandStdin(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'X' // the context holds a copy, not the caller's slice
	result, err := RunCommandBytes(ctx, "", binary, 128, stdinHelperRun)
	if err != nil || string(result.Stdout) != " \t exact\x00\n{\"k\":1}\n" {
		t.Fatalf("stdin changed: %q %v", result.Stdout, err)
	}
}

func TestWithCommandStdinRejectsNilContextAndOversizedInput(t *testing.T) {
	var absent context.Context
	if _, err := WithCommandStdin(absent, []byte("x")); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithCommandStdin(context.Background(), make([]byte, MaxCommandStdinBytes+1)); err == nil {
		t.Fatal("oversized stdin accepted")
	}
}

func TestWithCommandStdinBoundaries(t *testing.T) {
	if _, err := WithCommandStdin(context.Background(), make([]byte, MaxCommandStdinBytes)); err != nil {
		t.Fatalf("input at the bound refused: %v", err)
	}
	for name, input := range map[string][]byte{"absent": nil, "empty": {}} {
		ctx, binary := stdinHelper(t)
		if input != nil {
			var err error
			if ctx, err = WithCommandStdin(ctx, input); err != nil {
				t.Fatal(err)
			}
		}
		result, err := RunCommandBytes(ctx, "", binary, 128, stdinHelperRun)
		if err != nil || len(result.Stdout) != 0 {
			t.Fatalf("%s stdin is not an empty stream: %q %v", name, result.Stdout, err)
		}
	}
}

func TestCommandStdinHelper(t *testing.T) {
	if os.Getenv("PRAETOR_COMMAND_STDIN_TEST") != "echo" {
		return
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<10))
	if err != nil {
		os.Exit(7)
	}
	if _, err := io.Copy(os.Stdout, bytes.NewReader(data)); err != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
