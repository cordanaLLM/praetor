package editor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Positive: an existing JSON file gains the missing managed members, keeps every other key, and
// the resolution names each added member as an RFC 6901 JSON Pointer (a key holding "/" or "~"
// escaped, a list that gains entries once, as its pointer plus "/-"), whether or not drift is
// kept: a merge is never drift.
func TestResolveExisting_Positive_JSONMergeNamesAddedMembers(t *testing.T) {
	file := GeneratedFile{Path: ".vscode/settings.json", Editor: EditorVSCode,
		Content: `{"a/b":1,"n":{"t~x":true,"keep":2},"list":["x","y"]}`}
	existing := []byte(`{"custom":"kept","n":{"keep":2},"list":["x"]}`)
	for _, keepDrift := range []bool{false, true} {
		got, err := ResolveExisting(file, existing, keepDrift)
		if err != nil {
			t.Fatalf("keepDrift=%v: %v", keepDrift, err)
		}
		want := []string{"/a~1b", "/list/-", "/n/t~0x"}
		if got.Outcome != WriteMerged || !slices.Equal(sortedCopy(got.Added), want) {
			t.Fatalf("keepDrift=%v: outcome %s added %v, want MERGED %v", keepDrift, got.Outcome, got.Added, want)
		}
		if !strings.Contains(got.Content, `"custom": "kept"`) || !strings.Contains(got.Content, `"y"`) {
			t.Errorf("keepDrift=%v: merged document lost an adopter key or a managed entry:\n%s", keepDrift, got.Content)
		}
	}
}

// Negative: a JSONC file, and any other file that is not strict JSON, is refused with
// ErrExistingJSONInvalid; a conflicting managed value is refused without it, so a caller can
// tell a file it may never re-encode from one whose value the operator must reconcile.
func TestResolveExisting_Negative_UnmergeableJSONIsRefused(t *testing.T) {
	file := GeneratedFile{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}` + "\n"}
	for name, existing := range map[string]string{
		"jsonc comment":  "// adopter note\n{\"managed\":1}\n",
		"trailing comma": `{"managed":1,}`,
		"duplicate key":  `{"x":1,"x":2}`,
	} {
		_, err := ResolveExisting(file, []byte(existing), true)
		if !errors.Is(err, ErrExistingJSONInvalid) || !strings.Contains(err.Error(), file.Path) {
			t.Errorf("%s: err = %v, want ErrExistingJSONInvalid naming %s", name, err, file.Path)
		}
	}
	_, err := ResolveExisting(file, []byte(`{"managed":2}`), true)
	if err == nil || errors.Is(err, ErrExistingJSONInvalid) || !strings.Contains(err.Error(), `"managed"`) {
		t.Fatalf("conflict err = %v, want a conflict naming the key, not ErrExistingJSONInvalid", err)
	}
}

// Boundary: a differing non-JSON template file is rewritten for `editors generate` and kept
// when the caller keeps drift, with nothing to write; a developer-owned file stays preserved
// and an identical file present under either rule. A CRLF checkout of the template
// (core.autocrlf on Windows) is the template, present under either rule (HISS-21); mixed line
// endings are no checkout of it and stay drift.
func TestResolveExisting_Boundary_KeepDriftDecidesNonJSONDrift(t *testing.T) {
	module := GeneratedFile{Path: "lua/standards.lua", Editor: EditorNeovim, Content: "-- template\n-- end\n"}
	crlf, mixed := "-- template\r\n-- end\r\n", "-- template\r\n-- end\n"
	cases := []struct {
		file      GeneratedFile
		existing  string
		keepDrift bool
		outcome   WriteOutcome
		content   string
	}{
		{module, "-- edited\n", false, WriteRewritten, module.Content},
		{module, "-- edited\n", true, WriteKept, ""},
		{module, module.Content, true, WritePresent, ""},
		{module, crlf, true, WritePresent, ""},
		{module, crlf, false, WritePresent, ""},
		{module, mixed, true, WriteKept, ""},
		{module, mixed, false, WriteRewritten, module.Content},
		{GeneratedFile{Path: ".nvim.lua", Editor: EditorNeovim, Content: "-- template\n"}, "vim.opt.number = true\n", false, WritePreserved, ""},
	}
	for _, tc := range cases {
		got, err := ResolveExisting(tc.file, []byte(tc.existing), tc.keepDrift)
		if err != nil {
			t.Fatalf("%s keepDrift=%v: %v", tc.file.Path, tc.keepDrift, err)
		}
		if got.Outcome != tc.outcome || got.Content != tc.content || len(got.Added) != 0 {
			t.Errorf("%s %q keepDrift=%v: got %+v, want %s with content %q", tc.file.Path, tc.existing, tc.keepDrift, got, tc.outcome, tc.content)
		}
	}
}

// Boundary (HISS-21): verification accepts the CRLF checkout of a non-JSON template that
// ResolveExisting counts as present, so a file adoption reports as holding every managed value
// is one `editors verify` passes; mixed line endings are out of sync.
func TestVerifyWithReport_Boundary_CRLFCheckoutOfTemplateVerifies(t *testing.T) {
	root := t.TempDir()
	set := mustSynthesize(t, Options{Editors: []string{EditorNeovim}})
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "lua", "standards.lua")
	generated, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(generated), "\n", "\r\n")
	writeTestFile(t, root, "lua/standards.lua", crlf)
	report, err := VerifyWithReport(set, root)
	if err != nil || !slices.Contains(report.Verified, "lua/standards.lua") {
		t.Fatalf("CRLF checkout of the template not verified: %+v %v", report, err)
	}
	writeTestFile(t, root, "lua/standards.lua", strings.Replace(crlf, "\r\n", "\n", 1))
	if err := Verify(set, root); err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("mixed line endings verified: %v", err)
	}
}

func sortedCopy(values []string) []string {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted
}
