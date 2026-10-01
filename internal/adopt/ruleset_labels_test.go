package adopt

import (
	"bytes"
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

// sameLabelsIgnoringDescription reports whether both taxonomies hold the same labels by name
// and color; a refresh may reword a description (hiss-waiver, #393).
func sameLabelsIgnoringDescription(a, b []forge.Label) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Color != b[i].Color {
			return false
		}
	}
	return true
}

func TestPriorLabelTaxonomyDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorLabelFixtures, priorLabelTaxonomyDigests)
}

// Positive: the earlier taxonomy text adoption wrote is refreshed to the current one without
// --force, and keeps every label; it differs only by the document start yamllint requires or the hiss-waiver description.
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
		if err != nil || !sameLabelsIgnoringDescription(before, after) {
			t.Errorf("%s: the refresh changed the labels (err %v)", name, err)
		}
	}
}

// Negative: an edited copy of the earlier text is the repository's configuration: it stays
// byte for byte and is reported as drift, --force included.
func TestReconcileLabels_Negative_EditedPriorStaysUntouched(t *testing.T) {
	prior := readFixtureDir(t, priorLabelFixtures)["pre-document-start.labels.yaml"]
	edited := append(bytes.Clone(prior), []byte("  - name: \"local\"\n    color: \"000000\"\n")...)
	for _, force := range []bool{false, true} {
		s, path := labelSession(t, edited, AdoptOptions{Force: force})
		if err := reconcileLabels(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, path); got != string(edited) {
			t.Errorf("force=%v: an edited taxonomy was rewritten:\n%s", force, got)
		}
		if len(s.report.Warnings) != 1 {
			t.Errorf("force=%v: drift not reported: %v", force, s.report.Warnings)
		}
	}
}

// Boundary: the current taxonomy re-adopts without a write, a dry run of a prior text reports
// the refresh and writes nothing, and a missing taxonomy is created.
func TestReconcileLabels_Boundary_IdempotentDryRunAndMissing(t *testing.T) {
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

	prior := readFixtureDir(t, priorLabelFixtures)["pre-document-start.labels.yaml"]
	s, path := labelSession(t, prior, AdoptOptions{DryRun: true})
	if err := reconcileLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != string(prior) || !contains(s.report.ReconciledFiles, labelsFile) {
		t.Errorf("a dry run must report the refresh and write nothing: %v", s.report.ReconciledFiles)
	}

	s, path = labelSession(t, nil, AdoptOptions{})
	if err := reconcileLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != string(current) || !contains(s.report.CreatedFiles, labelsFile) {
		t.Errorf("a missing taxonomy must be created: %v", s.report.CreatedFiles)
	}
}

// Positive (HISS-21): a CRLF checkout of the earlier taxonomy (core.autocrlf on Windows) is
// still Praetor's unedited output. It is refreshed without --force and keeps its CRLF style,
// and the run after it verifies the refreshed file without a warning.
func TestReconcileLabels_Positive_RefreshesCRLFPriorInItsOwnStyle(t *testing.T) {
	prior := crlfText(string(readFixtureDir(t, priorLabelFixtures)["pre-document-start.labels.yaml"]))
	s, path := labelSession(t, []byte(prior), AdoptOptions{})
	if err := reconcileLabels(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	want := crlfText(string(forge.DefaultLabelTaxonomy()))
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
}

// Negative and boundary: an edited CRLF copy of the earlier taxonomy, and a copy with mixed
// line endings, are not Praetor's unedited output. Both stay byte for byte, with a warning.
func TestReconcileLabels_Negative_EditedOrMixedCRLFPriorStaysUntouched(t *testing.T) {
	prior := string(readFixtureDir(t, priorLabelFixtures)["pre-document-start.labels.yaml"])
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
}
