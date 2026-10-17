// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// editedEarlierAuditor is the earlier standardsctl auditor persona as an adopter's formatter left
// it (#1426): the description single-quoted and a trailing blank line added, so it matches no
// recorded Praetor text, and no model key, which the audit requires since #1341.
func editedEarlierAuditor(t *testing.T) string {
	t.Helper()
	text := string(readFixtureDir(t, filepath.Join(priorPersonaFixtures, "repo-auditor"))["standardsctl.repo-auditor.md.prior"])
	quoted := strings.Replace(text, `description: "Autonomous agent for repository HISS invariant sweeps and standardsctl compliance."`,
		`description: 'Autonomous agent for repository HISS invariant sweeps and standardsctl compliance.'`, 1)
	if quoted == text {
		t.Fatal("the earlier auditor fixture no longer holds the description this test edits")
	}
	return quoted + "\n"
}

// withTier is text with "model: <tier>" as the last front matter line, in text's line endings.
func withTier(text, tier string) string {
	return strings.Replace(text, "commandExecutionPolicy: auto\n---", "commandExecutionPolicy: auto\nmodel: "+tier+"\n---", 1)
}

// detailsFor joins every action detail the report records for rel.
func detailsFor(rep *AdoptReport, rel string) string {
	var details []string
	for _, detail := range rep.ActionDetails {
		if detail.Path == rel {
			details = append(details, detail.Details)
		}
	}
	return strings.Join(details, "\n")
}

// Positive (#1426): a plain run gives an edited persona that predates the model key the tier
// of its template, as the last front matter line in the file's own line endings, keeps every
// other byte, and the persona then passes the audit's front matter check.
func TestAdopt_Positive_EditedPersonaGainsModelTier(t *testing.T) {
	edited := editedEarlierAuditor(t)
	for style, text := range map[string]string{"lf": edited, "crlf": crlfText(edited)} {
		repoPath := newTestRepo(t, "persona-tier-"+style)
		full := filepath.Join(repoPath, filepath.FromSlash(auditorAgentFile))
		mustWrite(t, full, text)
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		want := withTier(edited, compiler.PersonaModelPro)
		if style == "crlf" {
			want = crlfText(want)
		}
		if got := mustRead(t, full); got != want {
			t.Fatalf("%s: persona after adopt:\n%q\nwant:\n%q", style, got, want)
		}
		problems, err := compiler.CheckPersonaFrontMatter(context.Background(), repoPath, []string{"repo-auditor.md"})
		if err != nil || len(problems) != 0 {
			t.Fatalf("%s: the repaired persona still fails: %+v %v", style, problems, err)
		}
		if !strings.Contains(detailsFor(rep, auditorAgentFile), "Added the model tier the audit requires (model: pro)") {
			t.Fatalf("%s: the repair is not reported: %s", style, detailsFor(rep, auditorAgentFile))
		}
	}
}

// Negative: a model value outside the tiers is the repository's own choice, so adoption leaves
// that persona as it is, and the remedy asks for the line by hand; a persona the repository
// wrote itself gets the same remedy, never the adopt one.
func TestAdopt_Negative_InvalidOrOwnPersonaTierLeftAlone(t *testing.T) {
	invalid := strings.Replace(editedEarlierAuditor(t), "commandExecutionPolicy: auto\n---", "commandExecutionPolicy: auto\nmodel: opus\n---", 1)
	repoPath := newTestRepo(t, "persona-tier-invalid")
	full := filepath.Join(repoPath, filepath.FromSlash(auditorAgentFile))
	mustWrite(t, full, invalid)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), `model "opus" is invalid`) {
		t.Fatalf("adopt over model: opus: err %v, want the invalid tier refused", err)
	}
	if got := mustRead(t, full); got != invalid {
		t.Fatalf("a persona declaring model: opus was changed:\n%s", got)
	}
	if rep != nil && strings.Contains(detailsFor(rep, auditorAgentFile), "Added the model tier") {
		t.Fatalf("a tier was added beside a declared one: %s", detailsFor(rep, auditorAgentFile))
	}
	for _, rel := range []string{auditorAgentFile, compiler.CanonicalAgentsRel + "/custom.md"} {
		if rel != auditorAgentFile {
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), "---\nname: custom\ndescription: \"Own persona.\"\n---\n")
		}
		remedy := PersonaModelTierRemedy(context.Background(), repoPath, rel)
		if !strings.Contains(remedy, "add model: flash or model: pro") || strings.Contains(remedy, "praetorctl adopt") {
			t.Fatalf("%s: remedy %q, want the line by hand", rel, remedy)
		}
	}
}

// Boundary: a dry run writes nothing and records the repair it would make; an edited persona
// that already declares a tier, even another than its template's, gains no second key; and the
// remedy for an unedited earlier text and for an edited keyless one each name adopt.
func TestAdopt_Boundary_PersonaTierDryRunAndDeclaredTier(t *testing.T) {
	edited := editedEarlierAuditor(t)
	repoPath := newTestRepo(t, "persona-tier-dry")
	full := filepath.Join(repoPath, filepath.FromSlash(auditorAgentFile))
	mustWrite(t, full, edited)
	if !strings.Contains(PersonaModelTierRemedy(context.Background(), repoPath, auditorAgentFile), "adds the template's model tier") {
		t.Fatal("the remedy for an edited keyless persona does not name adopt")
	}
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := mustRead(t, full); got != edited {
		t.Fatal("a dry run wrote the persona")
	}
	if !strings.Contains(detailsFor(rep, auditorAgentFile), "Added the model tier") {
		t.Fatalf("the dry run does not name the repair: %s", detailsFor(rep, auditorAgentFile))
	}
	declared := withTier(edited, compiler.PersonaModelFlash)
	mustWrite(t, full, declared)
	adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	if got := mustRead(t, full); got != declared {
		t.Fatalf("a persona declaring a tier was changed:\n%s", got)
	}
	prior := string(readFixtureDir(t, filepath.Join(priorPersonaFixtures, "repo-auditor"))["standardsctl.repo-auditor.md.prior"])
	mustWrite(t, full, prior)
	if !strings.Contains(PersonaModelTierRemedy(context.Background(), repoPath, auditorAgentFile), "unedited copy of an earlier engine's template") {
		t.Fatal("the remedy for an unedited earlier persona does not say adopt replaces it")
	}
}
