package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// TestPrintAdoptReportPrintsFacetNotesUnderFacets (#596): the default-facet notes print indented
// under the Facets line (positive); a report without notes prints none (negative); an empty
// facet list still prints its Facets line (boundary).
func TestPrintAdoptReportPrintsFacetNotesUnderFacets(t *testing.T) {
	rep := &adopt.AdoptReport{Facets: []string{"security:high"}, FacetOrigin: adopt.FacetsDefaulted, BaselineStatus: "not_run",
		FacetNotes: []string{"default facets: one", "choose other facets: two"}}
	out, err := captureStdout(t, func() error { printAdoptReport(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Facets:             [security:high]\n  default facets: one\n  choose other facets: two\n")
	rep.Facets, rep.FacetOrigin, rep.FacetNotes = nil, adopt.FacetsDeclared, nil
	out, err = captureStdout(t, func() error { printAdoptReport(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Facets:             []\n")
	if strings.Contains(out, "default facets") {
		t.Fatalf("declared facets printed a default note:\n%s", out)
	}
}

// TestRunAdoptTellsAnEmptyFacetsFlagFromAnOmittedOne (#596): through flag parsing, an omitted
// --facets and an explicitly empty one both declare the defaults on a first adoption, and the
// report says which one it was (positive, boundary); a --facets naming facets prints no default
// note (negative).
func TestRunAdoptTellsAnEmptyFacetsFlagFromAnOmittedOne(t *testing.T) {
	cases := []struct {
		flags []string
		want  string
	}{
		{nil, "  default facets: --facets was omitted, so adoption declares "},
		{[]string{"--facets="}, "  default facets: --facets named no facet, so adoption declares "},
		{[]string{"--facets=agent:sandboxed"}, ""},
	}
	for _, tc := range cases {
		args := append([]string{"--dry-run", "--record-baseline=false", "--path", gitRepoDir(t)}, tc.flags...)
		out, err := captureStdout(t, func() error { return runAdopt(args) })
		if err != nil {
			t.Fatalf("adopt %v: %v\n%s", tc.flags, err, out)
		}
		if tc.want == "" && strings.Contains(out, "default facets:") || tc.want != "" && !strings.Contains(out, tc.want) {
			t.Fatalf("adopt %v: want %q in:\n%s", tc.flags, tc.want, out)
		}
	}
}
