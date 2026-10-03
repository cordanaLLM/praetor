package editor

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// Issue #316: VS Code reads .vscode/settings.json, extensions.json, launch.json and tasks.json
// as JSON with Comments, so a commented file is not a broken one. These cases pin the three
// things that follow: a commented file holding every managed value is in sync, a commented
// file that lacks one is never rewritten, and no other editor path becomes tolerant.

// commentedSettings is a .vscode/settings.json as an adopter writes one: a header, a line
// comment, a block comment, a comment marker inside a value and a trailing comma.
const commentedSettings = `// workspace settings, see CONTRIBUTING.md
{
  /* formatting */
  "editor.formatOnSave": true, // the team agreed on this
  "files.associations": { "*.tpl": "html", },
  "search.exclude": { "**/vendor//**": true },
  "managed": 1,
}
`

// Positive: a commented .vscode file that holds every managed value is present under either
// rule, with nothing to write, and verification passes it with its bytes untouched.
func TestResolveExisting_Positive_CommentedVSCodeFileInSync(t *testing.T) {
	for _, name := range []string{"settings.json", "extensions.json", "launch.json", "tasks.json"} {
		file := GeneratedFile{Path: ".vscode/" + name, Editor: EditorVSCode, Content: `{"managed":1,"search.exclude":{"**/vendor//**":true}}` + "\n"}
		for _, keepDrift := range []bool{false, true} {
			got, err := ResolveExisting(file, []byte(commentedSettings), keepDrift)
			if err != nil {
				t.Fatalf("%s keepDrift=%v: %v", file.Path, keepDrift, err)
			}
			if got.Outcome != WritePresent || got.Content != "" || len(got.Added) != 0 {
				t.Errorf("%s keepDrift=%v: got %+v, want PRESENT with nothing to write", file.Path, keepDrift, got)
			}
		}
	}

	root := writeFixture(t, map[string]string{".vscode/settings.json": commentedSettings})
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`}}}
	report, err := WriteWithReport(set, root)
	if err != nil || len(report.Files) != 1 || report.Files[0].Outcome != WritePresent {
		t.Fatalf("generation of an in-sync commented file: %+v %v", report, err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); got != commentedSettings {
		t.Fatalf("generation changed an in-sync commented file:\n%s", got)
	}
	verification, err := VerifyWithReport(set, root)
	if err != nil || !slices.Contains(verification.Verified, ".vscode/settings.json") {
		t.Fatalf("verification refused an in-sync commented file: %+v %v", verification, err)
	}
}

// Negative: a commented .vscode file that lacks a managed value is refused, not rewritten. The
// refusal names the file, says the comments would be lost and names every value to add by hand.
func TestResolveExisting_Negative_CommentedVSCodeFileIsNotRewritten(t *testing.T) {
	file := GeneratedFile{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1,"added":{"x":true},"list":["a","b"]}` + "\n"}
	for name, existing := range map[string]string{
		"line comment":   "{\n  // adopter note\n  \"managed\": 1,\n  \"list\": [\"a\"]\n}\n",
		"block comment":  `{/* adopter note */"managed":1,"list":["a"]}`,
		"trailing comma": `{"managed":1,"list":["a",],}`,
	} {
		for _, keepDrift := range []bool{false, true} {
			got, err := ResolveExisting(file, []byte(existing), keepDrift)
			var commented *CommentedJSONError
			if !errors.As(err, &commented) || errors.Is(err, ErrExistingJSONInvalid) {
				t.Fatalf("%s keepDrift=%v: got %+v, %v, want a CommentedJSONError", name, keepDrift, got, err)
			}
			if want := []string{"/added", "/list/-"}; !slices.Equal(sortedCopy(commented.Lacks), want) {
				t.Errorf("%s: refusal lacks %v, want %v", name, commented.Lacks, want)
			}
			for _, want := range []string{"cannot safely merge existing .vscode/settings.json", "comments or trailing commas", "would lose", `"/added"`, `"/list/-"`, "by hand"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: refusal %q does not say %q", name, err, want)
				}
			}
			if got.Content != "" || got.Outcome != "" {
				t.Errorf("%s: a refused merge returned something to write: %+v", name, got)
			}
		}
	}

	// `editors generate` resolves every file before it writes one, so the refusal leaves the
	// commented file and every other file of the run as they were.
	existing := "{\n  // adopter note\n  \"managed\": 1\n}\n"
	root := writeFixture(t, map[string]string{".vscode/settings.json": existing})
	set := &EditorConfigSet{Files: []GeneratedFile{
		{Path: ".vscode/extensions.json", Editor: EditorVSCode, Content: `{"recommendations":[]}`},
		file,
	}}
	if _, err := WriteWithReport(set, root); err == nil || !strings.Contains(err.Error(), ".vscode/settings.json") || !strings.Contains(err.Error(), "comments") {
		t.Fatalf("generation did not refuse the commented file by name: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); got != existing {
		t.Fatalf("a refused merge changed the commented file:\n%s", got)
	}
	if fileExists(filepath.Join(root, ".vscode", "extensions.json")) {
		t.Fatal("a refused run wrote another file")
	}
	if err := Verify(&EditorConfigSet{Files: []GeneratedFile{file}}, root); err == nil || !strings.Contains(err.Error(), "missing managed standards policy") {
		t.Fatalf("verification of a commented file lacking a managed value: %v", err)
	}
}

// Boundary: the refusal is about comments a rewrite would lose and nothing else. A .vscode
// file without them merges as before, comment markers inside a string are data that survives
// the merge, and the same commented bytes at a path no editor reads as JSONC stay invalid.
func TestResolveExisting_Boundary_JSONCRefusalIsOnlyAboutComments(t *testing.T) {
	file := GeneratedFile{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: `{"managed":1,"added":true}`}
	plain := `{"managed":1,"glob":"**/*.go","note":"// kept /* as data */","list":",]"}`
	got, err := ResolveExisting(file, []byte(plain), false)
	if err != nil || got.Outcome != WriteMerged || !slices.Equal(got.Added, []string{"/added"}) {
		t.Fatalf("uncommented .vscode file: got %+v, %v, want MERGED /added", got, err)
	}
	for _, kept := range []string{`"glob": "**/*.go"`, `"note": "// kept /* as data */"`, `"list": ",]"`, `"added": true`} {
		if !strings.Contains(got.Content, kept) {
			t.Errorf("merged document lost %s:\n%s", kept, got.Content)
		}
	}

	commented := "{\n  // adopter note\n  \"managed\": 1,\n  \"added\": true\n}\n"
	for path, dialect := range map[string]strictjson.Dialect{
		".vscode/tasks.json":      strictjson.JSONC,
		".zed/settings.json":      strictjson.StrictJSON,
		".zed/tasks.json":         strictjson.StrictJSON,
		".fleet/settings.json":    strictjson.StrictJSON,
		"project.sublime-project": strictjson.StrictJSON,
	} {
		if got := strictjson.DialectOf(path); got != dialect {
			t.Fatalf("%s is read as %v, want %v", path, got, dialect)
		}
		candidate := GeneratedFile{Path: path, Content: file.Content}
		resolution, err := ResolveExisting(candidate, []byte(commented), false)
		if dialect == strictjson.JSONC {
			if err != nil || resolution.Outcome != WritePresent {
				t.Errorf("%s: commented file in sync: got %+v, %v", path, resolution, err)
			}
			continue
		}
		if !errors.Is(err, ErrExistingJSONInvalid) {
			t.Errorf("%s: a path read as strict JSON accepted a comment: %+v, %v", path, resolution, err)
		}
		root := writeFixture(t, map[string]string{path: commented})
		if err := Verify(&EditorConfigSet{Files: []GeneratedFile{candidate}}, root); err == nil || !strings.Contains(err.Error(), "cannot verify managed configuration") {
			t.Errorf("%s: verification of a commented strict-JSON path: %v", path, err)
		}
	}
}

// Boundary: every JSON file the VS Code family generates is at a path read as JSONC, and every
// other generated JSON file at a path read as strict JSON, so the dialect follows the editor
// and not the file extension.
func TestGeneratedJSONFiles_Boundary_OnlyVSCodePathsAreJSONC(t *testing.T) {
	set := mustSynthesize(t, DefaultOptions())
	jsonc, strict := 0, 0
	for _, file := range set.Files {
		if !isJSONEditorFile(file.Path) {
			continue
		}
		inVSCode := strings.HasPrefix(file.Path, ".vscode/")
		if got := strictjson.DialectOf(file.Path) == strictjson.JSONC; got != inVSCode {
			t.Errorf("%s: read as JSONC = %v, want %v", file.Path, got, inVSCode)
		}
		if inVSCode {
			jsonc++
		} else {
			strict++
		}
	}
	if jsonc != 3 || strict == 0 {
		t.Errorf("generated %d .vscode JSON files and %d others, want 3 and at least one", jsonc, strict)
	}
}
