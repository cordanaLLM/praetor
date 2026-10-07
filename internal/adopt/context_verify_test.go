package adopt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// customHelperCopy is a subagent an adopter wrote by hand into a client persona directory: a file
// compile-context --verify reads as compiled output that projects no canonical persona.
const customHelperCopy = ".claude/agents/custom-helper.md"

// customHelperText is that subagent's text, terse enough for the caveman gate once it moves to
// the canonical persona directory.
const customHelperText = "---\nname: custom-helper\ndescription: fixture\n---\n# Custom helper\n\n- review diffs\n"

// stepOutcome returns the outcome rep recorded for the step name.
func stepOutcome(t *testing.T, rep *AdoptReport, name string) StepOutcome {
	t.Helper()
	i := rep.stepNamed(name)
	if i < 0 {
		t.Fatalf("no step %q in %+v", name, rep.Steps)
	}
	return rep.Steps[i]
}

// assertHarnessPillarsClean fails when a verification finding another step owns reaches the
// agent-harness pillars.
func assertHarnessPillarsClean(t *testing.T, rep *AdoptReport) {
	t.Helper()
	if harness := stepOutcome(t, rep, "agent-harness"); harness.Status != StepCompleted || len(harness.Errors) != 0 {
		t.Errorf("agent-harness = %+v, want completed with no error", harness)
	}
	for _, name := range []string{"Universal Harness", "AI Context Sync"} {
		if pillar := pillarNamed(t, rep, name); pillar.Status == PillarFailed {
			t.Errorf("%s pillar failed on a finding it does not own: %v", name, pillar.Errors)
		}
	}
}

// verifyOwner names the step that writes what a failure rejects. Positive: an evidence ignore
// failure is git-ignore's, and a persona copy failure as compile-context --verify returns it, or
// canonical persona prose, is agent-definitions', through any wrapping. Negative: a vendor file,
// register block or AGENTS.md failure is agent-harness's. Boundary: the owner follows the marker
// compiler.ErrAgentSurface, not the text, so a persona error built without it falls to
// agent-harness; and every owner is a step of the chain.
func TestVerifyOwner(t *testing.T) {
	cases := map[string]struct {
		failure error
		want    string
	}{
		"evidence ignore rule":  {fmt.Errorf("context verification failed: %w in /repo", compiler.ErrEvidenceNotIgnored), "git-ignore"},
		"persona copy":          {fmt.Errorf("wrapped: %w", markedSurface(t)), "agent-definitions"},
		"canonical persona":     {fmt.Errorf("context verification failed: %w: x.md", compiler.ErrAgentTextProse), "agent-definitions"},
		"AGENTS.md prose":       {fmt.Errorf("context verification failed: %w", compiler.ErrContextProse), "agent-harness"},
		"nested AGENTS.md":      {fmt.Errorf("context verification failed: %w: a/AGENTS.md", compiler.ErrNestedContextProse), "agent-harness"},
		"register block":        {fmt.Errorf("context verification failed: %w", compiler.ErrRegisterBlockOutOfSync), "agent-harness"},
		"vendor file":           {errors.New("context verification failed: CLAUDE.md out of sync"), "agent-harness"},
		"unmarked persona text": {fmt.Errorf("%w: .claude/agents/x.md", compiler.ErrAgentProjectionDrift), "agent-harness"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := verifyOwner(tc.failure); got != tc.want {
				t.Fatalf("verifyOwner(%v) = %q, want %q", tc.failure, got, tc.want)
			}
		})
	}
	known := map[string]bool{}
	for _, step := range adoptSteps() {
		known[step.name] = true
	}
	for _, owner := range []string{"git-ignore", "agent-definitions", "agent-harness"} {
		if !known[owner] {
			t.Errorf("owner %q is no step of the chain", owner)
		}
	}
}

// markedSurface returns the persona failure compile-context --verify returns for a persona
// directory holding a file with no canonical persona, marked compiler.ErrAgentSurface.
func markedSurface(t *testing.T) error {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, filepath.FromSlash(customHelperCopy)), customHelperText)
	err := compiler.VerifyCompiledContext(t.Context(), io.Discard, compiler.NewTranspiler(), filepath.Join(root, agentsFile), root)
	for _, failure := range verifyFailures(err) {
		if errors.Is(failure, compiler.ErrAgentSurface) {
			return failure
		}
	}
	t.Fatalf("no failure marked ErrAgentSurface in %v", err)
	return nil
}

// Negative and its remedy: compile-context --verify reads every file in a persona directory as
// compiled output, so a subagent the adopter wrote into .claude/agents fails the run after the
// chain. The error says --verify rejects the repository's agent context, never that adoption
// wrote the file; it lands on agent-definitions, which writes that directory, and leaves the
// harness pillars alone; the file is never rewritten or removed. Moved into .agents/agents, the
// same subagent is a canonical persona, projected to every client, and the run is applied.
func TestAdopt_Negative_ClientPersonaDirFileWithoutCanonicalPersona(t *testing.T) {
	repoPath := newTestRepo(t, "custom-subagent")
	custom := filepath.Join(repoPath, filepath.FromSlash(customHelperCopy))
	mustWrite(t, custom, customHelperText)
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if rep.Outcome() != OutcomeIncomplete || len(rep.Errors) != 1 {
		t.Fatalf("outcome = %s, errors = %v; want one verification error", rep.Outcome(), rep.Errors)
	}
	for _, want := range []string{verifyRejection + ": ", customHelperCopy, "projects no canonical persona"} {
		if !strings.Contains(rep.Errors[0], want) {
			t.Errorf("error lacks %q: %s", want, rep.Errors[0])
		}
	}
	if strings.Contains(rep.Errors[0], "adoption wrote") {
		t.Errorf("error blames adoption for a file the repository wrote: %s", rep.Errors[0])
	}
	if defs := stepOutcome(t, rep, "agent-definitions"); defs.Status != StepFailed || len(defs.Errors) != 1 {
		t.Errorf("agent-definitions = %+v, want failed with the verification error", defs)
	}
	assertHarnessPillarsClean(t, rep)
	if got := mustRead(t, custom); got != customHelperText {
		t.Fatalf("adoption rewrote the repository's subagent: %q", got)
	}
	if err := os.Rename(custom, filepath.Join(repoPath, filepath.FromSlash(compiler.CanonicalAgentsRel), "custom-helper.md")); err != nil {
		t.Fatal(err)
	}
	rep, err = Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil || rep.Outcome() != OutcomeApplied {
		t.Fatalf("after the move: err = %v, outcome = %s, errors = %v; want applied", err, rep.Outcome(), rep.Errors)
	}
	if got := mustRead(t, custom); got != customHelperText {
		t.Errorf("the canonical subagent is not projected back into %s: %q", customHelperCopy, got)
	}
	assertContextVerifies(t, repoPath)
}

// nestedContextProse is a nested AGENTS.md an adopter wrote in prose.
const nestedContextProse = "# Service\n\nSearch for an existing implementation before adding one. Grep the repository for " +
	"the capability and extend the code that is already there. Two implementations of one behavior are a " +
	"defect: they drift, and the second one stops matching the first.\n"

// Boundary (#311): compile-context --verify after the chain rejects a tracked nested AGENTS.md
// the repository wrote in prose, and adoption keeps that text as written, as it keeps a persona
// or skill in prose: the run is applied with a warning naming the file, never an error, and the
// file is left untouched.
func TestAdopt_Boundary_NestedContextProseIsWarned(t *testing.T) {
	repoPath := newTestRepo(t, "nested-prose")
	nested := filepath.Join(repoPath, "service", agentsFile)
	mustWrite(t, nested, nestedContextProse)
	testsupport.RunFixtureGit(t, repoPath, []string{"add", "--", "service/" + agentsFile})
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if rep.Outcome() != OutcomeApplied || len(rep.Errors) != 0 {
		t.Fatalf("outcome = %s, errors = %v; want applied with no error", rep.Outcome(), rep.Errors)
	}
	warnings := strings.Join(rep.Warnings, "\n")
	for _, want := range []string{compiler.ErrNestedContextProse.Error(), filepath.Join("service", agentsFile),
		"adoption keeps the repository's text as written"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q: %v", want, rep.Warnings)
		}
	}
	if got := mustRead(t, nested); got != nestedContextProse {
		t.Fatalf("adoption rewrote the repository's nested AGENTS.md: %q", got)
	}
}

// Boundary: a manifest that declines agent-harness, or agent-definitions, leaves part of the
// agent context to the repository, so the verification after the chain does not run. Each case
// leaves a tree compile-context --verify rejects (no AGENTS.md at all, or a subagent in
// .claude/agents with no canonical persona) and the run is still applied, with no error; each
// fails once its skip is removed from verifyAgentContext.
func TestAdopt_Boundary_DeclinedAgentStepSkipsTheVerification(t *testing.T) {
	for _, declined := range []string{"agent-harness", "agent-definitions"} {
		t.Run(declined, func(t *testing.T) {
			repoPath := newTestRepo(t, "declined-"+declined)
			mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: ["+declined+"]\n")
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(customHelperCopy)), customHelperText)
			rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
			if err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			if rep.Outcome() != OutcomeApplied || len(rep.Errors) != 0 {
				t.Fatalf("outcome = %s, errors = %v; want applied with no error", rep.Outcome(), rep.Errors)
			}
			if step := stepOutcome(t, rep, declined); step.Status != StepDeclined {
				t.Fatalf("%s = %+v, want declined", declined, step)
			}
			source := filepath.Join(repoPath, agentsFile)
			if err := compiler.VerifyCompiledContext(t.Context(), io.Discard, compiler.NewTranspiler(), source, repoPath); err == nil {
				t.Fatal("the tree verifies, so this case proves no skip")
			}
		})
	}
}
