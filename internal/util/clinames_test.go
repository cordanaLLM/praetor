package util

import (
	"strings"
	"testing"
)

// Positive: generated Makefiles resolve the binary through ShellCLIResolution,
// preferring the current name over the legacy name.
func TestMakefileCLIVariable_Positive_ResolvesBinary(t *testing.T) {
	if !strings.Contains(MakefileCLIVariable, "$(shell "+ShellCLIResolution+" || echo "+PraetorCLI+")") {
		t.Fatalf("Makefile variable does not resolve through ShellCLIResolution: %s", MakefileCLIVariable)
	}
	if strings.Index(ShellCLIResolution, PraetorCLI) > strings.Index(ShellCLIResolution, LegacyCLI) {
		t.Fatal("the legacy name is preferred over the current one")
	}
}

// Boundary: the Makefile variable keeps its exact historical bytes, which generated Makefiles
// already committed in adopted repositories are compared against.
func TestMakefileCLIVariable_Boundary_BytesUnchanged(t *testing.T) {
	const want = "# Praetor ships one binary under two names; resolve whichever is installed.\n" +
		"PRAETORCTL ?= $(shell command -v praetorctl 2>/dev/null || command -v standardsctl 2>/dev/null || echo praetorctl)\n"
	if MakefileCLIVariable != want {
		t.Fatalf("MakefileCLIVariable changed:\n%q\nwant\n%q", MakefileCLIVariable, want)
	}
}
