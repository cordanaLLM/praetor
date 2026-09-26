// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// generatedPersona returns the content of the generated persona written to rel.
func generatedPersona(t *testing.T, rel string) string {
	t.Helper()
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		if personas[i].rel == rel {
			return string(personas[i].content)
		}
	}
	t.Fatalf("adoption generates no persona at %s", rel)
	return ""
}

// Positive: the gatekeeper persona adoption writes runs the full gate, gating.RepoRunCommand,
// the command cmd/standardsctl's TestRepoRunCommand_Positive_ParsesAgainstGateRun parses against
// the real flag set. It used to run `gate run --target=.`, which exits on an undefined flag
// (BUG-298).
func TestGatekeeperPersona_Positive_RunsTheGateCommand(t *testing.T) {
	want := "```bash\n" + gating.RepoRunCommand + "\n```"
	if got := generatedPersona(t, gatekeeperFile); !strings.Contains(got, want) {
		t.Fatalf("gatekeeper persona does not run %q:\n%s", want, got)
	}
}

// Negative: no generated persona names the undefined --target flag, and the gatekeeper's
// command is not a dry run. A dry run skips the prefetch, the security scanners and the
// receipt, which is the whole of what the persona's description says it does.
func TestGeneratedPersonas_Negative_NoUndefinedTargetFlag(t *testing.T) {
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		if strings.Contains(string(personas[i].content), "--target") {
			t.Errorf("%s names the undefined --target flag", personas[i].rel)
		}
	}
	if got := generatedPersona(t, gatekeeperFile); strings.Contains(got, gating.RepoRunCommand+" --dry-run") {
		t.Errorf("gatekeeper persona runs a dry run, which mints no receipt:\n%s", got)
	}
}

// Boundary: the gatekeeper persona carries exactly one gate command, so no second hand-written
// copy can drift from gating.RepoRunCommand.
func TestGatekeeperPersona_Boundary_OneGateCommand(t *testing.T) {
	if n := strings.Count(generatedPersona(t, gatekeeperFile), "praetorctl gate"); n != 1 {
		t.Fatalf("gatekeeper persona carries %d gate commands, want 1", n)
	}
}
