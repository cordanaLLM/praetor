package paperclip

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// operatorHarness is an operator-owned harness naming acme/old, with a member this release does
// not know and a value in the escaped form json.MarshalIndent writes.
const operatorHarness = `{
  "version": 1,
  "platform": "acme/old",
  "operating_contract": ["result: edited contract."],
  "agit_push_format": "git push custom",
  "invariants": ["result: func LOC <= 75."],
  "notes": "operator: keep this member"
}
`

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
	if strings.Index(string(patched), `"platform"`) > strings.Index(string(patched), `"operating_contract"`) ||
		!strings.HasSuffix(string(patched), "member\"\n}\n") {
		t.Fatalf("member order or layout not kept:\n%s", patched)
	}
	dir := t.TempDir()
	writeRepoFile(t, dir, "harness.json", string(patched))
	if h, err := LoadHarness(filepath.Join(dir, "harness.json")); err != nil || h.Platform != "acme/new" || h.OperatingContract[0] != "result: edited contract." {
		t.Fatalf("patched harness does not load: %+v %v", h, err)
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
// returned as it is, byte for byte and escapes included; a consistent CRLF text stays CRLF; a
// harness without a platform member gains one after the last member.
func TestPatchPlatform_Boundary_UnchangedAndLineEndings(t *testing.T) {
	same, changed, err := PatchPlatform([]byte(operatorHarness), "acme/old")
	if err != nil || changed || string(same) != operatorHarness {
		t.Fatalf("unchanged platform rewritten: changed=%v err=%v\n%s", changed, err, same)
	}
	crlf := strings.ReplaceAll(operatorHarness, "\n", "\r\n")
	patched, changed, err := PatchPlatform([]byte(crlf), "acme/new")
	if err != nil || !changed || strings.Count(string(patched), "\r\n") != strings.Count(string(patched), "\n") {
		t.Fatalf("CRLF style not kept: changed=%v err=%v\n%q", changed, err, patched)
	}
	missing, changed, err := PatchPlatform([]byte(`{"version": 1}`), "acme/new")
	if err != nil || !changed || !strings.HasSuffix(string(missing), "\"platform\": \"acme/new\"\n}\n") {
		t.Fatalf("absent platform not appended: changed=%v err=%v\n%s", changed, err, missing)
	}
}
