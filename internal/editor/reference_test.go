package editor

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// engineRoot is the checkout root, where the tracked editors/ tree lives.
var engineRoot = filepath.Join("..", "..")

// ideProvidedInspections are the inspection classes the generated JetBrains profile may name for
// a Go workspace. GoUnhandledErrorResult and GoInfiniteFor are documented under their ids in
// JetBrains Inspectopedia; GoCyclomaticComplexity carries the resolved cyclomatic ceiling.
var ideProvidedInspections = []string{"GoCyclomaticComplexity", "GoInfiniteFor", "GoUnhandledErrorResult"}

var inspectionClassPattern = regexp.MustCompile(`<inspection_tool class="([^"]+)"`)

// Positive: the tracked editors/ reference tree is exactly what the generator renders, so it
// cannot drift from `praetorctl editors generate` again (BUG-600, BUG-625).
func TestReferenceSetMatchesTrackedTree(t *testing.T) {
	set := ReferenceSet()
	if len(set.Files) != len(referencePaths) {
		t.Fatalf("reference set renders %d files, want one per mapped path (%d)", len(set.Files), len(referencePaths))
	}
	report, err := VerifyWithReport(set, engineRoot)
	if err != nil {
		t.Fatalf("tracked editors/ tree drifted from the generator; run `make editors-reference`: %v", err)
	}
	if len(report.Verified) != len(set.Files) {
		t.Errorf("verified %v, want every reference file", report.Verified)
	}
}

// Negative: a hand edit to a reference file is reported as drift, and Write restores the
// generator's text.
func TestReferenceSetReportsHandEditAsDrift(t *testing.T) {
	root := t.TempDir()
	set := ReferenceSet()
	if err := Write(set, root); err != nil {
		t.Fatalf("write reference set: %v", err)
	}
	lua := filepath.Join(root, "editors", "neovim", "lua", "standards.lua")
	if err := os.WriteFile(lua, []byte("-- hand edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(set, root); err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("hand-edited reference file verified: %v", err)
	}
	if err := Write(set, root); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if err := Verify(set, root); err != nil {
		t.Errorf("regenerated reference tree does not verify: %v", err)
	}
}

// Boundary: the JetBrains profile names only IDE-provided inspections -- none of the HISS
// classes no plugin implements (BUG-656) -- and every Neovim command runs a command that exists,
// the ratchet sweep included (BUG-588).
func TestReferenceSetNamesOnlyRealInspectionsAndCommands(t *testing.T) {
	files := make(map[string]string, len(referencePaths))
	for _, file := range ReferenceSet().Files {
		files[file.Path] = file.Content
	}
	profile := files["editors/jetbrains/inspectionProfiles/standards.xml"]
	matches := inspectionClassPattern.FindAllStringSubmatch(profile, 64)
	if len(matches) == 0 {
		t.Fatalf("reference profile names no inspection:\n%s", profile)
	}
	for _, m := range matches {
		if !slices.Contains(ideProvidedInspections, m[1]) {
			t.Errorf("reference profile names %s, which no IDE provides", m[1])
		}
	}
	lua := files["editors/neovim/lua/standards.lua"]
	sweep := `vim.api.nvim_create_user_command("StandardsRatchetSweep", function()` + "\n" + `  vim.cmd("!praetorctl audit")`
	if !strings.Contains(lua, sweep) {
		t.Errorf("ratchet sweep must run praetorctl audit:\n%s", lua)
	}
	if strings.Contains(lua, "baseline --check") || strings.Contains(lua, "go run ") {
		t.Errorf("reference module runs a missing flag or a source-checkout invocation:\n%s", lua)
	}
}
