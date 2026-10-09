package flavor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// flavorGuide is the onboarding guide whose flavor table lists the built-in flavors (#955).
var flavorGuide = filepath.Join("..", "..", "docs", "guides", "onboarding.md")

const (
	flavorHeading = "## Flavors"
	flavorRowOpen = "| `"
	maxGuideLines = 20000
)

// flavorTableRows returns, in order, "name profile" for each row of the flavor table: the rows
// between flavorHeading and the next heading.
func flavorTableRows(text string) []string {
	// The guide is checked out with CRLF line endings on Windows (`* text=auto`).
	text = strings.ReplaceAll(text, "\r\n", "\n")
	_, section, found := strings.Cut(text, flavorHeading+"\n")
	if !found {
		return nil
	}
	var rows []string
	lines := strings.Split(section, "\n")
	for i := 0; i < len(lines) && i < maxGuideLines; i++ {
		if strings.HasPrefix(lines[i], "#") {
			break
		}
		if !strings.HasPrefix(lines[i], flavorRowOpen) {
			continue
		}
		cells := strings.Split(lines[i], "|")
		if len(cells) < 4 {
			continue
		}
		rows = append(rows, strings.Trim(strings.TrimSpace(cells[1]), "`")+" "+strings.Trim(strings.TrimSpace(cells[2]), "`"))
	}
	return rows
}

func builtinFlavorRows() []string {
	var rows []string
	for _, f := range builtinFlavorList() {
		rows = append(rows, f.Name()+" "+f.HISSProfile())
	}
	return rows
}

// Positive: the flavor table lists every built-in flavor with its profile, in detection order.
func TestFlavorTable_MatchesTheBuiltinFlavors(t *testing.T) {
	data, err := os.ReadFile(flavorGuide)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flavorTableRows(string(data)), builtinFlavorRows(); !slices.Equal(got, want) {
		t.Fatalf("docs/guides/onboarding.md flavor table lists %v; builtinFlavorList is %v", got, want)
	}
}

// Negative and boundary: a table missing a flavor, one with a wrong profile, a document without
// the section and an empty table are each told apart from the built-in list.
func TestFlavorTable_RefusesDriftedTables(t *testing.T) {
	want := builtinFlavorRows()
	render := func(rows []string) string {
		var b strings.Builder
		b.WriteString(flavorHeading + "\n\n| Flavor | Profile | Detected by |\n| :-- | :-- | :-- |\n")
		for _, row := range rows {
			name, profile, _ := strings.Cut(row, " ")
			b.WriteString(flavorRowOpen + name + "` | `" + profile + "` | markers |\n")
		}
		b.WriteString("\n## Next section\n| `ignored` | `after` | the section |\n")
		return b.String()
	}
	if got := flavorTableRows(render(want)); !slices.Equal(got, want) {
		t.Fatalf("a complete table read as %v, want %v", got, want)
	}
	if got := flavorTableRows(render(want[1:])); slices.Equal(got, want) {
		t.Fatal("a table missing the first flavor matched")
	}
	wrong := slices.Clone(want)
	wrong[0] = strings.Split(wrong[0], " ")[0] + " not-a-profile"
	if got := flavorTableRows(render(wrong)); slices.Equal(got, want) {
		t.Fatal("a table with a wrong profile matched")
	}
	if got := flavorTableRows("no flavor section"); len(got) != 0 {
		t.Fatalf("a document without the section read as %v", got)
	}
	if got := flavorTableRows(render(nil)); len(got) != 0 {
		t.Fatalf("an empty table read as %v", got)
	}
}

// Boundary (HISS-21): a CRLF checkout of the guide yields the same rows as an LF one.
func TestFlavorTableRows_Boundary_CRLFCheckout(t *testing.T) {
	lf := flavorHeading + "\n\n| `a` | `p` | x |\n| `b` | `q` | y |\n\n## Next\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if got, want := flavorTableRows(crlf), flavorTableRows(lf); !slices.Equal(got, want) || len(got) != 2 {
		t.Fatalf("CRLF rows %v differ from LF rows %v", got, want)
	}
}
