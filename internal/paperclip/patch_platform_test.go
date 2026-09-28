package paperclip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// operatorHarness is an operator-owned harness naming acme/old, with a member this release does
// not know and values in the \u003c / \u0026 escaped form json.MarshalIndent writes.
const operatorHarness = `{
  "version": 1,
  "platform": "acme/old",
  "operating_contract": ["result: edited contract, fetch \u0026\u0026 rebase."],
  "agit_push_format": "git push custom",
  "invariants": ["result: func LOC \u003c= 75."],
  "notes": "operator: keep this member"
}
`

// withPlatform returns text with its "platform": "acme/old" member naming platform instead, the
// one change PatchPlatform may make to a harness that has the member.
func withPlatform(text, platform string) string {
	return strings.Replace(text, `"platform": "acme/old"`, `"platform": "`+platform+`"`, 1)
}

// decodeMembers decodes a harness text into its member values, unknown members included.
func decodeMembers(t *testing.T, data []byte) map[string]any {
	t.Helper()
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return values
}

// TestPatchPlatform_Positive_SetsOnlyPlatform: the patched harness names the new platform,
// still loads, and keeps every other member, the unknown one included, in its place.
func TestPatchPlatform_Positive_SetsOnlyPlatform(t *testing.T) {
	patched, changed, err := PatchPlatform([]byte(operatorHarness), "acme/new")
	if err != nil || !changed {
		t.Fatalf("PatchPlatform: changed=%v err=%v", changed, err)
	}
	before, after := decodeMembers(t, []byte(operatorHarness)), decodeMembers(t, patched)
	if after["platform"] != "acme/new" {
		t.Fatalf("platform not set:\n%s", patched)
	}
	delete(before, "platform")
	delete(after, "platform")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("members other than platform changed:\n%s", patched)
	}
	if want := withPlatform(operatorHarness, "acme/new"); string(patched) != want {
		t.Fatalf("patch changed more bytes than the platform value:\n%s\nwant:\n%s", patched, want)
	}
	dir := t.TempDir()
	writeRepoFile(t, dir, "harness.json", string(patched))
	if h, err := LoadHarness(filepath.Join(dir, "harness.json")); err != nil || h.Platform != "acme/new" ||
		h.OperatingContract[0] != "result: edited contract, fetch && rebase." {
		t.Fatalf("patched harness does not load: %+v %v", h, err)
	}
}

// TestPatchPlatform_Positive_ReleasedHarnessChangesOneLine: on a harness a release wrote
// (json.MarshalIndent, so &&, < and > stand escaped as \u0026, \u003c and \u003e), the patch
// differs from the input in the platform line alone; every escaped contract row stays byte for
// byte.
func TestPatchPlatform_Positive_ReleasedHarnessChangesOneLine(t *testing.T) {
	released, err := os.ReadFile(filepath.Join("testdata", "harness-462e3f3a", "harness.json.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(released), `\u003c`) || !strings.Contains(string(released), `"platform": "acme/legacy"`) {
		t.Fatal("golden harness no longer carries an escaped value and platform acme/legacy")
	}
	patched, changed, err := PatchPlatform(released, "acme/other")
	if err != nil || !changed {
		t.Fatalf("PatchPlatform: changed=%v err=%v", changed, err)
	}
	before, after := strings.Split(string(released), "\n"), strings.Split(string(patched), "\n")
	if len(before) != len(after) {
		t.Fatalf("line count changed: %d -> %d", len(before), len(after))
	}
	var diff []string
	for i := range before {
		if before[i] != after[i] {
			diff = append(diff, before[i]+" -> "+after[i])
		}
	}
	if len(diff) != 1 || diff[0] != `  "platform": "acme/legacy", ->   "platform": "acme/other",` {
		t.Fatalf("patch changed %d lines, want the platform line alone:\n%s", len(diff), strings.Join(diff, "\n"))
	}
}

// TestPatchPlatform_Negative_RefusesWhatItCannotPatch: text that is no JSON object, an object
// with a duplicate member, and a platform LoadHarnessContext would refuse all fail, with nothing
// returned to write.
func TestPatchPlatform_Negative_RefusesWhatItCannotPatch(t *testing.T) {
	for name, tc := range map[string]struct{ data, platform string }{
		"array":            {`["acme/old"]`, "acme/new"},
		"truncated":        {`{"version": 1, "platform": "acme/old",`, "acme/new"},
		"duplicate member": {`{"platform": "acme/old", "platform": "acme/older"}`, "acme/new"},
		"empty platform":   {operatorHarness, ""},
		"blank platform":   {operatorHarness, "  "},
		"oversized":        {operatorHarness, strings.Repeat("a", maxHarnessValueBytes+1)},
	} {
		patched, changed, err := PatchPlatform([]byte(tc.data), tc.platform)
		if err == nil || changed || patched != nil {
			t.Fatalf("%s: accepted: changed=%v patched=%q err=%v", name, changed, patched, err)
		}
	}
}

// TestPatchPlatform_Boundary_UnchangedAndLineEndings: a harness already naming the platform is
// returned as it is, byte for byte and escapes included; a CRLF text and a compact one differ
// from the input in the platform value alone; a harness without a platform member gains one
// after the last member.
func TestPatchPlatform_Boundary_UnchangedAndLineEndings(t *testing.T) {
	same, changed, err := PatchPlatform([]byte(operatorHarness), "acme/old")
	if err != nil || changed || string(same) != operatorHarness {
		t.Fatalf("unchanged platform rewritten: changed=%v err=%v\n%s", changed, err, same)
	}
	crlf := strings.ReplaceAll(operatorHarness, "\n", "\r\n")
	patched, changed, err := PatchPlatform([]byte(crlf), "acme/new")
	if err != nil || !changed || string(patched) != withPlatform(crlf, "acme/new") {
		t.Fatalf("CRLF text not kept byte for byte: changed=%v err=%v\n%q", changed, err, patched)
	}
	compact := `{"version":1,"notes":"a \u003c b","platform":"acme/old"}`
	patched, changed, err = PatchPlatform([]byte(compact), "acme/new")
	if err != nil || !changed || string(patched) != `{"version":1,"notes":"a \u003c b","platform":"acme/new"}` {
		t.Fatalf("compact layout re-rendered: changed=%v err=%v\n%s", changed, err, patched)
	}
	missing, changed, err := PatchPlatform([]byte(`{"version": 1}`), "acme/new")
	if err != nil || !changed || !strings.HasSuffix(string(missing), "\"platform\": \"acme/new\"\n}\n") {
		t.Fatalf("absent platform not appended: changed=%v err=%v\n%s", changed, err, missing)
	}
}
