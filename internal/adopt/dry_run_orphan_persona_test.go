// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// orphanSession is an agent-context session over repo, a dry run or a real one.
func orphanSession(repo string, dryRun bool) *adoptSession {
	return &adoptSession{repoPath: repo, report: &AdoptReport{Steps: []StepOutcome{{Name: "agent-definitions"}, {Name: "agent-harness"}, {Name: "git-ignore"}}},
		opts: AdoptOptions{DryRun: dryRun}}
}

// orphanErrors returns the errors s recorded that name a persona copy without a canonical persona.
func orphanErrors(s *adoptSession) []string {
	var found []string
	for _, err := range s.report.Errors {
		if strings.Contains(err, "projects no canonical persona") {
			found = append(found, err)
		}
	}
	return found
}

// A persona that exists only in a client persona directory fails the real run's verification,
// and the dry run verified nothing, so its preview showed one error fewer than the run (#1079).
// Positive: the dry run records the real run's error, word for word, on the same step. Negative:
// the same copy beside its canonical persona is no error. Boundary: a copy of a persona the run
// itself adds to .agents/agents is not reported by the dry run either.
func TestVerifyAgentContext_3D_DryRunNamesVendorOnlyPersonas(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(customHelperCopy)), customHelperText)
	dry, real := orphanSession(repo, true), orphanSession(repo, false)
	verifyAgentContext(t.Context(), dry, nil)
	verifyAgentContext(t.Context(), real, nil)
	want := orphanErrors(real)
	if len(want) != 1 || !strings.Contains(want[0], customHelperCopy) {
		t.Fatalf("the real run's verification must name %s once, got %q", customHelperCopy, real.report.Errors)
	}
	if got := orphanErrors(dry); !slices.Equal(got, want) {
		t.Errorf("dry run errors %q, want the real run's %q", got, want)
	}
	if step := stepOutcome(t, dry.report, "agent-definitions"); len(step.Errors) != 1 {
		t.Errorf("the dry run must record the error on agent-definitions: %+v", step)
	}

	mustWrite(t, filepath.Join(repo, ".agents", "agents", filepath.Base(customHelperCopy)), customHelperText)
	paired := orphanSession(repo, true)
	verifyAgentContext(t.Context(), paired, nil)
	if got := orphanErrors(paired); len(got) != 0 {
		t.Errorf("a copy beside its canonical persona: %q", got)
	}

	added := t.TempDir()
	mustWrite(t, filepath.Join(added, ".claude", "agents", filepath.Base(generatedPersonas()[0].rel)), customHelperText)
	pending := orphanSession(added, true)
	verifyAgentContext(t.Context(), pending, nil)
	if got := orphanErrors(pending); len(got) != 0 {
		t.Errorf("a copy of a persona the run adds: %q", got)
	}
}
