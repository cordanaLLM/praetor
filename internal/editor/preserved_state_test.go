package editor

import (
	"path/filepath"
	"slices"
	"testing"
)

// developerStateFiles are the generated files that hold IDE session state or a developer's own
// editor setup (BUG-024, BUG-171).
var developerStateFiles = []string{".idea/workspace.xml", ".nvim.lua", ".dir-locals.el"}

func developerStateSet(t *testing.T) *EditorConfigSet {
	t.Helper()
	return mustSynthesize(t, Options{Editors: []string{EditorJetBrains, EditorNeovim, EditorEmacs}})
}

// Positive: absent session files are still generated from the plan and verify clean.
func TestEditorWriteCreatesAbsentDeveloperStateFiles(t *testing.T) {
	root := t.TempDir()
	set := developerStateSet(t)
	report, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, report, map[string]WriteOutcome{
		".idea/inspectionProfiles/standards.xml": WriteCreated, ".idea/workspace.xml": WriteCreated,
		"lua/standards.lua": WriteCreated, ".nvim.lua": WriteCreated, ".dir-locals.el": WriteCreated,
	})
	for _, path := range developerStateFiles {
		if got := mustRead(t, filepath.Join(root, path)); got != fileContent(t, set, path) {
			t.Errorf("%s was not generated from its template: %q", path, got)
		}
	}
	verification, err := VerifyWithReport(set, root)
	if err != nil || len(verification.Verified) != len(set.Files) || len(verification.PreservedUnverified) != 0 {
		t.Fatalf("freshly generated files did not all verify: %+v %v", verification, err)
	}
}

// Negative: an existing, differing session file is left byte-for-byte as the developer had it,
// while the Praetor-owned files next to it are still rewritten.
func TestEditorWritePreservesExistingDeveloperStateFiles(t *testing.T) {
	root := t.TempDir()
	set := developerStateSet(t)
	custom := map[string]string{
		".idea/workspace.xml": "<project version=\"4\"><component name=\"RunManager\"/></project>\n",
		".nvim.lua":           "vim.opt.number = true\n",
		".dir-locals.el":      "((nil . ((fill-column . 80))))\n",
	}
	for path, content := range custom {
		writeTestFile(t, root, path, content)
	}
	writeTestFile(t, root, "lua/standards.lua", "-- hand edited\n")
	report, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, report, map[string]WriteOutcome{
		".idea/inspectionProfiles/standards.xml": WriteCreated, ".idea/workspace.xml": WritePreserved,
		"lua/standards.lua": WriteRewritten, ".nvim.lua": WritePreserved, ".dir-locals.el": WritePreserved,
	})
	for path, content := range custom {
		if got := mustRead(t, filepath.Join(root, path)); got != content {
			t.Errorf("%s was overwritten: %q", path, got)
		}
	}
	verification, err := VerifyWithReport(set, root)
	if err != nil {
		t.Fatalf("preserved developer state failed verification: %v", err)
	}
	if !slices.Equal(verification.PreservedUnverified, []string{".idea/workspace.xml", ".nvim.lua", ".dir-locals.el"}) {
		t.Fatalf("preserved files were not reported as unverified: %+v", verification)
	}
}

// Boundary: the rule matches the exact slash-separated workspace path and nothing near it.
func TestIsPreservedEditorFileMatchesExactPaths(t *testing.T) {
	for _, path := range append([]string{".editorconfig", ".clang-tidy"}, developerStateFiles...) {
		if !IsPreservedEditorFile(path) {
			t.Errorf("%s must be preserved", path)
		}
	}
	for _, path := range []string{
		"", ".idea/workspace.xml.bak", "idea/workspace.xml", ".IDEA/workspace.xml", `.idea\workspace.xml`,
		"sub/.nvim.lua", ".idea/inspectionProfiles/standards.xml", "lua/standards.lua", ".vscode/settings.json",
	} {
		if IsPreservedEditorFile(path) {
			t.Errorf("%q must not be preserved", path)
		}
	}
}
