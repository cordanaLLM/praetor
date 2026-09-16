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

// MakefileCLIVariable is the make assignment generated Makefiles use to resolve the binary.
// A recipe calls $(PRAETORCTL) rather than either literal name, so one Makefile works
// whichever name is installed.
const MakefileCLIVariable = "# Praetor ships one binary under two names; resolve whichever is installed.\n" +
	"PRAETORCTL ?= $(shell command -v " + PraetorCLI + " 2>/dev/null || command -v " + LegacyCLI +
	" 2>/dev/null || echo " + PraetorCLI + ")\n"

// MakefileCLI renders one generated make recipe body invoking the resolved binary.
func MakefileCLI(arguments string) string {
	return "\t@$(PRAETORCTL) " + arguments + "\n"
}
