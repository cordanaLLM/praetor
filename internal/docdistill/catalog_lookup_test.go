package docdistill

import (
	"slices"
	"testing"
)

// repeatedRuns is how often a determinism assertion repeats. Go randomises the start of
// every map range, so a single passing call proves nothing about a map-ordered result.
const repeatedRuns = 32

func twoVersionCatalog() *DocCatalog {
	return &DocCatalog{
		Version: CatalogVersion,
		Packages: map[string]DistilledDoc{
			"gopkg.in/yaml.v3@v3.0.0": {PackageName: "gopkg.in/yaml.v3", Version: "v3.0.0", RawMarkdown: "old"},
			"gopkg.in/yaml.v3@v3.0.1": {PackageName: "gopkg.in/yaml.v3", Version: "v3.0.1", RawMarkdown: "new"},
			"github.com/acme/lib@v1.2.3": {PackageName: "github.com/acme/lib", Version: "v1.2.3",
				RawMarkdown: "lib"},
		},
	}
}

// TestCatalogLookup_Positive_SameAnswerEveryRun is the row's core claim: a catalog holding
// two versions of one package must not alternate between them.
func TestCatalogLookup_Positive_SameAnswerEveryRun(t *testing.T) {
	cat := twoVersionCatalog()
	first, found := cat.Lookup("gopkg.in/yaml.v3")
	if !found {
		t.Fatal("an exact package name must resolve")
	}
	for i := 0; i < repeatedRuns; i++ {
		again, ok := cat.Lookup("gopkg.in/yaml.v3")
		if !ok || again.Version != first.Version || again.RawMarkdown != first.RawMarkdown {
			t.Fatalf("lookup %d returned %+v, the first returned %+v", i, again, first)
		}
	}
	if first.Version != "v3.0.1" {
		t.Errorf("the greatest catalog key must win, got %q", first.Version)
	}
}

// TestCatalogLookup_Positive_ExactBeatsSuffix pins the precedence the MCP tool got wrong:
// a suffix match is a fallback, never a competitor to a name the caller spelled in full.
func TestCatalogLookup_Positive_ExactBeatsSuffix(t *testing.T) {
	cat := &DocCatalog{Packages: map[string]DistilledDoc{
		"yaml.v3@v1":          {PackageName: "yaml.v3", Version: "v1", RawMarkdown: "exact"},
		"gopkg.in/yaml.v3@v3": {PackageName: "gopkg.in/yaml.v3", Version: "v3", RawMarkdown: "suffix"},
	}}
	for i := 0; i < repeatedRuns; i++ {
		doc, ok := cat.Lookup("yaml.v3")
		if !ok || doc.RawMarkdown != "exact" {
			t.Fatalf("run %d: the exact match must win, got %+v (ok=%v)", i, doc, ok)
		}
	}

	suffixOnly := &DocCatalog{Packages: map[string]DistilledDoc{
		"gopkg.in/yaml.v3@v3": {PackageName: "gopkg.in/yaml.v3", Version: "v3", RawMarkdown: "suffix"},
	}}
	doc, ok := suffixOnly.Lookup("yaml.v3")
	if !ok || doc.RawMarkdown != "suffix" {
		t.Errorf("a suffix match must still answer when nothing matches exactly, got %+v (ok=%v)", doc, ok)
	}
}

// TestCatalogLookup_Negative_UnknownNilAndEmpty covers the answers that must be refusals.
func TestCatalogLookup_Negative_UnknownNilAndEmpty(t *testing.T) {
	cat := twoVersionCatalog()
	if _, ok := cat.Lookup("github.com/absent/pkg"); ok {
		t.Error("an unknown package must not resolve")
	}
	if _, ok := cat.Lookup(""); ok {
		t.Error("an empty package name must not resolve")
	}
	var nilCat *DocCatalog
	if _, ok := nilCat.Lookup("anything"); ok {
		t.Error("a nil catalog must not resolve")
	}
	if keys := nilCat.SortedKeys(); keys != nil {
		t.Errorf("a nil catalog has no keys, got %v", keys)
	}
	// A bare substring is not a path segment, so it is not a suffix match either.
	if _, ok := cat.Lookup("yaml"); ok {
		t.Error("a bare substring must not match a package path")
	}
}

// TestCatalogLookup_Boundary_EmptyAndSingleEntry covers the two smallest catalogs.
func TestCatalogLookup_Boundary_EmptyAndSingleEntry(t *testing.T) {
	empty := &DocCatalog{Packages: map[string]DistilledDoc{}}
	if _, ok := empty.Lookup("anything"); ok {
		t.Error("an empty catalog resolves nothing")
	}
	if len(empty.SortedKeys()) != 0 {
		t.Errorf("an empty catalog has no keys, got %v", empty.SortedKeys())
	}

	single := &DocCatalog{Packages: map[string]DistilledDoc{
		"only@v1": {PackageName: "only", Version: "v1", RawMarkdown: "one"},
	}}
	doc, ok := single.Lookup("only")
	if !ok || doc.RawMarkdown != "one" {
		t.Errorf("a single-entry catalog must answer with its entry, got %+v (ok=%v)", doc, ok)
	}
}

// TestCatalogSortedKeys_IsStableAndSorted pins the ordering every catalog report depends on.
func TestCatalogSortedKeys_IsStableAndSorted(t *testing.T) {
	cat := twoVersionCatalog()
	want := cat.SortedKeys()
	if !slices.IsSorted(want) {
		t.Fatalf("keys must be sorted, got %v", want)
	}
	for i := 0; i < repeatedRuns; i++ {
		if got := cat.SortedKeys(); !slices.Equal(got, want) {
			t.Fatalf("run %d returned %v, the first returned %v", i, got, want)
		}
	}
}
