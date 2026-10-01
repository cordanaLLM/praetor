package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// pluginSkillText is a canonical skill the caveman gate accepts.
const pluginSkillText = "---\nname: lint\n---\n# Lint\n\n- run `make lint` before commit\n"

// pluginAuditorCopy is the plugin copy of the canonical auditor persona.
const pluginAuditorCopy = compiler.PluginAgentsRel + "/repo-auditor.md"

// pluginSkillCopy is the plugin copy of pluginSkillText.
const pluginSkillCopy = compiler.PluginSkillsRel + "/lint/SKILL.md"

// newPluginRepo is a repository that ships the praetor plugin (compiler.PluginManifestRel) and
// declares one canonical skill, with both canonical personas holding an earlier Praetor text
// and every persona copy, the plugin copies included, projected from them: the state
// compile-context leaves before a release changes the personas (#359).
func newPluginRepo(t *testing.T, name string) string {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(compiler.PluginManifestRel)), "{\"name\": \"praetor\"}\n")
	mustWrite(t, filepath.Join(repoPath, ".agents", "skills", "lint", "SKILL.md"), pluginSkillText)
	for _, persona := range generatedPersonas() {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(persona.rel)), earlierPersonaText(t, persona))
	}
	if _, err := compiler.CompileAgentSurfaces(t.Context(), io.Discard, repoPath); err != nil {
		t.Fatalf("project the earlier personas: %v", err)
	}
	return repoPath
}

// earlierPersonaText returns, by fixture name, the first text an earlier release wrote at the
// persona that differs from its current text, so adoption refreshes it.
func earlierPersonaText(t *testing.T, persona scaffold) string {
	t.Helper()
	fixtures := readFixtureDir(t, personaFixtureDir(persona.rel))
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if text := string(fixtures[name]); text != string(persona.content) {
			return text
		}
	}
	t.Fatalf("no earlier text of %s in %s", persona.rel, personaFixtureDir(persona.rel))
	return ""
}

// assertContextVerifies runs what compile-context --verify runs (compiler.VerifyCompiledContext)
// and the audit's persona projection gate (compiler.VerifyAgentProjections, auditAgentProjections
// in cmd/standardsctl/audit.go) over repoPath.
func assertContextVerifies(t *testing.T, repoPath string) {
	t.Helper()
	source := filepath.Join(repoPath, agentsFile)
	if err := compiler.VerifyCompiledContext(t.Context(), io.Discard, compiler.NewTranspiler(), source, repoPath); err != nil {
		t.Fatalf("compile-context --verify rejects the adopted repository: %v", err)
	}
	if _, err := compiler.VerifyAgentProjections(t.Context(), repoPath); err != nil {
		t.Fatalf("audit's projection gate rejects the adopted repository: %v", err)
	}
}

// Positive: the #359 reproduction. A forced adoption of a repository that ships the plugin
// refreshes the earlier canonical personas and writes every copy compile-context --verify
// checks, the plugin persona and skill copies included, so verify and the audit's projection
// gate pass right after it; the plugin copies are reported by the rule every persona copy
// follows.
func TestAdopt_Positive_ForceProjectsThePluginCopies(t *testing.T) {
	repoPath := newPluginRepo(t, "plugin-force")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	assertContextVerifies(t, repoPath)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(pluginAuditorCopy))); got != defaultAuditorAgentMD {
		t.Fatalf("plugin persona copy not refreshed:\n%s", got)
	}
	if got := findActionDetail(rep.ActionDetails, pluginAuditorCopy); got != "Synchronized plugin copy of the canonical agent definition" {
		t.Errorf("%s reported as %q", pluginAuditorCopy, got)
	}
	if got := findActionDetail(rep.ActionDetails, pluginSkillCopy); got != "Synchronized plugin copy of the canonical skill" {
		t.Errorf("%s reported as %q", pluginSkillCopy, got)
	}
	if rep.Outcome() != OutcomeApplied {
		t.Errorf("outcome = %s, want applied", rep.Outcome())
	}
}

// Positive: a hand-edited plugin persona copy is replaced with its line delta and a backup, as a
// hand-edited client persona copy is, never listed as created.
func TestAdopt_Positive_EditedPluginCopyReplacedWithBackup(t *testing.T) {
	repoPath := newPluginRepo(t, "plugin-edit")
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(pluginAuditorCopy)), "# Local auditor notes\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	assertContextVerifies(t, repoPath)
	if !hasAction(rep, pluginAuditorCopy, actionReplace) || contains(rep.CreatedFiles, pluginAuditorCopy) {
		t.Fatalf("edited plugin copy not reported as replaced: %v", rep.ActionDetails)
	}
	detail := findActionDetail(rep.ActionDetails, pluginAuditorCopy)
	if !strings.Contains(detail, `removed "# Local auditor notes"`) || !strings.Contains(detail, "backup: "+adoptBackupRoot+"/") {
		t.Errorf("replace detail lacks the delta or the backup: %s", detail)
	}
}

// Negative: a symlinked plugin persona directory fails adoption before its first write, with
// nothing written in the repository or through the link.
func TestAdopt_Negative_SymlinkedPluginAgentsRefusedBeforeAnyWrite(t *testing.T) {
	repoPath := newTestRepo(t, "plugin-symlink")
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(compiler.PluginManifestRel)), "{\"name\": \"praetor\"}\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repoPath, filepath.FromSlash(compiler.PluginAgentsRel))); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err == nil || !strings.Contains(err.Error(), "agent-definitions preflight") ||
		!strings.Contains(err.Error(), compiler.PluginAgentsRel) {
		t.Fatalf("want the symlinked plugin directory refused in the preflight, got %v", err)
	}
	if len(rep.Errors) == 0 {
		t.Error("the refusal is not in the report")
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("wrote through the symlinked plugin directory: %v (err=%v)", entries, err)
	}
}

// Negative: a projection adoption cannot repair, a plugin persona copy of a persona the
// repository does not define, is never deleted (adopter data), and the compile-context --verify
// after the chain rejects it: the run is incomplete, and the error is on the agent-definitions
// step, which writes the plugin copies, not on the agent-harness step or its pillars.
func TestAdopt_Negative_UnverifiableContextFailsTheRun(t *testing.T) {
	repoPath := newPluginRepo(t, "plugin-orphan")
	orphan := compiler.PluginAgentsRel + "/retired.md"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(orphan)), "# Retired\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if rep.Outcome() != OutcomeIncomplete || len(rep.Errors) != 1 {
		t.Fatalf("outcome = %s, errors = %v; want one verification error", rep.Outcome(), rep.Errors)
	}
	for _, want := range []string{verifyRejection + ": ", orphan, "projects no canonical persona"} {
		if !strings.Contains(rep.Errors[0], want) {
			t.Errorf("error lacks %q: %s", want, rep.Errors[0])
		}
	}
	if defs := stepOutcome(t, rep, "agent-definitions"); defs.Status != StepFailed || len(defs.Errors) != 1 {
		t.Errorf("agent-definitions = %+v, want failed with the verification error", defs)
	}
	assertHarnessPillarsClean(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(orphan))); got != "# Retired\n" {
		t.Errorf("adoption rewrote the orphaned copy: %q", got)
	}
}

// Boundary: a dry run of a repository that ships the plugin writes nothing, plugin copies
// included, and runs no verification over the tree it left as it was.
func TestAdopt_Boundary_PluginDryRunWritesNothing(t *testing.T) {
	repoPath := newPluginRepo(t, "plugin-dry-run")
	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("Adopt --dry-run: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.Outcome() != OutcomeSimulated {
		t.Errorf("outcome = %s, want simulated", rep.Outcome())
	}
}

// Boundary: agent_clients naming one client projects the personas into that client's
// directory and the plugin, and leaves an unselected persona directory byte for byte as it was,
// stale copy included, which compile-context --verify does not check either.
func TestAdopt_Boundary_AgentClientsSubsetLeavesUnselectedDirsUntouched(t *testing.T) {
	repoPath := newPluginRepo(t, "plugin-subset")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nagent_clients: [claude]\n")
	unselected := filepath.Join(repoPath, ".codex", "agents", "repo-auditor.md")
	mustWrite(t, unselected, "# Stale codex copy\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	assertContextVerifies(t, repoPath)
	if got := mustRead(t, unselected); got != "# Stale codex copy\n" {
		t.Errorf("unselected persona directory rewritten: %q", got)
	}
	for _, rel := range []string{".claude/agents/repo-auditor.md", pluginAuditorCopy} {
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != defaultAuditorAgentMD {
			t.Errorf("selected copy %s not projected", rel)
		}
	}
	if notApplicableDetail(rep, ".codex/agents") == "" || hasAction(rep, ".codex/agents/repo-auditor.md", actionReplace) {
		t.Errorf("unselected directory not reported as not applicable: %v", rep.ActionDetails)
	}
}

// verifyFailures opens every joined error in order and keeps a once-wrapped failure whole.
// Positive: the checks VerifyCompiledContext joins come back one per entry. Negative: a single
// error is its own only failure, and nil errors are dropped. Boundary: a join deeper than the
// bound keeps the unopened rest joined in the last entry, dropping nothing.
func TestVerifyFailures(t *testing.T) {
	first, second, third := errors.New("register"), errors.New("vendor"), errors.New("persona")
	wrapped := errors.Join(errors.Join(first, errors.Join(second)), errors.Join(nil, fmt.Errorf("check failed: %w", third)))
	got := verifyFailures(wrapped)
	if len(got) != 3 || !errors.Is(got[0], first) || !errors.Is(got[1], second) || !errors.Is(got[2], third) {
		t.Fatalf("verifyFailures = %v", got)
	}
	if got[0].Error() != "register" || got[2].Error() != "check failed: persona" {
		t.Fatalf("a failure lost or gained context: %q, %q", got[0], got[2])
	}
	if single := verifyFailures(first); len(single) != 1 || single[0].Error() != "register" {
		t.Fatalf("single error = %v", single)
	}
	deep := make([]error, 0, maxVerifyFailures+2)
	for i := 0; i < maxVerifyFailures+2; i++ {
		deep = append(deep, errors.New("check"))
	}
	bounded := verifyFailures(errors.Join(deep...))
	if len(bounded) != maxVerifyFailures {
		t.Fatalf("bounded = %d entries, want %d", len(bounded), maxVerifyFailures)
	}
	if rest, ok := bounded[len(bounded)-1].(interface{ Unwrap() []error }); !ok || len(rest.Unwrap()) != 3 {
		t.Fatalf("the unopened rest must stay joined in the last entry: %v", bounded[len(bounded)-1])
	}
}

// Negative and its remedy: a manifest that declines git-ignore leaves .gitignore to the
// operator, and compile-context --verify requires Git to ignore .workingdir/evidence/ (#631).
// Without an operator rule the run is incomplete and the error names the remedy; it lands on the
// declined git-ignore step, which owns the rule, and leaves the harness pillars alone. With
// /.workingdir/ in the operator's .gitignore the same run is applied.
func TestAdopt_DeclinedGitIgnoreNeedsTheOperatorWorkingDirRule(t *testing.T) {
	for _, tc := range []struct {
		name      string
		gitignore string
		want      AdoptOutcome
	}{
		{name: "no-rule", want: OutcomeIncomplete},
		{name: "operator-rule", gitignore: "/.workingdir/\n", want: OutcomeApplied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := newTestRepo(t, "declined-ignore-"+tc.name)
			mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [git-ignore]\n")
			if tc.gitignore != "" {
				mustWrite(t, filepath.Join(repoPath, ".gitignore"), tc.gitignore)
			}
			rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
			if err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			if rep.Outcome() != tc.want {
				t.Fatalf("outcome = %s, want %s; errors %v", rep.Outcome(), tc.want, rep.Errors)
			}
			if tc.want == OutcomeApplied {
				return
			}
			if len(rep.Errors) != 1 {
				t.Fatalf("errors = %v, want the one verification error", rep.Errors)
			}
			for _, want := range []string{verifyRejection + ": ", ".workingdir/evidence/", "add /.workingdir/ to the operator-owned .gitignore"} {
				if !strings.Contains(rep.Errors[0], want) {
					t.Errorf("error lacks %q: %s", want, rep.Errors[0])
				}
			}
			if ignore := stepOutcome(t, rep, "git-ignore"); ignore.Status != StepDeclined || len(ignore.Errors) != 1 {
				t.Errorf("git-ignore = %+v, want declined carrying the verification error", ignore)
			}
			assertHarnessPillarsClean(t, rep)
		})
	}
}
