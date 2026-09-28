// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// earlierText is an earlier Praetor text of a template in the tests below.
const earlierText = "# earlier rendering\nkey: old\n"

// priorOf records text as the one earlier text of a template (TemplateItem.Prior).
func priorOf(t *testing.T, text string) map[string]string {
	t.Helper()
	digest, _, err := util.CanonicalTextDigest([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{digest: "earlier release"}
}

// withPrior is a ContentFunc template at rel declaring earlierText as an earlier text.
func withPrior(t *testing.T, rel string) TemplateItem {
	t.Helper()
	tmpl := contentFuncTemplate(rel)
	tmpl.Prior = priorOf(t, earlierText)
	return tmpl
}

// Positive: a kept file holding an earlier text is refreshed, in its own line-ending style.
func TestPlanTargetWrite_Positive_RefreshesAnEarlierText(t *testing.T) {
	prior := priorOf(t, earlierText)
	for _, crlf := range []bool{false, true} {
		target := templateTarget{before: []byte(util.RestoreLineEndings(earlierText, crlf)), exists: true, keep: true}
		body, write := planTargetWrite(target, []byte(forceBody), prior)
		if want := util.RestoreLineEndings(forceBody, crlf); write != targetRefreshed || string(body) != want {
			t.Errorf("crlf %v: got %d %q, want a refresh to %q", crlf, write, body, want)
		}
	}
}

// Negative: a file forceProtected names is never refreshed, and an edited or unrecorded text
// is kept; under --force (keep false) a differing file is written as before.
func TestPlanTargetWrite_Negative_ProtectedEditedAndUnrecordedAreKept(t *testing.T) {
	prior := priorOf(t, earlierText)
	cases := map[string]struct {
		target templateTarget
		prior  map[string]string
		want   targetWrite
	}{
		"protected":  {templateTarget{before: []byte(earlierText), exists: true, keep: true, protected: true}, prior, targetKept},
		"edited":     {templateTarget{before: []byte(earlierText + "local: true\n"), exists: true, keep: true}, prior, targetKept},
		"unrecorded": {templateTarget{before: []byte(earlierText), exists: true, keep: true}, nil, targetKept},
		"forced":     {templateTarget{before: []byte(earlierText + "local: true\n"), exists: true}, prior, targetWritten},
	}
	for name, tc := range cases {
		if _, write := planTargetWrite(tc.target, []byte(forceBody), tc.prior); write != tc.want {
			t.Errorf("%s: got %d, want %d", name, write, tc.want)
		}
	}
}

// Boundary: an absent file is written, and a file already holding the rendering, line endings
// aside, is unchanged even when the rendering is itself a recorded earlier text.
func TestPlanTargetWrite_Boundary_AbsentAndUnchanged(t *testing.T) {
	prior := priorOf(t, forceBody)
	if _, write := planTargetWrite(templateTarget{}, []byte(forceBody), prior); write != targetWritten {
		t.Errorf("absent file: got %d, want written", write)
	}
	crlf := templateTarget{before: []byte(util.RestoreLineEndings(forceBody, true)), exists: true, keep: true}
	if _, write := planTargetWrite(crlf, []byte(forceBody), prior); write != targetUnchanged {
		t.Errorf("file holding the rendering: got %d, want unchanged", write)
	}
}

// Positive and negative through scaffoldTemplate: an earlier text is refreshed without --force,
// but never at a path --force never replaces, and a file already holding the rendering stays.
func TestScaffoldTemplate_PriorRefreshStopsAtProtectedPaths(t *testing.T) {
	root := seedRepo(t, map[string]string{
		"lint.yml":        earlierText,
		".standards.yaml": earlierText,
		"current.yml":     forceBody,
	})
	for rel, want := range map[string]struct {
		outcome templateOutcome
		body    string
	}{
		"lint.yml":        {templateRefreshed, forceBody},
		".standards.yaml": {templateSkipped, earlierText},
		"current.yml":     {templateSkipped, forceBody},
	} {
		outcome, _, err := scaffoldTemplate(t.Context(), root, withPrior(t, rel), "widget", "", false)
		if err != nil || outcome != want.outcome {
			t.Errorf("%s: got outcome %d err %v, want %d", rel, outcome, err, want.outcome)
		}
		if got := readRepoFile(t, root, rel); got != want.body {
			t.Errorf("%s: left %q, want %q", rel, got, want.body)
		}
	}
}

// Positive: the workflow plan asks what apply asks, so a workflow holding an earlier text is
// planned as the rendering apply refreshes it to; an edited one is the repository's own.
func TestPlannedWorkflowBodies_ListsAWorkflowApplyRefreshes(t *testing.T) {
	const rel = ".github/workflows/ci.yml"
	for body, wantPlanned := range map[string]bool{earlierText: true, earlierText + "local: true\n": false} {
		root := seedRepo(t, map[string]string{rel: body})
		planned, err := plannedWorkflowBodies(t.Context(), root, []TemplateItem{withPrior(t, rel)}, "widget", "")
		if err != nil {
			t.Fatal(err)
		}
		listed := slices.ContainsFunc(planned, func(p PlannedTemplate) bool { return p.Path == rel && p.Content == forceBody })
		if listed != wantPlanned {
			t.Errorf("workflow %q: planned %v, want %v (%+v)", body, listed, wantPlanned, planned)
		}
	}
}
