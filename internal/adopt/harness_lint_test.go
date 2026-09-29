package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// harnessPillar returns the Universal Harness pillar of rep.
func harnessPillar(t *testing.T, rep *AdoptReport) Pillar {
	t.Helper()
	for _, pillar := range rep.Pillars() {
		if pillar.Name == "Universal Harness" {
			return pillar
		}
	}
	t.Fatal("report carries no Universal Harness pillar")
	return Pillar{}
}

// adoptWithAgents adopts a fresh repository whose AGENTS.md holds existing, and returns the report.
func adoptWithAgents(t *testing.T, name, existing string, dryRun bool) (*AdoptReport, string) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, agentsFile), existing)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", DryRun: dryRun})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return rep, repoPath
}

// Positive: repository text the caveman gate accepts leaves the pillar ready.
func TestAdoptHarnessLint_Positive_CavemanTextKeepsThePillarReady(t *testing.T) {
	rep, _ := adoptWithAgents(t, "terse-agents", "# Repo rules\n- tests before commit\n- small commits\n", false)
	if pillar := harnessPillar(t, rep); pillar.Status != PillarReady {
		t.Fatalf("pillar = %s %v; want ready", pillar.Status, pillar.Warnings)
	}
}

// Negative: an emoji heading the adopter wrote fails the gate compile-context --verify and audit
// apply, so the harness pillar warns with the flagged line instead of claiming a linted harness,
// and the kept text is not rewritten.
func TestAdoptHarnessLint_Negative_FailingTextWarnsThePillar(t *testing.T) {
	existing := "## \U0001F31F GLOBAL PROJECT RULES\n- tests before commit\n"
	rep, repoPath := adoptWithAgents(t, "emoji-agents", existing, false)
	pillar := harnessPillar(t, rep)
	if pillar.Status != PillarWarned || len(pillar.Warnings) != 1 {
		t.Fatalf("pillar = %s %v; want one warning", pillar.Status, pillar.Warnings)
	}
	for _, want := range []string{"AGENTS.md fails the caveman lint", "C4 terminal-noise", "praetorctl caveman check --kind=context AGENTS.md"} {
		if !strings.Contains(pillar.Warnings[0], want) {
			t.Errorf("warning lacks %q: %s", want, pillar.Warnings[0])
		}
	}
	if !strings.HasSuffix(mustRead(t, filepath.Join(repoPath, agentsFile)), existing) {
		t.Error("adoption must keep the repository text as written")
	}
	if rep.Outcome() != OutcomeApplied {
		t.Errorf("a lint finding on kept text warns; outcome = %s", rep.Outcome())
	}
}

// Boundary: a dry run judges the merged text it would write, so the plan warns too while the
// file stays untouched.
func TestAdoptHarnessLint_Boundary_DryRunWarnsWithoutWriting(t *testing.T) {
	existing := "## \U0001F31F GLOBAL PROJECT RULES\n"
	rep, repoPath := adoptWithAgents(t, "emoji-dry-run", existing, true)
	if pillar := harnessPillar(t, rep); pillar.Status != PillarWarned {
		t.Fatalf("pillar = %s; want warned in the plan", pillar.Status)
	}
	if got := mustRead(t, filepath.Join(repoPath, agentsFile)); got != existing {
		t.Errorf("dry run wrote AGENTS.md: %q", got)
	}
}
