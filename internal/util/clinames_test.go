package util

import (
	"strings"
	"testing"
)

// Positive: generated Makefiles and generated hooks resolve the binary through one expression,
// so make verify-all and the hooks of the same repository cannot pick different names.
func TestShellCLI_Positive_SharesTheMakefileResolution(t *testing.T) {
	line := ShellCLI("audit", "missing")
	if !strings.Contains(line, "$("+ShellCLIResolution+")") {
		t.Fatalf("hook line does not resolve through ShellCLIResolution: %s", line)
	}
	if !strings.Contains(MakefileCLIVariable, "$(shell "+ShellCLIResolution+" || echo "+PraetorCLI+")") {
		t.Fatalf("Makefile variable does not resolve through ShellCLIResolution: %s", MakefileCLIVariable)
	}
	if strings.Index(ShellCLIResolution, PraetorCLI) > strings.Index(ShellCLIResolution, LegacyCLI) {
		t.Fatal("the legacy name is preferred over the current one")
	}
}

// Negative: the rendered command fails closed. It never swallows an exit status and never
// falls back to building from source.
func TestShellCLI_Negative_NoSwallowedStatusOrSourceFallback(t *testing.T) {
	line := ShellCLI("audit", "missing")
	for _, forbidden := range []string{"|| true", "go run", "./cmd/"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("hook line carries %q: %s", forbidden, line)
		}
	}
	if !strings.HasSuffix(line, "echo missing >&2; exit 1; fi") {
		t.Errorf("a missing binary does not block: %s", line)
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
	if got := ShellCLI("", "m"); !strings.Contains(got, "\"$praetor_cli\" ;") {
		t.Errorf("empty arguments render an invalid command: %s", got)
	}
}
