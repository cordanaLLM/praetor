package adopt

import (
	"context"
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

// TestVerifyAgentContext_ReadOnlyFailures checks adoption's report for independent target and
// canonical lint failures, including the target's path and owning step.
func TestVerifyAgentContext_ReadOnlyFailures(t *testing.T) {
	cases := []struct {
		name, target string
		lint         bool
	}{
		{name: "clean"},
		{name: "lint only", lint: true},
		{name: "missing only", target: "missing"},
		{name: "drift only", target: "drift"},
		{name: "missing and lint", target: "missing", lint: true},
		{name: "drift and lint", target: "drift", lint: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := contextVerificationFixture(t, tc.lint)
			plantReadOnlyTarget(t, root, tc.target)
			assertReadOnlyReport(t, runContextVerification(t, root), tc.target != "")
		})
	}
}

// plantReadOnlyTarget leaves the projection synchronized, removes it, or plants drift.
func plantReadOnlyTarget(t *testing.T, root, state string) {
	t.Helper()
	target := filepath.Join(root, compiler.ReadOnlyFile)
	switch state {
	case "missing":
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
	case "drift":
		mustWrite(t, target, "drifted\n")
	}
}

// assertReadOnlyReport requires target failures on their owner without repeating canonical lint.
func assertReadOnlyReport(t *testing.T, rep *AdoptReport, targetFailure bool) {
	t.Helper()
	step := stepOutcome(t, rep, "agent-harness")
	if len(step.Warnings) != 0 {
		t.Errorf("canonical lint warning repeated: %v", step.Warnings)
	}
	if !targetFailure {
		if len(rep.Errors) != 0 {
			t.Fatalf("lint alone must stay accepted: %v", rep.Errors)
		}
		return
	}
	if len(rep.Errors) != 1 || len(step.Errors) != 1 || step.Status != StepFailed {
		t.Fatalf("target failure missing from report/owner: errors=%v step=%+v", rep.Errors, step)
	}
	if !strings.Contains(step.Errors[0], "context verification failed: target "+compiler.ReadOnlyFile) {
		t.Errorf("target path or verifier prefix lost: %s", step.Errors[0])
	}
}

// contextVerificationFixture prepares synchronized outputs without rejecting canonical prose.
func contextVerificationFixture(t *testing.T, lint bool) string {
	t.Helper()
	root := newTestRepo(t, "context-verification")
	content := "# Policy\n\n- review diffs\n"
	if lint {
		content += strings.Repeat(nestedContextProse, 8)
	}
	source := filepath.Join(root, agentsFile)
	mustWrite(t, source, content)
	mustWrite(t, filepath.Join(root, ".gitignore"), "/.workingdir/\n")
	if _, err := compiler.CompileVendorTargets(t.Context(), io.Discard, compiler.NewTranspiler(), source, root); err != nil {
		t.Fatal(err)
	}
	return root
}

// runContextVerification replays adoption's final verification through its prepared-step seam.
func runContextVerification(t *testing.T, root string) *AdoptReport {
	t.Helper()
	rep, err := adoptOrStep(t, AdoptOptions{Path: root}, false, func(ctx context.Context, s *adoptSession) error {
		for _, name := range []string{"agent-harness", "agent-definitions", "git-ignore"} {
			s.report.recordStep(name, StepCompleted, s.report.mark())
		}
		verifyAgentContext(ctx, s, nil)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestVerifyAgentContext_PersonaDriftAndLint keeps persona drift on its step and prose warned.
func TestVerifyAgentContext_PersonaDriftAndLint(t *testing.T) {
	root := contextVerificationFixture(t, false)
	canonical := compiler.CanonicalAgentsRel + "/custom-helper.md"
	mustWrite(t, filepath.Join(root, filepath.FromSlash(canonical)), customHelperText+strings.Repeat(nestedContextProse, 8))
	if _, err := compiler.CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, filepath.FromSlash(customHelperCopy)), "drifted\n")
	rep := runContextVerification(t, root)
	step := stepOutcome(t, rep, "agent-definitions")
	if len(rep.Errors) != 1 || len(step.Errors) != 1 || step.Status != StepFailed {
		t.Fatalf("persona drift owner: errors=%v step=%+v", rep.Errors, step)
	}
	for _, want := range []string{"agent persona verification failed", customHelperCopy} {
		if !strings.Contains(step.Errors[0], want) {
			t.Errorf("persona drift lacks %q: %s", want, step.Errors[0])
		}
	}
	if len(step.Warnings) != 1 || !strings.Contains(step.Warnings[0], canonical) ||
		!strings.Contains(step.Warnings[0], compiler.ErrAgentTextProse.Error()) {
		t.Errorf("persona lint must stay warned on its owner: %+v", step)
	}
	assertHarnessPillarsClean(t, rep)
}

// TestVerifyAgentContext_CappedPersonaDrift keeps a read-only rejection after eighty
// persona copy failures exhaust expansion beside canonical lint.
func TestVerifyAgentContext_CappedPersonaDrift(t *testing.T) {
	root := contextVerificationFixture(t, true)
	plantPersonaDrifts(t, root)
	plantReadOnlyTarget(t, root, "drift")
	rep := runContextVerification(t, root)
	step := stepOutcome(t, rep, "agent-harness")
	if step.Status != StepFailed || len(step.Errors) == 0 || len(step.Warnings) != 0 {
		t.Fatalf("read-only rejection lost: report errors=%d step=%+v", len(rep.Errors), step)
	}
	if !strings.Contains(strings.Join(step.Errors, "\n"), compiler.ReadOnlyFile) {
		t.Errorf("read-only target absent: %v", step.Errors)
	}
	t.Logf("read-only rejection: %v", step.Errors)
}

// plantPersonaDrifts writes twenty valid canonical personas and eighty drifted selected copies.
func plantPersonaDrifts(t *testing.T, root string) {
	t.Helper()
	dirs, _, err := compiler.SelectPersonaDirs(t.Context(), root)
	if err != nil || len(dirs) != 4 {
		t.Fatalf("persona selection: dirs=%v err=%v", dirs, err)
	}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("helper-%02d.md", i)
		mustWrite(t, filepath.Join(root, compiler.CanonicalAgentsRel, name), customHelperText)
		for j := 0; j < len(dirs) && j < 4; j++ {
			mustWrite(t, filepath.Join(root, dirs[j], name), "drifted\n")
		}
	}
}

// TestVerifyAgentContext_ExpansionBound checks report status, errors and warnings at the
// expansion limit. The root join consumes one step, leaving maxVerifyFailures-1 opened causes.
func TestVerifyAgentContext_ExpansionBound(t *testing.T) {
	drift := errors.New("target AGENTS.readonly.md is out of sync")
	cases := []struct {
		name     string
		causes   []error
		stopping bool
		want     string
	}{
		{"at bound lint last", repeatedVerifyLint(maxVerifyFailures - 1), false, ""},
		{"one over lint only", repeatedVerifyLint(maxVerifyFailures), true, "1 pending failures"},
		{"one over target last", append(repeatedVerifyLint(maxVerifyFailures-1), drift), true, compiler.ReadOnlyFile},
		{"canonical remainder", append(repeatedVerifyLint(maxVerifyFailures-1), drift, compiler.ErrContextProse), true, compiler.ReadOnlyFile},
		{"persona remainder", append(repeatedVerifyLint(maxVerifyFailures-1), drift, compiler.ErrAgentTextProse), true, compiler.ReadOnlyFile},
		{"nested remainder", append(repeatedVerifyLint(maxVerifyFailures-1), drift, compiler.ErrNestedContextProse), true, compiler.ReadOnlyFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := verificationFailureReport(errors.Join(tc.causes...))
			assertVerificationBoundReport(t, rep, "agent-harness", tc.stopping, tc.want)
		})
	}
}

// TestVerifyAgentContext_OpenedWarnings preserves ordinary persona and nested prose warnings.
func TestVerifyAgentContext_OpenedWarnings(t *testing.T) {
	cases := []struct {
		cause error
		owner string
	}{
		{compiler.ErrAgentTextProse, "agent-definitions"},
		{compiler.ErrNestedContextProse, "agent-harness"},
	}
	for _, tc := range cases {
		t.Run(tc.owner, func(t *testing.T) {
			rep := verificationFailureReport(errors.Join(tc.cause))
			step := stepOutcome(t, rep, tc.owner)
			if step.Status != StepCompleted || len(rep.Errors) != 0 || len(step.Errors) != 0 {
				t.Fatalf("opened prose stopped owner: report=%v step=%+v", rep.Errors, step)
			}
			if len(rep.Warnings) != 1 || len(step.Warnings) != 1 {
				t.Fatalf("opened prose warning lost: report=%v step=%+v", rep.Warnings, step)
			}
		})
	}
}

// repeatedVerifyLint supplies bounded canonical lint causes, whose opened forms are skipped.
func repeatedVerifyLint(count int) []error {
	causes := make([]error, count)
	for i := 0; i < count && i < maxVerifyFailures*2; i++ {
		causes[i] = compiler.ErrContextProse
	}
	return causes
}

// verificationFailureReport uses the production classifier with completed owner steps.
func verificationFailureReport(err error) *AdoptReport {
	rep := &AdoptReport{Steps: []StepOutcome{
		{Name: "agent-harness", Status: StepCompleted},
		{Name: "agent-definitions", Status: StepCompleted},
		{Name: "git-ignore", Status: StepCompleted},
	}}
	recordContextVerification(rep, err)
	return rep
}

// assertVerificationBoundReport requires unopened aggregates to stop their owner without warnings.
func assertVerificationBoundReport(t *testing.T, rep *AdoptReport, owner string, stopping bool, want string) {
	t.Helper()
	step := stepOutcome(t, rep, owner)
	if len(rep.Warnings) != 0 || len(step.Warnings) != 0 {
		t.Fatalf("unopened lint downgraded: report=%v step=%+v", rep.Warnings, step)
	}
	if !stopping {
		assertOpenedLintReport(t, rep, step)
		return
	}
	if step.Status != StepFailed || len(step.Errors) != 1 || len(rep.Errors) != 1 {
		t.Fatalf("unopened remainder not stopping: report=%v step=%+v", rep.Errors, step)
	}
	for _, text := range []string{verifyRejection, "bound reached", want} {
		if !strings.Contains(step.Errors[0], text) {
			t.Errorf("stopping error lacks %q: %s", text, step.Errors[0])
		}
	}
}

// assertOpenedLintReport keeps canonical lint accepted when expansion completed at the bound.
func assertOpenedLintReport(t *testing.T, rep *AdoptReport, step StepOutcome) {
	t.Helper()
	if step.Status != StepCompleted || len(rep.Errors) != 0 || len(step.Errors) != 0 {
		t.Fatalf("opened lint stopped step: report=%v step=%+v", rep.Errors, step)
	}
}

// TestVerifyAgentContext_DepthBound checks that depth exhaustion cannot suppress or warn drift.
func TestVerifyAgentContext_DepthBound(t *testing.T) {
	drift := errors.New("target AGENTS.readonly.md is out of sync")
	cases := []struct {
		name     string
		depth    int
		cause    error
		stopping bool
	}{
		{"at bound lint", maxVerifyDepth, errors.Join(compiler.ErrContextProse), false},
		{"over bound lint", maxVerifyDepth + 1, errors.Join(compiler.ErrContextProse), true},
		{"over bound canonical", maxVerifyDepth + 1, errors.Join(drift, compiler.ErrContextProse), true},
		{"over bound persona", maxVerifyDepth + 1, errors.Join(drift, compiler.ErrAgentTextProse), true},
		{"over bound nested", maxVerifyDepth + 1, errors.Join(drift, compiler.ErrNestedContextProse), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := tc.cause
			for i := 0; i < tc.depth && i <= maxVerifyDepth; i++ {
				wrapped = fmt.Errorf("file: %w", wrapped)
			}
			rep := verificationFailureReport(wrapped)
			assertVerificationBoundReport(t, rep, "agent-harness", tc.stopping, "file:")
		})
	}
}

// TestVerifyAgentContext_SurfaceRemainder retains wrapper diagnostics and surface ownership.
func TestVerifyAgentContext_SurfaceRemainder(t *testing.T) {
	causes := append(repeatedVerifyLint(maxVerifyFailures-1), errors.New("target drift"), compiler.ErrAgentTextProse)
	wrapped := verifyFailurePart{prefix: "persona copies: ", suffix: " [surface]", cause: errors.Join(causes...), surface: true}
	rep := verificationFailureReport(wrapped)
	assertVerificationBoundReport(t, rep, "agent-definitions", true, "persona copies:")
	step := stepOutcome(t, rep, "agent-definitions")
	for _, text := range []string{"target drift", "[surface]", "2 pending failures"} {
		if !strings.Contains(step.Errors[0], text) {
			t.Errorf("surface diagnostic lacks %q: %v", text, step.Errors)
		}
	}
	got := verifyFailures(wrapped)
	if !errors.Is(got[len(got)-1], compiler.ErrAgentSurface) {
		t.Fatal("surface marker lost from remainder")
	}
}

// TestVerifyAgentContext_PendingFrontier keeps late targets when nested joins leave more
// pending entries than one expansion budget can visit.
func TestVerifyAgentContext_PendingFrontier(t *testing.T) {
	inner := errors.Join(repeatedVerifyLint(maxVerifyFailures)...)
	causes := append([]error{inner}, repeatedVerifyLint(maxVerifyFailures-2)...)
	causes = append(causes, errors.New("target AGENTS.readonly.md is out of sync"))
	rep := verificationFailureReport(errors.Join(causes...))
	assertVerificationBoundReport(t, rep, "agent-harness", true, compiler.ReadOnlyFile)
}

// TestVerifyAgentContext_OpaqueWrapper keeps an aggregate whose wrapper cannot be split safely.
func TestVerifyAgentContext_OpaqueWrapper(t *testing.T) {
	err := opaqueVerifyWrapper{cause: errors.Join(errors.New("target drift"), compiler.ErrContextProse)}
	rep := verificationFailureReport(err)
	assertVerificationBoundReport(t, rep, "agent-harness", true, "opaque wrapper")
}

// opaqueVerifyWrapper exposes causes without including them in its diagnostic text.
type opaqueVerifyWrapper struct{ cause error }

// Error supplies context that substituting child text must not discard.
func (opaqueVerifyWrapper) Error() string { return "opaque wrapper" }

// Unwrap exposes the hidden aggregate to the bounded walk.
func (e opaqueVerifyWrapper) Unwrap() error { return e.cause }

// TestVerifyFailures_WrappedJoin separates each cause through two wrappers, retaining context.
func TestVerifyFailures_WrappedJoin(t *testing.T) {
	drift := errors.New("target missing")
	lint := compiler.ErrContextProse
	joined := fmt.Errorf("step: %w", fmt.Errorf("AGENTS.readonly.md: %w", errors.Join(drift, lint)))
	got := verifyFailures(joined)
	if len(got) != 2 {
		t.Fatalf("joined causes = %v, want two", got)
	}
	for i, cause := range []error{drift, lint} {
		if !errors.Is(got[i], cause) || errors.Is(got[i], []error{lint, drift}[i]) {
			t.Errorf("cause %d includes sibling or lost identity: %v", i, got[i])
		}
		if want := "step: AGENTS.readonly.md: " + cause.Error(); got[i].Error() != want {
			t.Errorf("cause %d = %q, want %q", i, got[i], want)
		}
	}
}

// TestVerifyFailures_SingleAndNil preserves single causes, their wrapper context, and nil.
func TestVerifyFailures_SingleAndNil(t *testing.T) {
	drift := errors.New("target missing")
	single := fmt.Errorf("file: %w", drift)
	if got := verifyFailures(single); len(got) != 1 || !errors.Is(got[0], single) || got[0].Error() != single.Error() {
		t.Errorf("single wrapped cause changed: %v", got)
	}
	if got := verifyFailures(drift); len(got) != 1 || !errors.Is(got[0], drift) {
		t.Errorf("single unwrapped cause changed: %v", got)
	}
	if got := verifyFailures(nil); len(got) != 0 {
		t.Errorf("nil changed: %v", got)
	}
}

// TestVerifyFailures_Overflow keeps excess causes joined, both directly and beneath a wrapper.
func TestVerifyFailures_Overflow(t *testing.T) {
	causes := make([]error, maxVerifyFailures+2)
	for i := 0; i < len(causes) && i < maxVerifyFailures+2; i++ {
		causes[i] = fmt.Errorf("cause %d", i)
	}
	for _, err := range []error{errors.Join(causes...), fmt.Errorf("file: %w", errors.Join(causes...))} {
		got := verifyFailures(err)
		if len(got) > maxVerifyFailures || len(got) < 2 {
			t.Fatalf("bounded failure count = %d", len(got))
		}
		for i := len(got) - 1; i < len(causes) && i < maxVerifyFailures+2; i++ {
			if !errors.Is(got[len(got)-1], causes[i]) {
				t.Errorf("overflow cause %d lost: %v", i, got[len(got)-1])
			}
		}
	}
}

// TestVerifyFailures_Depth limits single-wrapper inspection and preserves an unopened failure.
func TestVerifyFailures_Depth(t *testing.T) {
	wrapped := &countedVerifyWrapper{}
	got := verifyFailures(wrapped)
	if len(got) != 1 || !errors.Is(got[0], wrapped) || wrapped.reads > maxVerifyDepth+1 {
		t.Errorf("depth bound: reads=%d failures=%v", wrapped.reads, got)
	}
}

// TestVerifyFailures_DepthBoundary opens a join at the depth limit and preserves one beyond it.
func TestVerifyFailures_DepthBoundary(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	wrapped := errors.Join(first, second)
	for i := 0; i < maxVerifyDepth; i++ {
		wrapped = fmt.Errorf("layer: %w", wrapped)
	}
	if got := verifyFailures(wrapped); len(got) != 2 || !errors.Is(got[0], first) || !errors.Is(got[1], second) {
		t.Fatalf("join at depth limit not opened: %v", got)
	}
	beyond := fmt.Errorf("beyond: %w", wrapped)
	if got := verifyFailures(beyond); len(got) != 1 || !errors.Is(got[0], beyond) ||
		got[0].bound != "depth" || !strings.Contains(got[0].Error(), beyond.Error()) {
		t.Fatalf("join beyond depth limit not marked unopened: %v", got)
	}
}

// countedVerifyWrapper terminates beyond the inspection budget, exposing an unbounded walk.
type countedVerifyWrapper struct{ reads int }

// Error supplies fixed text without traversing the synthetic wrapper chain.
func (*countedVerifyWrapper) Error() string { return "deep wrapper" }

// Unwrap records inspection and terminates even if the production depth guard is removed.
func (e *countedVerifyWrapper) Unwrap() error {
	e.reads++
	if e.reads > maxVerifyDepth+2 {
		return nil
	}
	return e
}

// customHelperCopy is a subagent an adopter wrote by hand into a client persona directory: a file
// compile-context --verify reads as compiled output that projects no canonical persona.
const customHelperCopy = ".claude/agents/custom-helper.md"

// customHelperText is that subagent's text, terse enough for the caveman gate once it moves to
// the canonical persona directory.
const customHelperText = "---\nname: custom-helper\ndescription: fixture\nmodel: pro\n---\n# Custom helper\n\n- review diffs\n"

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
	wantProjected := strings.Replace(customHelperText, "model: pro", "model: sonnet", 1)
	if got := mustRead(t, custom); got != wantProjected {
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
