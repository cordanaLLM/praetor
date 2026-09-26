package util

// Praetor ships one binary under two names. PraetorCLI is the current name, reported by the
// version subcommand and used by every generated artefact. LegacyCLI is the name older
// releases installed, kept only as a resolution fallback so a repository adopted while one
// is on PATH does not fail against the other.
//
// These live here because generated artefacts previously disagreed: adoption wrote a README
// and a Makefile naming standardsctl while the pre-commit hook it wrote in the same run
// preferred praetorctl and refused to run without it, so installing what the README named
// broke the hook and installing what the hook named broke make verify-all (#118).
const (
	PraetorCLI = "praetorctl"
	LegacyCLI  = "standardsctl"
)

// ShellCLIResolution is the POSIX shell command list that prints the path of whichever name is
// installed, PraetorCLI first, and fails when neither is. Generated Makefiles and generated
// hooks both resolve through it, so a repository's make targets and its hooks never pick
// different binaries.
const ShellCLIResolution = "command -v " + PraetorCLI + " 2>/dev/null || command -v " + LegacyCLI + " 2>/dev/null"

// MakefileCLIVariable is the make assignment generated Makefiles use to resolve the binary.
// A recipe calls $(PRAETORCTL) rather than either literal name, so one Makefile works
// whichever name is installed.
const MakefileCLIVariable = "# Praetor ships one binary under two names; resolve whichever is installed.\n" +
	"PRAETORCTL ?= $(shell " + ShellCLIResolution + " || echo " + PraetorCLI + ")\n"

// MakefileCLI renders one generated make recipe body invoking the resolved binary.
func MakefileCLI(arguments string) string {
	return "\t@$(PRAETORCTL) " + arguments + "\n"
}

// ShellCLI renders one POSIX shell command that runs arguments with the resolved binary and,
// when neither name is installed, prints missing to standard error and exits 1. It fails
// closed on both counts: a failing command keeps its exit status, and a missing binary blocks.
// It never builds from source, because an adopted repository has no ./cmd/standardsctl: that
// fallback only ever ran in Praetor's own checkout and hid a missing binary there (BUG-805).
func ShellCLI(arguments, missing string) string {
	return "if praetor_cli=$(" + ShellCLIResolution + "); then \"$praetor_cli\" " + arguments +
		"; else echo " + missing + " >&2; exit 1; fi"
}
