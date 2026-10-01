package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// priorLabelFixtures reproduces every digest in priorLabelTaxonomyDigests.
const priorLabelFixtures = "testdata/labels"

// labelSession is an adoption session over a fresh directory holding labels, or no taxonomy
// when labels is nil.
func labelSession(t *testing.T, labels []byte, opts AdoptOptions) (*adoptSession, string) {
	t.Helper()
	root := t.TempDir()
	if labels != nil {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(labelsFile)), string(labels))
	}
	return &adoptSession{repoPath: root, opts: opts, report: &AdoptReport{}}, filepath.Join(root, filepath.FromSlash(labelsFile))
}

// revisedLabelDescription names the one label whose description a refresh may reword: the
// earlier text claimed waivers are cryptographically signed (#393).
const revisedLabelDescription = "hiss-waiver"

// sameLabelsButRevisedDescription reports whether both taxonomies hold the same labels, in the
// same order, with the same colors and descriptions, except the description of
// revisedLabelDescription.
func sameLabelsButRevisedDescription(a, b []forge.Label) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a) && i < forge.MaxLabelsLimit; i++ {
		if a[i].Name != b[i].Name || a[i].Color != b[i].Color {
			return false
		}
		if a[i].Description != b[i].Description && a[i].Name != revisedLabelDescription {
			return false
		}
	}
	return true
}

// Positive, negative and boundary: an identical taxonomy and one differing only in the revised
// description are the same labels; a reworded other description, a recolored label and a
// dropped label are not.
func TestSameLabelsButRevisedDescription_3D(t *testing.T) {
	current, err := forge.ParseLabelTaxonomy(forge.DefaultLabelTaxonomy())
	if err != nil {
		t.Fatal(err)
	}
	edit := func(name string, change func(*forge.Label)) []forge.Label {
		labels := append([]forge.Label(nil), current...)
		for i := range labels {
			if labels[i].Name == name {
				change(&labels[i])
			}
		}
		return labels
	}
	reword := func(l *forge.Label) { l.Description = "reworded" }
	cases := map[string]struct {
		labels []forge.Label
		same   bool
	}{
		"positive: identical":                        {current, true},
		"positive: hiss-waiver description reworded": {edit(revisedLabelDescription, reword), true},
		"negative: another description reworded":     {edit("hiss-violation", reword), false},
		"negative: hiss-waiver recolored":            {edit(revisedLabelDescription, func(l *forge.Label) { l.Color = "000000" }), false},
		"boundary: one label fewer":                  {current[:len(current)-1], false},
	}
	for name, tc := range cases {
		if got := sameLabelsButRevisedDescription(current, tc.labels); got != tc.same {
			t.Errorf("%s: same = %v, want %v", name, got, tc.same)
		}
	}
}

func TestPriorLabelTaxonomyDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorLabelFixtures, priorLabelTaxonomyDigests)
}

// Boundary: the current taxonomy is never a prior one. A revision of forge.DefaultLabelTaxonomy
// records the text it replaces, so reverting the revision lands on a recorded digest and fails.
func TestPriorLabelTaxonomyDigests_Boundary_CurrentTaxonomyIsNotPrior(t *testing.T) {
	digest := fixtureDigest(t, "current taxonomy", forge.DefaultLabelTaxonomy())
	if origin, prior := priorLabelTaxonomyDigests[digest]; prior {
		t.Fatalf("the current label taxonomy is recorded as a prior one (%s)", origin)
	}
}

// Positive: the earlier taxonomy text adoption wrote is refreshed to the current one without
// --force, and keeps every label; it differs only by the document start yamllint requires or
// by the hiss-waiver description.
func TestReconcileLabels_Positive_RefreshesPriorTaxonomy(t *testing.T) {
	for name, prior := range readFixtureDir(t, priorLabelFixtures) {
		s, path := labelSession(t, prior, AdoptOptions{})
		if err := reconcileLabels(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := mustRead(t, path); got != string(forge.DefaultLabelTaxonomy()) {
			t.Errorf("%s: not refreshed to the current taxonomy:\n%s", name, got)
		}
		if detail := findActionDetail(s.report.ActionDetails, labelsFile); detail == "" || !contains(s.report.ReconciledFiles, labelsFile) {
			t.Errorf("%s: refresh not reported: %v", name, s.report.ActionDetails)
		}
		before, err := forge.ParseLabelTaxonomy(prior)
		if err != nil {
			t.Fatalf("%s: prior taxonomy must parse: %v", name, err)
		}
		after, err := forge.ParseLabelTaxonomy(forge.DefaultLabelTaxonomy())
		if err != nil || !sameLabelsButRevisedDescription(before, after) {
			t.Errorf("%s: the refresh changed the labels (err %v)", name, err)
		}
	}
}

// forEachPriorLabelTaxonomy runs check once per recorded earlier taxonomy text, so every case
// below covers the document-start revision and the hiss-waiver revision alike.
func forEachPriorLabelTaxonomy(t *testing.T, check func(t *testing.T, prior string)) {
	t.Helper()
	for name, prior := range readFixtureDir(t, priorLabelFixtures) {
		t.Run(name, func(t *testing.T) { check(t, string(prior)) })
	}
}

// Negative: an edited copy of an earlier text is the repository's configuration: it stays
// byte for byte and is reported as drift, --force included.
func TestReconcileLabels_Negative_EditedPriorStaysUntouched(t *testing.T) {
	forEachPriorLabelTaxonomy(t, func(t *testing.T, prior string) {
		edited := prior + "  - name: \"local\"\n    color: \"000000\"\n"
		for _, force := range []bool{false, true} {
			s, path := labelSession(t, []byte(edited), AdoptOptions{Force: force})
			if err := reconcileLabels(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, path); got != edited {
				t.Errorf("force=%v: an edited taxonomy was rewritten:\n%s", force, got)
			}
			if len(s.report.Warnings) != 1 {
				t.Errorf("force=%v: drift not reported: %v", force, s.report.Warnings)
			}
		}
	})
}

// Boundary: the current taxonomy re-adopts without a write, and a missing taxonomy is created.
func TestReconcileLabels_Boundary_IdempotentAndMissing(t *testing.T) {
	current := forge.DefaultLabelTaxonomy()
	s, _ := labelSession(t, current, AdoptOptions{})
	if err := reconcileLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if detail := findActionDetail(s.report.ActionDetails, labelsFile); detail == "" || len(s.report.Warnings) != 0 {
		t.Errorf("the current taxonomy must verify without a warning: %v %v", s.report.ActionDetails, s.report.Warnings)
	}
	if contains(s.report.CreatedFiles, labelsFile) {
		t.Error("the current taxonomy must not be rewritten")
	}

	s, path := labelSession(t, nil, AdoptOptions{})
	if err := reconcileLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != string(current) || !contains(s.report.CreatedFiles, labelsFile) {
		t.Errorf("a missing taxonomy must be created: %v", s.report.CreatedFiles)
	}
}

// Boundary: a dry run of each earlier text reports the refresh and writes nothing.
func TestReconcileLabels_Boundary_DryRunReportsPriorRefreshWithoutWriting(t *testing.T) {
	forEachPriorLabelTaxonomy(t, func(t *testing.T, prior string) {
		s, path := labelSession(t, []byte(prior), AdoptOptions{DryRun: true})
		if err := reconcileLabels(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, path); got != prior || !contains(s.report.ReconciledFiles, labelsFile) {
			t.Errorf("a dry run must report the refresh and write nothing: %v", s.report.ReconciledFiles)
		}
	})
}

// Positive (HISS-21): a CRLF checkout of an earlier taxonomy (core.autocrlf on Windows) is
// still Praetor's unedited output. It is refreshed without --force and keeps its CRLF style,
// and the run after it verifies the refreshed file without a warning.
func TestReconcileLabels_Positive_RefreshesCRLFPriorInItsOwnStyle(t *testing.T) {
	want := crlfText(string(forge.DefaultLabelTaxonomy()))
	forEachPriorLabelTaxonomy(t, func(t *testing.T, prior string) {
		s, path := labelSession(t, []byte(crlfText(prior)), AdoptOptions{})
		if err := reconcileLabels(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, path); got != want {
			t.Fatalf("the CRLF prior taxonomy was not refreshed in CRLF:\n%q", got)
		}
		if detail := findActionDetail(s.report.ActionDetails, labelsFile); !strings.HasPrefix(detail, "Refreshed") || len(s.report.Warnings) != 0 {
			t.Errorf("the refresh must be reported without a warning: %v %v", s.report.ActionDetails, s.report.Warnings)
		}
		s.report = &AdoptReport{}
		if err := reconcileLabels(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, path); got != want || len(s.report.Warnings) != 0 {
			t.Errorf("the refreshed CRLF taxonomy must verify unchanged: %v", s.report.Warnings)
		}
	})
}

// Negative and boundary: an edited CRLF copy of an earlier taxonomy, and a copy with mixed
// line endings, are not Praetor's unedited output. Both stay byte for byte, with a warning.
func TestReconcileLabels_Negative_EditedOrMixedCRLFPriorStaysUntouched(t *testing.T) {
	forEachPriorLabelTaxonomy(t, func(t *testing.T, prior string) {
		for name, text := range map[string]string{
			"edited CRLF":   crlfText(prior + "  - name: \"local\"\n    color: \"000000\"\n"),
			"mixed endings": strings.Replace(crlfText(prior), "\r\n", "\n", 1),
		} {
			s, path := labelSession(t, []byte(text), AdoptOptions{})
			if err := reconcileLabels(t.Context(), s); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got := mustRead(t, path); got != text {
				t.Errorf("%s: the taxonomy was rewritten:\n%q", name, got)
			}
			if len(s.report.Warnings) != 1 {
				t.Errorf("%s: the preserved file must be reported: %v", name, s.report.Warnings)
			}
		}
	})
}
