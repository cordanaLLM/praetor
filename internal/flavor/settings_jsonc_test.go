package flavor_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// Issue #316: VS Code documents .vscode/settings.json as JSON with Comments, and the audit
// parsed it as strict JSON, so one "//" note counted the file invalid and named it under
// Missing or Invalid Settings. These cases run the audit itself on such a repository.

// commentedVSCodeSettings is a workspace settings file with a header, a line comment, a block
// comment, a comment marker inside a value and a trailing comma.
const commentedVSCodeSettings = `// workspace settings
{
  /* interpreter */
  "python.defaultInterpreterPath": ".venv/bin/python", // local venv
  "files.exclude": { "**/__pycache__//**": true, },
}
`

// pythonML is a python-ml checkout: 1 template and 1 setting, so the setting alone decides
// between 100 and a failing 50.
func pythonML(t *testing.T, settings string) string {
	t.Helper()
	return repoWithFiles(t, map[string]string{
		"ruff.toml":             "line-length = 100\n",
		".vscode/settings.json": settings,
	})
}

func TestAuditFlavor_Positive_CommentedVSCodeSettingsAreValid(t *testing.T) {
	emptyPATH(t)
	report, err := flavor.AuditFlavor(pythonML(t, commentedVSCodeSettings), "python-ml")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Score != 100.0 || !report.Passed || report.SettingsValid != report.SettingsTotal {
		t.Fatalf("a commented settings.json must score 100 and pass, got score %v passed %v settings %d/%d",
			report.Score, report.Passed, report.SettingsValid, report.SettingsTotal)
	}
	if paths := report.InvalidSettingPaths(); len(paths) != 0 {
		t.Fatalf("a commented settings.json was named invalid: %v", paths)
	}

	// The same file in a flavor whose bar tolerates one invalid setting: nothing is reported.
	report, err = flavor.AuditFlavor(conformingNativeGPU(t, map[string]string{".vscode/settings.json": commentedVSCodeSettings}), "native-gpu-systems")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Score != 100.0 || len(report.MissingSettings) != 0 {
		t.Fatalf("native-gpu-systems with commented settings: score %v, missing %+v", report.Score, report.MissingSettings)
	}
}

// Negative: what JSON with Comments does not add still counts the settings file invalid and
// names it, and a comment in a file nothing documents as JSONC stays invalid.
func TestAuditFlavor_Negative_JSONCDoesNotExcuseABrokenSettingsFile(t *testing.T) {
	emptyPATH(t)
	for name, settings := range map[string]string{
		"duplicate key":              "{\n  // twice\n  \"editor.tabSize\": 2,\n  \"editor.tabSize\": 4\n}\n",
		"unterminated block comment": "{\n  \"editor.tabSize\": 2 /* never closed\n}\n",
		"comma after no value":       "{,}",
		"hash comment":               "{\n  # not JSONC\n  \"editor.tabSize\": 2\n}\n",
		"array":                      "// a list is not a settings object\n[\"editor.tabSize\"]\n",
	} {
		report, err := flavor.AuditFlavor(pythonML(t, settings), "python-ml")
		if err != nil {
			t.Fatalf("%s: audit: %v", name, err)
		}
		if report.Score != 50.0 || report.Passed || !slices.Equal(report.InvalidSettingPaths(), []string{".vscode/settings.json"}) {
			t.Errorf("%s: want score 50, failed and .vscode/settings.json named, got score %v passed %v invalid %v",
				name, report.Score, report.Passed, report.InvalidSettingPaths())
		}
	}

	repo := conformingNativeGPU(t, map[string]string{".github/rulesets/main.json": "{\n  // operator note\n  \"name\": \"main\",\n}\n"})
	report, err := flavor.AuditFlavor(repo, "native-gpu-systems")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !slices.Equal(report.InvalidSettingPaths(), []string{".github/rulesets/main.json"}) {
		t.Fatalf("a commented ruleset is not JSON its consumer reads and must be named, got %v", report.InvalidSettingPaths())
	}
}

// Boundary: comments configure nothing. A settings file whose object is empty once its
// comments are set aside is as invalid as `{}`, a single trailing comma after one member is
// valid, and the read bound counts the comment bytes.
func TestSettingSatisfied_Boundary_JSONCSettings(t *testing.T) {
	setting := settingsFor(t, "python-ml", ".vscode/settings.json")[0]
	const maxSettingBytes = 1 << 20
	member := `{"a":1}`
	cases := []struct {
		name     string
		settings string
		want     bool
	}{
		{"only comments around an empty object", "// nothing yet\n{ /* todo */ }\n", false},
		{"commented-out member", "{\n  // \"editor.tabSize\": 2\n}\n", false},
		{"one member, trailing comma", `{"editor.tabSize":2,}`, true},
		{"one member under a comment", "// tabs\n{\"editor.tabSize\":2}", true},
		{"comment filling the file to the read bound", member + "//" + strings.Repeat("x", maxSettingBytes-len(member)-2), true},
		{"comment one byte past the read bound", member + "//" + strings.Repeat("x", maxSettingBytes-len(member)-1), false},
	}
	for _, tc := range cases {
		if got := flavor.SettingSatisfied(pythonML(t, tc.settings), setting); got != tc.want {
			t.Errorf("%s: satisfied = %v, want %v", tc.name, got, tc.want)
		}
	}
}
