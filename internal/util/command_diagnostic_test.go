package util

import (
	"errors"
	"strings"
	"testing"
)

// TestCommandDiagnostic_3D: a failed command's standard error is trimmed and appended with err
// still reachable through errors.Is; blank stderr returns err itself; stderr at the cap is kept
// whole and one byte more is cut and marked.
func TestCommandDiagnostic_3D(t *testing.T) {
	base := errors.New("exit status 128")
	got := CommandDiagnostic(base, []byte("  error: a.txt: unsupported file type\n"))
	if !errors.Is(got, base) || got.Error() != "exit status 128: error: a.txt: unsupported file type" {
		t.Fatalf("CommandDiagnostic = %v, want git's reason appended to the wrapped error", got)
	}
	if got := CommandDiagnostic(base, []byte(" \n\t")); !errors.Is(got, base) || got.Error() != base.Error() {
		t.Fatalf("blank stderr changed the error to %v", got)
	}
	exact := strings.Repeat("e", maxCommandDiagnosticBytes)
	if got := CommandDiagnostic(base, []byte(exact)).Error(); got != base.Error()+": "+exact {
		t.Fatalf("stderr at the cap was not kept whole: %d bytes", len(got))
	}
	over := CommandDiagnostic(base, []byte(exact+"e")).Error()
	if over != base.Error()+": "+exact+"... [truncated]" {
		t.Fatalf("stderr one byte over the cap was not cut and marked: %d bytes", len(over))
	}
}
