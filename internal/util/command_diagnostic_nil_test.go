// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"strings"
	"testing"
)

// A failed command's error carries its trimmed stderr (positive); a command that succeeded stays a
// success even when it printed warnings, as pasta does on a host without IPv6 routes (negative,
// planted: the earlier code returned "%!w(<nil>): ..."); blank stderr returns err unchanged (boundary).
func TestCommandDiagnostic_NilErrorStaysNil(t *testing.T) {
	failed := errors.New("exit status 1")
	if got := CommandDiagnostic(failed, []byte("  bwrap: permission denied \n")); !errors.Is(got, failed) ||
		!strings.Contains(got.Error(), "bwrap: permission denied") {
		t.Fatalf("failed command: %v", got)
	}
	if got := CommandDiagnostic(nil, []byte("No interfaces with usable IPv6 routes\n")); got != nil {
		t.Fatalf("a successful command with stderr output must stay nil: %v", got)
	}
	if got := CommandDiagnostic(failed, []byte("   ")); !errors.Is(got, failed) || got.Error() != failed.Error() {
		t.Fatalf("blank stderr must return err unchanged: %v", got)
	}
}
