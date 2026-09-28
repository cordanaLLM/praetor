package adopt

import (
	"bytes"
	"path/filepath"
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

func TestPriorLabelTaxonomyDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorLabelFixtures, priorLabelTaxonomyDigests)
}

// Positive: the earlier taxonomy text adoption wrote is refreshed to the current one without
// --force, and keeps every label; it differs only by the document start yamllint requires.
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
		if err != nil || !deepEqual(before, after) {
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
