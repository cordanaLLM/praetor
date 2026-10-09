package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// scaffoldGuide is the adoption guide whose scaffold table lists the adoption steps (#955).
var scaffoldGuide = filepath.Join("..", "..", "docs", "adoption.md")

const (
	scaffoldHeading = "### What Adoption Scaffolds Automatically"
	scaffoldRowOpen = "| `"
)

// scaffoldTableSteps returns, in order, the step names the first cell of each row of the
// scaffold table holds, using testsupport.MarkdownTableRowsUnderHeading.
func scaffoldTableSteps(text string) []string {
	rows := testsupport.MarkdownTableRowsUnderHeading(text, scaffoldHeading)
	steps := make([]string, 0, len(rows))
	for _, cells := range rows {
		if len(cells) > 0 {
			steps = append(steps, strings.Trim(cells[0], "`"))
		}
	}
	return steps
}

// Positive: the scaffold table lists exactly the steps of the adoption chain, in order, so a
// step added to adoptSteps without a row, or a row for a removed step, fails here. It also
// verifies that the "What it writes" column is non-empty for every step in the chain.
func TestAdoptionScaffoldTable_MatchesTheStepChain(t *testing.T) {
	data, err := os.ReadFile(scaffoldGuide)
	if err != nil {
		t.Fatal(err)
	}
	rows := testsupport.MarkdownTableRowsUnderHeading(string(data), scaffoldHeading)
	steps := make([]string, 0, len(rows))
	for _, cells := range rows {
		if len(cells) < 2 || strings.TrimSpace(cells[1]) == "" {
			t.Errorf("step %s has empty 'What it writes' column in scaffold table", cells[0])
		}
		steps = append(steps, strings.Trim(cells[0], "`"))
	}
	want := adoptStepNames()
	if !slices.Equal(steps, want) {
		t.Fatalf("docs/adoption.md scaffold table lists %v; adoptSteps runs %v", steps, want)
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
