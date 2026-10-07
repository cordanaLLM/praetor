package hisscoverage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// manualClaim is the coverage block of one rule entry: a manual claim needs no fixture.
const manualClaim = "    coverage:\n      - language: go\n        state: manual\n        rationale: upheld by review\n"

// driftedCoverage renders a coverage file in which HISS-01 to HISS-11 carry a title the HISS
// catalog does not state (every odd one with a line comment), HISS-12 repeats the catalog's
// title and HISS-13 omits it.
func driftedCoverage(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	out.WriteString("# adopter coverage\nversion: 1\nrules:\n")
	for n := 1; n <= 11; n++ {
		fmt.Fprintf(&out, "  - id: HISS-%02d\n    title: Earlier name %d", n, n)
		if n%2 == 1 {
			out.WriteString("  # kept")
		}
		out.WriteString("\n" + manualClaim)
	}
	registered, ok := hisscatalog.LookupRule("HISS-12")
	if !ok {
		t.Fatal("HISS-12 missing from the HISS catalog")
	}
	fmt.Fprintf(&out, "  - id: HISS-12\n    title: %q\n%s", registered.Title, manualClaim)
	out.WriteString("  - id: HISS-13\n" + manualClaim)
	return out.String()
}

// writeCoverage stages body as the coverage file of a fresh repository root.
func writeCoverage(t *testing.T, body string, perm os.FileMode) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(CatalogFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatalf("write coverage file: %v", err)
	}
	return root
}

// Positive: eleven drifted titles are rewritten to the catalog's, every other line -- the
// comments, the catalog's own title, the rule without a title -- stays byte for byte, and the
// result loads with no stale title left.
func TestSyncTitles_Positive_RewritesElevenDriftedTitles(t *testing.T) {
	before := driftedCoverage(t)
	result, err := SyncTitles([]byte(before))
	if err != nil {
		t.Fatalf("sync titles: %v", err)
	}
	if len(result.Stale) != 11 {
		t.Fatalf("want 11 stale titles, got %d: %+v", len(result.Stale), result.Stale)
	}
	after, err := parseCatalog(result.Content)
	if err != nil {
		t.Fatalf("the rewritten file must load: %v\n%s", err, result.Content)
	}
	if stale := after.StaleTitles(); len(stale) != 0 {
		t.Fatalf("no title may stay stale: %+v", stale)
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(string(result.Content), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("the rewrite changed the line count: %d -> %d", len(beforeLines), len(afterLines))
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] == afterLines[i] {
			continue
		}
		changed++
		if !strings.HasPrefix(afterLines[i], "    title: ") ||
			strings.HasSuffix(beforeLines[i], "# kept") != strings.HasSuffix(afterLines[i], "  # kept") {
			t.Errorf("line %d rewritten beyond its title value: %q -> %q", i+1, beforeLines[i], afterLines[i])
		}
	}
	if changed != 11 {
		t.Fatalf("want exactly the 11 title lines changed, got %d", changed)
	}
	for i := 0; i < len(after.Rules); i++ {
		if title := after.Rules[i].Title; title != "" && title != after.Rules[i].CatalogTitle() {
			t.Errorf("rule %s title %q, want the catalog's", after.Rules[i].ID, title)
		}
	}
}

// Positive: a file with no stale title comes back as the same bytes.
func TestSyncTitles_Positive_NothingStaleKeepsTheBytes(t *testing.T) {
	registered, _ := hisscatalog.LookupRule("HISS-02")
	body := fmt.Sprintf("version: 1\nrules:\n  - id: HISS-02\n    title: %q\n%s  - id: HISS-14\n%s",
		registered.Title, manualClaim, manualClaim)
	result, err := SyncTitles([]byte(body))
	if err != nil {
		t.Fatalf("sync titles: %v", err)
	}
	if len(result.Stale) != 0 || !bytes.Equal(result.Content, []byte(body)) {
		t.Fatalf("an in-sync file must come back unchanged: stale=%+v\n%s", result.Stale, result.Content)
	}
}

// Negative: a layout a one-line edit cannot rewrite is refused, never written differently: a
// block scalar, a title below its key, a flow-style rule, a plain title continued on the next
// line, and an aliased title.
func TestSyncTitles_Negative_RefusesLayoutsALineEditCannotRewrite(t *testing.T) {
	cases := map[string]string{
		"folded":    "  - id: HISS-14\n    title: >-\n      Append-Only\n      ABI\n" + manualClaim,
		"below key": "  - id: HISS-14\n    title:\n      Append-Only ABI\n" + manualClaim,
		"flow rule": "  - {id: HISS-14, title: Append-Only ABI, coverage: [{language: go, state: manual, rationale: r}]}\n",
		"continued": "  - id: HISS-14\n    title: Append-Only\n      ABI\n" + manualClaim,
		"alias":     "  - id: HISS-01\n    title: &old Append-Only ABI\n" + manualClaim + "  - id: HISS-14\n    title: *old\n" + manualClaim,
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := SyncTitles([]byte("version: 1\nrules:\n" + rules))
			if !errors.Is(err, ErrTitleNotRewritable) {
				t.Fatalf("want ErrTitleNotRewritable, got %v", err)
			}
		})
	}
}

// Negative: the sync refuses a file LoadCatalog refuses; an unregistered rule id still fails.
func TestSyncTitles_Negative_UnregisteredRuleStillFails(t *testing.T) {
	_, err := SyncTitles([]byte("version: 1\nrules:\n  - id: HISS-99\n    title: Invented\n" + manualClaim))
	if !errors.Is(err, ErrInvalidCatalog) || !strings.Contains(err.Error(), "not a registered invariant") {
		t.Fatalf("want the unregistered id refused, got %v", err)
	}
}

// Boundary: a title as the first key of its list item, single- and double-quoted titles, a line
// comment and CRLF line endings survive the rewrite.
func TestSyncTitles_Boundary_KeepsQuotesCommentsAndLineEndings(t *testing.T) {
	body := strings.Join([]string{
		"version: 1",
		"rules:",
		"  - title: 'Earlier'  # first key",
		"    id: HISS-02",
		strings.TrimSuffix(manualClaim, "\n"),
		`  - id: HISS-14`,
		`    title: "Append-Only ABI" # quoted`,
		strings.TrimSuffix(manualClaim, "\n"),
		"",
	}, "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	result, err := SyncTitles([]byte(body))
	if err != nil {
		t.Fatalf("sync titles: %v", err)
	}
	out := string(result.Content)
	if strings.Count(out, "\n") != strings.Count(out, "\r\n") {
		t.Fatalf("every line must keep its CRLF ending:\n%q", out)
	}
	loops, _ := hisscatalog.LookupRule("HISS-02")
	abi, _ := hisscatalog.LookupRule("HISS-14")
	for _, want := range []string{
		"  - title: " + loops.Title + "  # first key\r\n    id: HISS-02\r\n",
		"    title: " + abi.Title + " # quoted\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Boundary: SyncTitlesFile is a dry run unless write is set. The dry run names the stale titles
// and leaves the file alone; the write replaces it, keeps its permission bits, and a second
// write finds nothing to do and writes nothing.
func TestSyncTitlesFile_Boundary_DryRunUnlessWrite(t *testing.T) {
	before := driftedCoverage(t)
	root := writeCoverage(t, before, 0o600)
	path := filepath.Join(root, filepath.FromSlash(CatalogFile))

	dry, err := SyncTitlesFile(t.Context(), root, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Written || len(dry.Stale) != 11 {
		t.Fatalf("dry run must name 11 stale titles and write nothing: written=%v stale=%d", dry.Written, len(dry.Stale))
	}
	if data, _ := os.ReadFile(path); string(data) != before {
		t.Fatal("a dry run must leave the file untouched")
	}

	written, err := SyncTitlesFile(t.Context(), root, true)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !written.Written {
		t.Fatal("write must replace a file holding stale titles")
	}
	catalog, err := LoadCatalog(t.Context(), root)
	if err != nil || len(catalog.StaleTitles()) != 0 {
		t.Fatalf("the written file must load with no stale title: %v", err)
	}
	if runtime.GOOS != "windows" { // Windows reports no Unix permission bits to compare.
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("the rewrite must keep the file's 0600 bits: %v %v", info, err)
		}
	}

	again, err := SyncTitlesFile(t.Context(), root, true)
	if err != nil || again.Written || len(again.Stale) != 0 {
		t.Fatalf("an in-sync file must not be written again: %+v %v", again, err)
	}
}

// Negative: without a coverage file the sync reports the absent catalog.
func TestSyncTitlesFile_Negative_AbsentCatalog(t *testing.T) {
	if _, err := SyncTitlesFile(t.Context(), t.TempDir(), true); !errors.Is(err, ErrCatalogAbsent) {
		t.Fatalf("want ErrCatalogAbsent, got %v", err)
	}
}
