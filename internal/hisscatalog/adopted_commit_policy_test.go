// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"strings"
	"testing"
)

func TestAdoptedCommitPolicy_Positive_BothStages(t *testing.T) {
	rule, ok := LookupRule("HISS-14")
	if !ok {
		t.Fatal("HISS-14 missing")
	}
	if rule.Adoption.Stages != StageCommitMsg|StagePrePush {
		t.Fatalf("HISS-14 stages = %b, want commit-msg and pre-push", rule.Adoption.Stages)
	}
	for _, pipeline := range []Pipeline{AllPipelines, PipelineLefthook} {
		check, failure := rule.Adopted(pipeline)
		for _, want := range []string{"`praetorctl forge check-message`", "`praetorctl forge check-commits`", "lefthook commit-msg/pre-push"} {
			if !strings.Contains(check, want) {
				t.Errorf("check lacks %q: %s", want, check)
			}
		}
		if failure != "policy violation blocks commit, blocks push" {
			t.Errorf("failure = %q", failure)
		}
	}
}

func TestAdoptedCommitPolicy_Negative_UninstalledHooks(t *testing.T) {
	rule, _ := LookupRule("HISS-14")
	for _, pipeline := range []Pipeline{PipelineVerifyAll, 0} {
		if check, failure := rule.Adopted(pipeline); check != NotEnforced || failure != Advisory {
			t.Errorf("uninstalled hooks credited: %q, %q", check, failure)
		}
	}
}

func TestAdoptedCommitPolicy_Boundary_LanguageAndOrder(t *testing.T) {
	rule, _ := LookupRule("HISS-14")
	check, _ := rule.AdoptedFor(PipelineLefthook, Facts{Languages: LanguageOther})
	if check == NotEnforced {
		t.Fatal("language-independent message policy lost for other languages")
	}
	all := AdoptedCheck{Stages: StagePreCommit | StageCommitMsg | StagePrePush}
	labels := stageLabels(all.stagesIn(PipelineLefthook))
	if labels != "lefthook pre-commit/commit-msg/pre-push" {
		t.Errorf("stage labels = %q", labels)
	}
}
