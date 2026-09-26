package needs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The deprecated replacement key is read in both encodings: alone it is adopted with a
// deprecation, beside an equal new key it is harmless, and beside a different one it is an
// error; writing always uses the new key.
func TestDemandReplacementAliasMatrix(t *testing.T) {
	const newValue, oldValue = "example.com/acme/kit/db", "example.com/acme/kit/legacy"
	cases := []struct {
		name, newKey, oldKey string
		want                 string
		deprecated, fails    bool
	}{
		{"new key only", newValue, "", newValue, false, false},
		{"deprecated key only", "", oldValue, oldValue, true, false},
		{"both keys agree", newValue, newValue, newValue, true, false},
		{"both keys differ", newValue, oldValue, "", false, true},
		{"neither key", "", "", "", false, false},
	}
	for _, tc := range cases {
		for encoding, decode := range map[string]func(string, string) (RepoNeeds, error){"yaml": decodeAliasYAML, "json": decodeAliasJSON} {
			row, err := decode(tc.newKey, tc.oldKey)
			if tc.fails {
				if err == nil || !strings.Contains(err.Error(), legacyReplacementKey) {
					t.Errorf("%s/%s: err = %v, want a conflict naming the deprecated key", tc.name, encoding, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s/%s: %v", tc.name, encoding, err)
			}
			if got := row.Dependencies[0].FrameworkReplacement; got != tc.want {
				t.Errorf("%s/%s: replacement %q, want %q", tc.name, encoding, got, tc.want)
			}
			if deprecated := len(row.Deprecations) == 1 && row.Deprecations[0] == legacyReplacementDeprecation; deprecated != tc.deprecated {
				t.Errorf("%s/%s: deprecations %v, want deprecated=%v", tc.name, encoding, row.Deprecations, tc.deprecated)
			}
			written, err := yaml.Marshal(&row)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(written), legacyReplacementKey) || strings.Contains(string(written), "deprecations") {
				t.Errorf("%s/%s: a written manifest carries the deprecated key or the warning:\n%s", tc.name, encoding, written)
			}
		}
	}
}

func aliasDemand(newKey, oldKey string) map[string]string {
	demand := map[string]string{"package": "github.com/jackc/pgx/v5", "capability": "db.postgres", "status": "covered"}
	if newKey != "" {
		demand["framework_replacement"] = newKey
	}
	if oldKey != "" {
		demand[legacyReplacementKey] = oldKey
	}
	return demand
}

func decodeAliasYAML(newKey, oldKey string) (RepoNeeds, error) {
	body, err := yaml.Marshal(map[string]any{"version": 1, "repository": "example.com/app", "dependencies": []any{aliasDemand(newKey, oldKey)}})
	if err != nil {
		return RepoNeeds{}, fmt.Errorf("marshal fixture: %w", err)
	}
	var row RepoNeeds
	err = yaml.Unmarshal(body, &row)
	return row, err
}

func decodeAliasJSON(newKey, oldKey string) (RepoNeeds, error) {
	body, err := json.Marshal(map[string]any{"version": 1, "repository": "example.com/app", "dependencies": []any{aliasDemand(newKey, oldKey)}})
	if err != nil {
		return RepoNeeds{}, fmt.Errorf("marshal fixture: %w", err)
	}
	var row RepoNeeds
	err = json.Unmarshal(body, &row)
	return row, err
}

// Decoding goes through the struct itself, so no field is dropped: a fleet report row keeps
// its sub-project lists and standard-library imports across a JSON round trip.
func TestRepoNeedsDecodeKeepsEveryField(t *testing.T) {
	row := RepoNeeds{Version: 1, Repository: "example.com/app", Language: "go", Framework: "example.com/acme/kit",
		BuilderKits: []string{"acme/kit"}, Path: "/srv/app", Subprojects: []string{"web"}, UnscannedSubprojects: []string{"a/b/c/d/e"},
		FailedSubprojects:      []SubprojectFailure{{Dir: "tools", Error: "parse"}},
		Dependencies:           []DependencyDemand{{Package: "github.com/jackc/pgx/v5", Capability: "db.postgres", Status: StatusCovered, FrameworkReplacement: "example.com/acme/kit/db"}},
		StandardLibraryImports: []DependencyDemand{{Package: "log/slog", Capability: "telemetry.logging", Status: StatusNative}},
		Deprecations:           []string{"note"}}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RepoNeeds
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, row) {
		t.Fatalf("JSON round trip changed the row:\n got %+v\nwant %+v", decoded, row)
	}
	var fromYAML RepoNeeds
	if err := yaml.Unmarshal([]byte("dependencies: [{package: a, "+legacyReplacementKey+": [not, a, string]}]\n"), &fromYAML); err == nil {
		t.Fatal("a malformed deprecated key decoded")
	}
}

// A scan reading a .needs.yaml written with the deprecated key reports the deprecation on
// the new row.
func TestScanReportsDeprecatedNeedsKey(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.com/app\n\ngo 1.27\n")
	writeFixture(t, repo, ".needs.yaml", "version: 1\nrepository: example.com/app\ndependencies:\n  - package: github.com/jackc/pgx/v5\n"+
		"    capability: db.postgres\n    status: covered\n    "+legacyReplacementKey+": example.com/acme/kit/db\n")
	report, err := ScanRepo(t.Context(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deprecations) != 1 || !strings.Contains(FormatDeprecations(report), "Deprecated input: the .needs.yaml key "+legacyReplacementKey) {
		t.Fatalf("deprecations = %v", report.Deprecations)
	}
	writeFixture(t, repo, ".needs.yaml", "version: 1\nrepository: example.com/app\n")
	if clean, err := ScanRepo(t.Context(), repo, nil); err != nil || len(clean.Deprecations) != 0 || FormatDeprecations(clean) != "" {
		t.Fatalf("a current manifest reported deprecations: %v, %v", clean, err)
	}
}
