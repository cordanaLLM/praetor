package dedupe

import (
	"encoding/json"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

type endLineLocation struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
	Root    string `json:"root"`
}

// endLinePair makes raw pairs without reading source files or running a scanner.
func endLinePair(a string, startA, endA int, b string, startB, endB, lines int) rawJscpdDuplicate {
	return rawJscpdDuplicate{
		Format: "python", Lines: lines,
		FirstFile:  rawJscpdFile{Name: filepath.Join("fixture", a), Start: startA, End: endA},
		SecondFile: rawJscpdFile{Name: filepath.Join("fixture", b), Start: startB, End: endB},
	}
}

// unequalEndLinePairs gives one merged copy and two shorter partner copies.
func unequalEndLinePairs() []rawJscpdDuplicate {
	return []rawJscpdDuplicate{
		endLinePair("a.py", 21, 34, "b.py", 33, 46, 14),
		endLinePair("a.py", 31, 45, "c.py", 163, 174, 14),
	}
}

// endLineLocations decodes the consumer-visible JSON, including each copy's end.
func endLineLocations(t *testing.T, locations []FileLocation) []endLineLocation {
	t.Helper()
	data, err := json.Marshal(locations)
	if err != nil {
		t.Fatal(err)
	}
	var got []endLineLocation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestDuplicateEndLine checks unequal spans and minimum, adjacent, and partner boundaries.
func TestDuplicateEndLine(t *testing.T) {
	cases := []struct {
		name  string
		pairs []rawJscpdDuplicate
		loc   int
		want  []endLineLocation
	}{
		{"positive_unequal_spans", unequalEndLinePairs(), 25, []endLineLocation{
			{"a.py", 21, 45, "fixture"}, {"b.py", 33, 46, "fixture"}, {"c.py", 163, 174, "fixture"},
		}},
		{"boundary_five_lines", []rawJscpdDuplicate{endLinePair("a.py", 7, 11, "b.py", 21, 25, 5)}, 5, []endLineLocation{
			{"a.py", 7, 11, "fixture"}, {"b.py", 21, 25, "fixture"},
		}},
		{"boundary_missing_end", []rawJscpdDuplicate{endLinePair("a.py", 7, 0, "b.py", 21, 0, 5)}, 5, []endLineLocation{
			{"a.py", 7, 11, "fixture"}, {"b.py", 21, 25, "fixture"},
		}},
		{"boundary_adjacent_nonpartners", []rawJscpdDuplicate{
			endLinePair("a.py", 1, 5, "b.py", 11, 15, 5), endLinePair("a.py", 6, 10, "c.py", 21, 25, 5),
		}, 10, []endLineLocation{{"a.py", 1, 10, "fixture"}, {"b.py", 11, 15, "fixture"}, {"c.py", 21, 25, "fixture"}}},
		{"boundary_adjacent_partners", []rawJscpdDuplicate{endLinePair("a.py", 1, 5, "a.py", 6, 10, 5)}, 5, []endLineLocation{
			{"a.py", 1, 5, "fixture"}, {"a.py", 6, 10, "fixture"},
		}},
		{"boundary_overlapping_partners", []rawJscpdDuplicate{endLinePair("a.py", 1, 5, "a.py", 4, 8, 5)}, 5, []endLineLocation{
			{"a.py", 1, 5, "fixture"}, {"a.py", 4, 8, "fixture"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := buildDuplicateGroup(tc.pairs, []rootMapping{{raw: "fixture", abs: "fixture"}}, true)
			if got := endLineLocations(t, group.Locations); !slices.Equal(got, tc.want) || group.LOC != tc.loc {
				t.Errorf("locations = %+v, LOC = %d; want %+v, LOC = %d", got, group.LOC, tc.want, tc.loc)
			}
		})
	}
}

// TestDuplicateEndLineHash excludes end lines from the pinned Root:Path:Line hash.
func TestDuplicateEndLineHash(t *testing.T) {
	const want = "51279eef4c75c6f537796d4527c1b738ec3201476ba88b12c60cbde5de2625cf"
	// SHA-256 of "python\nfixture:a.py:21\nfixture:b.py:33\nfixture:c.py:163\n".
	for _, tc := range []struct {
		name string
		end  int
	}{{"original_ends", 174}, {"changed_end", 180}} {
		t.Run(tc.name, func(t *testing.T) {
			pairs := unequalEndLinePairs()
			pairs[1].SecondFile.End = tc.end
			group := buildDuplicateGroup(pairs, []rootMapping{{raw: "fixture", abs: "fixture"}}, true)
			if group.Hash != want {
				t.Errorf("hash = %s, want Root:Path:Line baseline %s", group.Hash, want)
			}
		})
	}
}

// TestResolveFileLocationEndLine keeps supplied ends and leaves absent ends unknown.
func TestResolveFileLocationEndLine(t *testing.T) {
	cases := []struct {
		name  string
		file  rawJscpdFile
		cross bool
		want  endLineLocation
	}{
		{"mapped", rawJscpdFile{Name: filepath.Join("fixture", "a.py"), Start: 7, End: 11}, true, endLineLocation{"a.py", 7, 11, "fixture"}},
		{"local", rawJscpdFile{Name: filepath.Join("fixture", "a.py"), Start: 7, End: 11}, false, endLineLocation{"a.py", 7, 11, ""}},
		{"unmapped", rawJscpdFile{Name: "elsewhere.py", Start: 7, End: 11}, true, endLineLocation{"elsewhere.py", 7, 11, ""}},
		{"unknown_end", rawJscpdFile{Name: "elsewhere.py", Start: 7}, true, endLineLocation{"elsewhere.py", 7, 0, ""}},
		{"mapped_unknown_end", rawJscpdFile{Name: filepath.Join("fixture", "a.py"), Start: 7}, true, endLineLocation{"a.py", 7, 0, "fixture"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := resolveFileLocation(tc.file, []rootMapping{{raw: "fixture", abs: "fixture"}}, tc.cross)
			if got := endLineLocations(t, []FileLocation{loc})[0]; got != tc.want {
				t.Errorf("location = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestFileLocationEndLineJSON omits unknown ends while serializing known ends.
func TestFileLocationEndLineJSON(t *testing.T) {
	for _, tc := range []struct {
		name    string
		end     int
		present bool
	}{{"positive_known", 11, true}, {"negative_unknown_omitted", 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			loc := resolveFileLocation(rawJscpdFile{Name: "a.py", Start: 7, End: tc.end}, nil, false)
			data, err := json.Marshal(loc)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["end_line"]; present != tc.present {
				t.Errorf("end_line presence = %v, want %v: %s", present, tc.present, data)
			}
		})
	}
}

// TestScanGoFileEndLine records source ends, including bodies printed over more lines.
func TestScanGoFileEndLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		end    int
	}{
		{"multiline", "package fixture\nfunc clone() int {\n n := 1\n n++\n return n\n}\n", 6},
		{"compact", "package fixture\nfunc clone() int { n := 1; n++; return n }\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locations := scanGoEndLineCandidates(t, tc.source)
			want := []endLineLocation{{Path: "clone.go", Line: 2, EndLine: tc.end}}
			if got := endLineLocations(t, locations); !slices.Equal(got, want) {
				t.Errorf("locations = %+v, want %+v", got, want)
			}
		})
	}
}

// scanGoEndLineCandidates scans one fixture and requires a single clone candidate.
func scanGoEndLineCandidates(t *testing.T, source string) []FileLocation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clone.go")
	if err := contextopt.WriteSnapshot(t.Context(), path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	groups, lines := make(map[string][]FileLocation), make(map[string]int)
	var starts []adopterGitStart
	if err := scanGoFile(t.Context(), token.NewFileSet(), path, "clone.go", groups, lines, true, &starts, &DedupeReport{}); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("clone groups = %d, want 1", len(groups))
	}
	return slices.Collect(maps.Values(groups))[0]
}
