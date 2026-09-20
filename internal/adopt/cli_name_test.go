package adopt

import (
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
