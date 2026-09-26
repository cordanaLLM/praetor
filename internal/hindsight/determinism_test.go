package hindsight

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/docdistill"
)

// determinismRuns repeats a map-ordered assertion. One pass proves nothing: Go randomises
// where a map range starts, so an unsorted result can agree with itself by chance.
const determinismRuns = 32

func seedCatalog(t *testing.T, repoPath string) {
	t.Helper()
	cat := &docdistill.DocCatalog{
		Version: docdistill.CatalogVersion,
		Packages: map[string]docdistill.DistilledDoc{
			"gopkg.in/yaml.v3@v3.0.1": {PackageName: "gopkg.in/yaml.v3", Version: "v3.0.1", Summary: "YAML"},
			"github.com/acme/lib@v1":  {PackageName: "github.com/acme/lib", Version: "v1", Summary: "lib"},
			"github.com/acme/cli@v2":  {PackageName: "github.com/acme/cli", Version: "v2", Summary: "cli"},
			"github.com/acme/api@v3":  {PackageName: "github.com/acme/api", Version: "v3", Summary: "api"},
		},
	}
	if err := docdistill.SaveCatalog(repoPath, cat); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
}

func factIDs(facts []MemoryFact) []string {
	ids := make([]string, 0, len(facts))
	for i := 0; i < len(facts); i++ {
		ids = append(ids, facts[i].ID)
	}
	return ids
}

// TestDistillPackageDocFacts_Positive_StableAcrossRuns is the row's claim: two
// distillations of one unchanged tree must produce the same fact file, not a cache that
// diffs against itself.
func TestDistillPackageDocFacts_Positive_StableAcrossRuns(t *testing.T) {
	repo := t.TempDir()
	seedCatalog(t, repo)

	first, err := distillPackageDocFacts(repo)
	if err != nil {
		t.Fatalf("distill package doc facts: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("expected one fact per cached package, got %d", len(first))
	}
	want := factIDs(first)
	for i := 0; i < determinismRuns; i++ {
		again, err := distillPackageDocFacts(repo)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if !slices.Equal(factIDs(again), want) {
			t.Fatalf("run %d produced %v, the first produced %v", i, factIDs(again), want)
		}
	}
}

// TestDistillPackageDocFacts_Boundary_EmptyAndSingleCatalog covers the two smallest
// catalogs, where an ordering defect cannot show itself and the count still must be right.
func TestDistillPackageDocFacts_Boundary_EmptyAndSingleCatalog(t *testing.T) {
	empty := t.TempDir()
	facts, err := distillPackageDocFacts(empty)
	if err != nil {
		t.Fatalf("a repository with no catalog is not an error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("an absent catalog yields no facts, got %d", len(facts))
	}

	single := t.TempDir()
	one := &docdistill.DocCatalog{
		Version:  docdistill.CatalogVersion,
		Packages: map[string]docdistill.DistilledDoc{"only@v1": {PackageName: "only", Version: "v1"}},
	}
	if err := docdistill.SaveCatalog(single, one); err != nil {
		t.Fatalf("seed single catalog: %v", err)
	}
	facts, err = distillPackageDocFacts(single)
	if err != nil || len(facts) != 1 {
		t.Errorf("a one-entry catalog yields one fact, got %d facts, err=%v", len(facts), err)
	}
}

// TestSortedCategories_3D covers the tally helper every report now prints through.
func TestSortedCategories_3D(t *testing.T) {
	tally := map[FactCategory]int{
		CategoryGovernance:    3,
		CategoryArchitecture:  1,
		CategoryFlavor:        2,
		CategoryDependencyDoc: 4,
	}
	want := SortedCategories(tally)
	if !slices.IsSorted(want) {
		t.Fatalf("categories must be sorted, got %v", want)
	}
	for i := 0; i < determinismRuns; i++ {
		if got := SortedCategories(tally); !slices.Equal(got, want) {
			t.Fatalf("run %d returned %v, the first returned %v", i, got, want)
		}
	}

	if got := SortedCategories(nil); len(got) != 0 {
		t.Errorf("a nil tally has no categories, got %v", got)
	}
	single := map[FactCategory]int{CategoryFlavor: 1}
	if got := SortedCategories(single); len(got) != 1 || got[0] != CategoryFlavor {
		t.Errorf("a one-entry tally returns its category, got %v", got)
	}
}
