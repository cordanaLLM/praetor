package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// scaffoldGuide is the adoption guide whose scaffold table lists the adoption steps (#955).
var scaffoldGuide = filepath.Join("..", "..", "docs", "adoption.md")

const (
	scaffoldHeading = "### What Adoption Scaffolds Automatically"
	scaffoldRowOpen = "| `"
	maxGuideLines   = 20000
)

// scaffoldTableSteps returns, in order, the step names the first cell of each row of the
// scaffold table holds. The table is the rows between scaffoldHeading and the next heading.
func scaffoldTableSteps(text string) []string {
	_, section, found := strings.Cut(text, scaffoldHeading)
	if !found {
		return nil
	}
	var steps []string
	lines := strings.Split(section, "\n")
	for i := 0; i < len(lines) && i < maxGuideLines; i++ {
		if strings.HasPrefix(lines[i], "#") {
			break
		}
		if !strings.HasPrefix(lines[i], scaffoldRowOpen) {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(lines[i], scaffoldRowOpen), "`")
		steps = append(steps, name)
	}
	return steps
}

// Positive: the scaffold table lists exactly the steps of the adoption chain, in order, so a
// step added to adoptSteps without a row, or a row for a removed step, fails here.
func TestAdoptionScaffoldTable_MatchesTheStepChain(t *testing.T) {
	data, err := os.ReadFile(scaffoldGuide)
	if err != nil {
		t.Fatal(err)
	}
	got, want := scaffoldTableSteps(string(data)), adoptStepNames()
	if !slices.Equal(got, want) {
		t.Fatalf("docs/adoption.md scaffold table lists %v; adoptSteps runs %v", got, want)
	}
}

// Negative and boundary: a table missing a step, one naming a step that does not exist, a
// document without the section and an empty table are each told apart from the chain.
func TestAdoptionScaffoldTable_RefusesDriftedTables(t *testing.T) {
	chain := adoptStepNames()
	rows := func(names []string) string {
		var b strings.Builder
		b.WriteString(scaffoldHeading + "\n\n| Step | Files |\n| :-- | :-- |\n")
		for _, name := range names {
			b.WriteString(scaffoldRowOpen + name + "` | files |\n")
		}
		b.WriteString("\n### Next section\n| `ignored` | after the section |\n")
		return b.String()
	}
	if got := scaffoldTableSteps(rows(chain)); !slices.Equal(got, chain) {
		t.Fatalf("a complete table read as %v, want %v", got, chain)
	}
	if got := scaffoldTableSteps(rows(chain[1:])); slices.Equal(got, chain) {
		t.Fatal("a table missing the first step matched the chain")
	}
	if got := scaffoldTableSteps(rows(append(slices.Clone(chain), "invented-step"))); slices.Equal(got, chain) {
		t.Fatal("a table naming a step that does not exist matched the chain")
	}
	if got := scaffoldTableSteps("no scaffold section here"); len(got) != 0 {
		t.Fatalf("a document without the section read as %v", got)
	}
	if got := scaffoldTableSteps(rows(nil)); len(got) != 0 {
		t.Fatalf("an empty table read as %v", got)
	}
}
