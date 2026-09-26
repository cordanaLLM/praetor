package adopt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the artefacts one adoption run writes must agree about which binary exists.
// They did not: the README and Makefile named standardsctl while the pre-commit hook
// written in the same run refused to run without praetorctl, so installing what the README
// named broke the hook and installing what the hook named broke make verify-all (#118).
func TestGeneratedArtefacts_Positive_AgreeOnOneBinary(t *testing.T) {
	plan := &VerificationPlan{}
	readme, _, err := readmegovernance.Reconcile("", readmegovernance.State{})
	if err != nil {
		t.Fatalf("rendering README governance: %v", err)
	}
	harness, err := buildAgentHarness("praetor-test", "go", plan)
	if err != nil {
		t.Fatalf("rendering the agent harness: %v", err)
	}
	artefacts := map[string]string{
		"README governance table": readme,
		"Makefile":                buildMakefile(plan),
		"agent harness":           harness,
	}
	// An invocation, not a mention: the Makefile's resolution line names both binaries on
	// purpose, and asserting the string is absent would forbid the fix itself.
	for name, body := range artefacts {
		for _, invocation := range []string{"@" + util.LegacyCLI + " ", "`" + util.LegacyCLI + " ", "\n" + util.LegacyCLI + " "} {
			if strings.Contains(body, invocation) {
				t.Errorf("%s invokes %s (%q); generated artefacts name %s",
					name, util.LegacyCLI, strings.TrimSpace(invocation), util.PraetorCLI)
			}
		}
	}
}

// The Makefile must not name either binary directly: it resolves whichever is installed,
// so a repository adopted while only the legacy name is on PATH still runs its own gates.
func TestGeneratedMakefile_Positive_ResolvesTheBinaryRatherThanNamingIt(t *testing.T) {
	makefile := buildMakefile(&VerificationPlan{})
	if !strings.Contains(makefile, "PRAETORCTL ?=") {
		t.Fatal("generated Makefile does not define the resolution variable")
	}
	for _, literal := range []string{"@" + util.PraetorCLI + " ", "@" + util.LegacyCLI + " "} {
		if strings.Contains(makefile, literal) {
			t.Errorf("generated Makefile hardcodes %q instead of $(PRAETORCTL)", strings.TrimSpace(literal))
		}
	}
	if !strings.Contains(makefile, "$(PRAETORCTL) audit") {
		t.Error("generated Makefile does not invoke the resolved binary")
	}
}

// Negative/boundary: the legacy detectors must keep naming the legacy binary. They match
// Makefiles Praetor wrote before this change; rewriting them would make adoption stop
// recognising its own historical output and silently leave those Makefiles in place.
func TestLegacyMakefileDetection_Negative_StillRecognisesHistoricalOutput(t *testing.T) {
	if !strings.Contains(legacyVerificationStub, util.LegacyCLI) {
		t.Fatal("the legacy stub no longer names the legacy binary, so it can never match")
	}
	if !isLegacyVerificationMakefile(legacyVerificationStub) {
		t.Error("the historical stub is no longer recognised as legacy Praetor output")
	}
	if isLegacyVerificationMakefile(buildMakefile(&VerificationPlan{})) {
		t.Error("current output is misclassified as legacy")
	}
}

func TestLegacyMakefileDetection_Boundary_RejectsUnrelatedMakefiles(t *testing.T) {
	for _, body := range []string{"", "all:\n\techo hi\n", "verify-all:\n\t@make test\n"} {
		if isLegacyVerificationMakefile(body) {
			t.Errorf("unrelated Makefile %q classified as legacy Praetor output", body)
		}
	}
}

// runHookLine runs one generated hook command under shell with PATH restricted to stubDir,
// from workDir, and returns its combined output and exit code. POSIX shells only: the hook
// lines are sh syntax, which lefthook also runs through sh on Windows, but the stubs here are
// shell scripts that Windows cannot execute directly.
func runHookLine(t *testing.T, shell, line, stubDir, workDir string) (string, int) {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stubs required")
	}
	shellPath, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s required", shell)
	}
	cmd := exec.Command(shellPath, "-c", line)
	cmd.Dir = workDir
	cmd.Env = []string{"PATH=" + stubDir}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v", shell, err)
		return "", -1
	}
}

// recordingStub writes a CLI stub that appends its name and arguments to a log and exits code.
func recordingStub(t *testing.T, dir, name, log string, code int) {
	t.Helper()
	writeStub(t, dir, name, "echo \""+name+" $*\" >> '"+log+"'\nexit "+strconv.Itoa(code)+"\n")
}

// Positive: a governed hook resolves the legacy binary when it is the only one installed, as
// the generated Makefile already does, and prefers the current name when both are (BUG-805).
func TestLefthookGovernedCommand_Positive_ResolvesEitherInstalledName(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, util.LegacyCLI, log, 0)
	if out, code := runHookLine(t, "sh", lefthookGovernedCommand("audit"), stubs, work); code != 0 {
		t.Fatalf("legacy-only install failed the hook (%d): %s", code, out)
	}
	recordingStub(t, stubs, util.PraetorCLI, log, 0)
	if out, code := runHookLine(t, "sh", lefthookGovernedCommand("state sync ."), stubs, work); code != 0 {
		t.Fatalf("hook failed with both names installed (%d): %s", code, out)
	}
	if got, want := mustRead(t, log), util.LegacyCLI+" audit\n"+util.PraetorCLI+" state sync .\n"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if out, code := runHookLine(t, "bash", buildFallbackPreCommitScript(), stubs, work); code != 0 {
		t.Fatalf("fallback pre-commit hook failed (%d): %s", code, out)
	}
}

// Negative: the hook fails closed. A failing binary keeps its exit status, and with neither
// name installed the hook blocks with a message naming both.
func TestLefthookGovernedCommand_Negative_FailsClosed(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	out, code := runHookLine(t, "sh", lefthookGovernedCommand("audit"), stubs, work)
	if code != 1 || !strings.Contains(out, util.PraetorCLI) || !strings.Contains(out, util.LegacyCLI) {
		t.Fatalf("missing binaries: exit %d, output %q", code, out)
	}
	if _, code := runHookLine(t, "bash", buildFallbackPreCommitScript(), stubs, work); code != 1 {
		t.Fatalf("fallback hook with no binary exited %d, want 1", code)
	}
	recordingStub(t, stubs, util.PraetorCLI, filepath.Join(work, "calls.log"), 7)
	if _, code := runHookLine(t, "sh", lefthookGovernedCommand("audit"), stubs, work); code != 7 {
		t.Fatalf("a failing audit exited %d, want its own 7", code)
	}
}

// Boundary: a ./cmd/standardsctl directory no longer stands in for an installed binary. The
// go run fallback only ever fired inside Praetor's own checkout, where it hid a missing binary.
func TestLefthookGovernedCommand_Boundary_SourceTreeIsNotABinary(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "cmd", "standardsctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "go", log, 0)
	if _, code := runHookLine(t, "sh", lefthookGovernedCommand("audit"), stubs, work); code != 1 {
		t.Fatalf("source tree without a binary exited %d, want 1", code)
	}
	if fileExists(log) {
		t.Fatalf("the hook fell back to the go toolchain: %s", mustRead(t, log))
	}
	if strings.Contains(buildLefthookYAML(), "go run") {
		t.Fatal("the generated configuration still carries a go run fallback")
	}
}
