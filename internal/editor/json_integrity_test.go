package editor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// =========================================================================
// BUG-776: existing editor JSON keeps exact values and rejects duplicate keys
// =========================================================================

func TestEditorJSONMergePreservesNumbersAndRejectsDuplicateKeys(t *testing.T) {
	desired := `{"files.insertFinalNewline":true}`
	root := writeFixture(t, map[string]string{
		".vscode/settings.json": `{"editor.fontSize":9007199254740993}`,
	})
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Content: desired}}}
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	merged := mustRead(t, filepath.Join(root, ".vscode", "settings.json"))
	if !strings.Contains(merged, "9007199254740993") {
		t.Fatalf("JSON merge changed an unrelated integer: %s", merged)
	}

	duplicate := `{"editor.fontSize":41,"editor.fontSize":42}`
	writeTestFile(t, root, ".vscode/settings.json", duplicate)
	if err := Write(set, root); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ambiguous existing JSON was accepted: %v", err)
	}
	if after := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); after != duplicate {
		t.Fatalf("duplicate-key rejection changed existing JSON: %s", after)
	}
	if err := Verify(set, root); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("verification accepted ambiguous duplicate keys: %v", err)
	}
}

func TestEditorWriteMergesJSONAndPreservesHumanFiles(t *testing.T) {
	root := writeFixture(t, map[string]string{
		".vscode/settings.json":   `{"editor.fontSize":42}`,
		".vscode/extensions.json": `{"recommendations":["human.extension"],"unwantedRecommendations":["bad.extension"]}`,
		".vscode/tasks.json":      `{"version":"2.0.0","tasks":[{"label":"Human","type":"shell","command":"echo human"}]}`,
		".editorconfig":           "root = false\n# human policy\n",
	})
	opts := DefaultOptions()
	opts.Editors = []string{EditorVSCode, EditorUniversal}
	set := mustSynthesize(t, opts)
	report, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, report, map[string]WriteOutcome{
		".editorconfig": WritePreserved, ".vscode/settings.json": WriteMerged,
		".vscode/extensions.json": WriteMerged, ".vscode/tasks.json": WriteMerged,
	})
	assertContainsFile(t, root, ".vscode/settings.json", `"editor.fontSize": 42`, `"standards.lsp.enabled": true`)
	assertContainsFile(t, root, ".vscode/extensions.json", "human.extension", "unwantedRecommendations", "golang.go")
	assertContainsFile(t, root, ".vscode/tasks.json", "echo human", "make verify-all")
	if got := mustRead(t, filepath.Join(root, ".editorconfig")); got != "root = false\n# human policy\n" {
		t.Fatalf("human non-JSON config changed: %q", got)
	}
	verification, err := VerifyWithReport(set, root)
	if err != nil {
		t.Fatalf("merged configuration did not verify: %v", err)
	}
	if !slices.Equal(verification.PreservedUnverified, []string{".editorconfig"}) || slices.Contains(verification.Verified, ".editorconfig") || len(verification.Verified) != 3 {
		t.Fatalf("preserved non-JSON file was not reported separately: %+v", verification)
	}

	// Boundary: a second run over the merged workspace changes nothing.
	again, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, again, map[string]WriteOutcome{
		".editorconfig": WritePreserved, ".vscode/settings.json": WritePresent,
		".vscode/extensions.json": WritePresent, ".vscode/tasks.json": WritePresent,
	})
}

func TestEditorWriteRejectsManagedConflictBeforeAnyMutation(t *testing.T) {
	root := t.TempDir()
	opts := DefaultOptions()
	opts.Editors = []string{EditorVSCode}
	set := mustSynthesize(t, opts)
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, ".vscode", "settings.json")
	for _, conflict := range []string{
		`{"standards.lsp.enabled":false,"human":true}`,
		`{"[go]":["not","an","object"]}`,
		"// comments are not strict JSON\n{}",
		`{"standards.lsp.enabled":true} {"second":"document"}`,
	} {
		writeTestFile(t, root, ".vscode/settings.json", conflict)
		conflictingSet := &EditorConfigSet{Files: append([]GeneratedFile{{Path: "would-have-been-created.json", Content: "{}\n"}}, set.Files...)}
		if err := Write(conflictingSet, root); err == nil || !strings.Contains(err.Error(), "cannot safely merge") {
			t.Fatalf("managed conflict %q was not rejected: %v", conflict, err)
		}
		if after := mustRead(t, settingsPath); after != conflict {
			t.Fatalf("conflicting human configuration was mutated: %s", after)
		}
		if _, err := os.Stat(filepath.Join(root, "would-have-been-created.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("conflict left a partial workspace mutation: %v", err)
		}
		if err := Verify(set, root); err == nil {
			t.Fatalf("managed conflict %q unexpectedly verified", conflict)
		}
	}
}

// =========================================================================
// BUG-778: preserved non-JSON files are reported as preserved-but-unverified
// =========================================================================

func TestEditorVerifyReportsPreservedNonJSONAsUnverified(t *testing.T) {
	root := t.TempDir()
	opts := DefaultOptions()
	opts.Editors = []string{EditorUniversal, EditorVisualStudio, EditorEmacs}
	set := mustSynthesize(t, opts)
	created, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, created, map[string]WriteOutcome{".editorconfig": WriteCreated, ".clang-tidy": WriteCreated, ".dir-locals.el": WriteCreated})

	// Boundary: an untouched generation has zero preserved files.
	clean, err := VerifyWithReport(set, root)
	if err != nil || len(clean.Verified) != 3 || len(clean.PreservedUnverified) != 0 {
		t.Fatalf("generated files were not all verified: %+v %v", clean, err)
	}

	writeTestFile(t, root, ".editorconfig", "root = false\n")
	writeTestFile(t, root, ".clang-tidy", "Checks: '-*'\n")
	report, err := VerifyWithReport(set, root)
	if err != nil {
		t.Fatalf("preserved human files failed verification: %v", err)
	}
	if !slices.Equal(report.PreservedUnverified, []string{".editorconfig", ".clang-tidy"}) || !slices.Equal(report.Verified, []string{".dir-locals.el"}) {
		t.Fatalf("preserved files were counted as verified: %+v", report)
	}

	// Negative: a differing template that Write rewrites is out of sync, not preserved.
	writeTestFile(t, root, ".dir-locals.el", "; human managed\n")
	if err := Verify(set, root); err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("differing rewritable template verified: %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".editorconfig")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWithReport(set, root); err == nil || !strings.Contains(err.Error(), "missing expected configuration file .editorconfig") {
		t.Fatalf("missing preserved file was not reported: %v", err)
	}
}

func TestEditorWriteReportsPresentRewrittenAndPreserved(t *testing.T) {
	root := t.TempDir()
	set := mustSynthesize(t, Options{Editors: []string{EditorUniversal, EditorEmacs}})
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	present, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, present, map[string]WriteOutcome{".editorconfig": WritePresent, ".dir-locals.el": WritePresent})

	writeTestFile(t, root, ".editorconfig", "root = false\n")
	writeTestFile(t, root, ".dir-locals.el", "; human managed\n")
	changed, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, changed, map[string]WriteOutcome{".editorconfig": WritePreserved, ".dir-locals.el": WriteRewritten})
	if got := mustRead(t, filepath.Join(root, ".editorconfig")); got != "root = false\n" {
		t.Fatalf("preserved file changed: %q", got)
	}
	if got := mustRead(t, filepath.Join(root, ".dir-locals.el")); got != fileContent(t, set, ".dir-locals.el") {
		t.Fatalf("rewritable template was not rewritten: %q", got)
	}
	// Boundary: a preserved file that cannot be read as text is still never replaced.
	unreadable := []byte("root = true\x00\n")
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), unreadable, 0o644); err != nil {
		t.Fatal(err)
	}
	binary, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, binary, map[string]WriteOutcome{".editorconfig": WritePreserved, ".dir-locals.el": WritePresent})
	if got := mustRead(t, filepath.Join(root, ".editorconfig")); got != string(unreadable) {
		t.Fatalf("unreadable preserved file changed: %q", got)
	}
	if _, err := WriteWithReport(nil, root); err == nil {
		t.Fatal("nil config set accepted")
	}
	if _, err := VerifyWithReport(&EditorConfigSet{Files: []GeneratedFile{{Path: "../escape.json", Content: "{}"}}}, root); err == nil {
		t.Fatal("verification accepted a path outside the workspace")
	}
}

// =========================================================================
// Strict decoding and merge boundaries
// =========================================================================

func TestEditorJSONDecodeBoundaries(t *testing.T) {
	accepted := map[string]string{
		"depth at bound":            strings.Repeat("[", maxEditorJSONDepth) + strings.Repeat("]", maxEditorJSONDepth),
		"array nodes at bound":      jsonArrayOf(maxJSONNodes - 1),
		"object nodes at bound":     jsonObjectOf(maxJSONNodes - 1),
		"same key in sibling scope": `{"a":{"k":1},"b":{"k":1},"k":[{"k":1},{"k":2}]}`,
		"scalar root":               "9007199254740993",
	}
	for name, raw := range accepted {
		if _, err := decodeEditorJSON([]byte(raw)); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	rejected := map[string][]byte{
		"empty":                []byte(""),
		"whitespace":           []byte(" \n"),
		"invalid UTF-8":        {'"', 0xff, '"'},
		"oversize":             bytes.Repeat([]byte(" "), contextopt.MaxSourceBytes+1),
		"two documents":        []byte("{} {}"),
		"trailing garbage":     []byte(`{"a":1}x`),
		"depth over bound":     []byte(strings.Repeat("[", maxEditorJSONDepth+1) + strings.Repeat("]", maxEditorJSONDepth+1)),
		"array nodes over":     []byte(jsonArrayOf(maxJSONNodes)),
		"object nodes over":    []byte(jsonObjectOf(maxJSONNodes)),
		"nested duplicate key": []byte(`{"a":{"k":1,"k":2}}`),
		"escaped duplicate":    []byte(`{"a":1,"a":2}`),
		"trailing comma":       []byte(`{"a":1,}`),
	}
	for name, raw := range rejected {
		if _, err := decodeEditorJSON(raw); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestEditorJSONMergeKeepsLiteralsAndUnchangedBytes(t *testing.T) {
	existing := []byte(`{"numbers":[9007199254740993,1e400,-0.000000000000000000001,1.50],"text":"<a&b>"}`)
	merged, changed, err := mergeJSONDocument(existing, []byte(`{"managed":true}`))
	if err != nil || !changed {
		t.Fatalf("merge failed: %v %v", changed, err)
	}
	for _, literal := range []string{"9007199254740993", "1e400", "-0.000000000000000000001", "1.50", `"<a&b>"`, `"managed": true`} {
		if !strings.Contains(string(merged), literal) {
			t.Errorf("merged JSON lost literal %s: %s", literal, merged)
		}
	}
	if !bytes.HasSuffix(merged, []byte("}\n")) {
		t.Errorf("merged JSON lacks a final newline: %q", merged)
	}

	compact := []byte(`{"b":2,"managed":true,"a":1}`)
	unchanged, changed, err := mergeJSONDocument(compact, []byte(`{"managed":true}`))
	if err != nil || changed || !bytes.Equal(unchanged, compact) {
		t.Fatalf("satisfied document was reformatted: %s %v %v", unchanged, changed, err)
	}
	if _, _, err := mergeJSONDocument([]byte(`{"n":1.0}`), []byte(`{"n":1}`)); err == nil || !strings.Contains(err.Error(), `"n"`) {
		t.Fatalf("differing number literal for a managed key did not conflict: %v", err)
	}
	if _, _, err := mergeJSONDocument([]byte(`{}`), []byte(`{"a":1,"a":2}`)); err == nil || !strings.Contains(err.Error(), "generated JSON is invalid") {
		t.Fatalf("ambiguous generated JSON was merged: %v", err)
	}
}

func TestEditorJSONMergeArraysAndTypeConflicts(t *testing.T) {
	existing := []byte(`[{"label":"Human verify","command":"make","args":["verify-all"]}]`)
	desired := []byte(`[{"label":"Standards: Verify All","command":"make","args":["verify-all"]},{"label":"Standards: Audit","command":"standardsctl","args":["audit"]}]`)
	merged, changed, err := mergeJSONDocument(existing, desired)
	if err != nil || !changed {
		t.Fatalf("root array merge failed: %v %v", changed, err)
	}
	if !strings.Contains(string(merged), "Human verify") || !strings.Contains(string(merged), "Standards: Audit") || strings.Contains(string(merged), "Standards: Verify All") {
		t.Fatalf("root array merge duplicated or dropped a task: %s", merged)
	}
	if contains, err := jsonDocumentContains(merged, desired); err != nil || !contains {
		t.Fatalf("merged root array does not contain managed tasks: %v %v", contains, err)
	}
	shellTask := []byte(`{"tasks":[{"label":"Human","type":"shell","command":"make verify-all"}]}`)
	processTask := []byte(`{"tasks":[{"label":"Standards: Verify All","type":"process","command":"make","args":["verify-all"]}]}`)
	if _, changed, err := mergeJSONDocument(shellTask, processTask); err != nil || changed {
		t.Fatalf("shell command string and argument vector for the same command were not matched: %v %v", changed, err)
	}
	for _, conflict := range []struct{ have, want, message string }{
		{have: `{"a":[1]}`, want: `{"a":{"b":1}}`, message: "managed object"},
		{have: `{"a":{"b":1}}`, want: `{"a":[1]}`, message: "managed array"},
		{have: `{"a":{"b":1}}`, want: `{"a":{"b":2}}`, message: `"b"`},
	} {
		if _, _, err := mergeJSONDocument([]byte(conflict.have), []byte(conflict.want)); err == nil || !strings.Contains(err.Error(), conflict.message) {
			t.Errorf("merge of %s into %s did not report %s: %v", conflict.want, conflict.have, conflict.message, err)
		}
		if contains, err := jsonDocumentContains([]byte(conflict.have), []byte(conflict.want)); err != nil || contains {
			t.Errorf("containment accepted conflicting %s in %s: %v %v", conflict.want, conflict.have, contains, err)
		}
	}
}

func TestEditorJSONTraversalRejectsFramesOverBound(t *testing.T) {
	have := make(map[string]any, maxJSONNodes)
	want := make(map[string]any, maxJSONNodes)
	for i := 0; i < maxJSONNodes; i++ {
		key := fmt.Sprintf("k%d", i)
		have[key] = map[string]any{}
		want[key] = map[string]any{}
	}
	if _, _, err := mergeJSONValue(have, want); !errors.Is(err, errEditorJSONNodeBound) {
		t.Fatalf("merge silently stopped at the frame bound: %v", err)
	}
	if jsonContains(have, want) {
		t.Fatal("containment passed without visiting every frame")
	}
	delete(have, "k0")
	delete(want, "k0")
	if _, _, err := mergeJSONValue(have, want); err != nil {
		t.Fatalf("frames at the bound were rejected: %v", err)
	}
	if !jsonContains(have, want) {
		t.Fatal("containment rejected frames at the bound")
	}
}

func TestEditorGeneratedJSONIsStrictlyValid(t *testing.T) {
	for _, archetype := range []string{"framework", "native-gpu-systems", "app-service"} {
		opts := DefaultOptions()
		opts.Archetype = archetype
		set := mustSynthesize(t, opts)
		jsonFiles := 0
		for _, file := range set.Files {
			if !isJSONEditorFile(file.Path) {
				continue
			}
			jsonFiles++
			if _, err := decodeEditorJSON([]byte(file.Content)); err != nil {
				t.Errorf("%s/%s is not strict editor JSON: %v", archetype, file.Path, err)
			}
		}
		if jsonFiles != 8 {
			t.Errorf("%s generated %d JSON editor files, want 8", archetype, jsonFiles)
		}
	}
}

// =========================================================================
// Helpers
// =========================================================================

func jsonArrayOf(items int) string {
	return "[" + strings.TrimSuffix(strings.Repeat("0,", items), ",") + "]"
}

func jsonObjectOf(members int) string {
	var builder strings.Builder
	builder.WriteString("{")
	for i := 0; i < members; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `"k%d":0`, i)
	}
	builder.WriteString("}")
	return builder.String()
}

func assertOutcomes(t *testing.T, report WriteReport, want map[string]WriteOutcome) {
	t.Helper()
	got := make(map[string]WriteOutcome, len(report.Files))
	for _, file := range report.Files {
		got[filepath.ToSlash(file.Path)] = file.Outcome
	}
	if len(got) != len(want) {
		t.Fatalf("write report %v, want %v", got, want)
	}
	for path, outcome := range want {
		if got[path] != outcome {
			t.Errorf("%s outcome %q, want %q (report %v)", path, got[path], outcome, got)
		}
	}
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeTestFile(t, root, path, content)
	}
	return root
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSynthesize(t *testing.T, opts Options) *EditorConfigSet {
	t.Helper()
	set, err := Synthesize(opts)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func fileContent(t *testing.T, set *EditorConfigSet, path string) string {
	t.Helper()
	for _, file := range set.Files {
		if file.Path == path {
			return file.Content
		}
	}
	t.Fatalf("missing generated file %s", path)
	return ""
}

func assertContainsFile(t *testing.T, root, path string, values ...string) {
	t.Helper()
	content := mustRead(t, filepath.Join(root, path))
	for _, value := range values {
		if !strings.Contains(content, value) {
			t.Errorf("%s missing %q: %s", path, value, content)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
