package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestEditor_Positive_SynthesizeAllEditors(t *testing.T) {
	opts := DefaultOptions()
	// The workspace proves every capability, so each renderer has something to assert.
	opts.WorkspaceRoot = evidenceWorkspace(t)
	set, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if len(set.Files) == 0 {
		t.Fatalf("expected generated files, got 0")
	}

	fileMap := make(map[string]string)
	for _, f := range set.Files {
		fileMap[f.Path] = f.Content
	}

	verifyVSCodeAndJetBrains(t, fileMap)
	verifyNeovimAndZed(t, fileMap)
	verifyRemainingEditors(t, fileMap)
}

func verifyVSCodeAndJetBrains(t *testing.T, fileMap map[string]string) {
	vscodeSettings, ok := fileMap[".vscode/settings.json"]
	if !ok {
		t.Errorf("missing .vscode/settings.json")
	}
	var settingsJSON map[string]any
	if err := json.Unmarshal([]byte(vscodeSettings), &settingsJSON); err != nil {
		t.Errorf(".vscode/settings.json is not valid JSON: %v", err)
	}
	if !strings.Contains(vscodeSettings, "standards-lsp") {
		t.Errorf(".vscode/settings.json missing standards-lsp reference")
	}
	for _, forbidden := range []string{"standards.mcp.", "standards.modelTier", "gemini-2.5-pro"} {
		if strings.Contains(vscodeSettings, forbidden) {
			t.Errorf("workspace settings must not assert %s: %s", forbidden, vscodeSettings)
		}
	}

	vscodeTasks, ok := fileMap[".vscode/tasks.json"]
	if !ok {
		t.Errorf("missing .vscode/tasks.json")
	}
	var tasksJSON map[string]any
	if err := json.Unmarshal([]byte(vscodeTasks), &tasksJSON); err != nil {
		t.Errorf(".vscode/tasks.json is not valid JSON: %v", err)
	}

	ideaInspection, ok := fileMap[".idea/inspectionProfiles/standards.xml"]
	if !ok {
		t.Errorf("missing .idea/inspectionProfiles/standards.xml")
	}
	if !strings.Contains(ideaInspection, "HISS04ComplexityLOC") {
		t.Errorf("idea inspection profile missing HISS04ComplexityLOC")
	}
}

func verifyNeovimAndZed(t *testing.T, fileMap map[string]string) {
	nvimLua, ok := fileMap["lua/standards.lua"]
	if !ok {
		t.Errorf("missing lua/standards.lua")
	}
	if !strings.Contains(nvimLua, "standards_lsp") {
		t.Errorf("lua/standards.lua missing standards_lsp registration")
	}
	if !strings.Contains(nvimLua, "StandardsVerifyAll") {
		t.Errorf("lua/standards.lua missing the resolved verify-all user command")
	}

	if _, ok := fileMap[".zed/settings.json"]; !ok {
		t.Errorf("missing .zed/settings.json")
	}
	if _, ok := fileMap[".zed/tasks.json"]; !ok {
		t.Errorf("missing .zed/tasks.json")
	}
}

func verifyRemainingEditors(t *testing.T, fileMap map[string]string) {
	if _, ok := fileMap[".editorconfig"]; !ok {
		t.Errorf("missing .editorconfig")
	}
	if _, ok := fileMap[".helix/config.toml"]; !ok {
		t.Errorf("missing .helix/config.toml")
	}
	if _, ok := fileMap[".helix/languages.toml"]; !ok {
		t.Errorf("missing .helix/languages.toml")
	}
	if _, ok := fileMap[".dir-locals.el"]; !ok {
		t.Errorf("missing .dir-locals.el")
	}
	if _, ok := fileMap[".fleet/settings.json"]; !ok {
		t.Errorf("missing .fleet/settings.json")
	}
	if _, ok := fileMap["standards.sublime-project"]; !ok {
		t.Errorf("missing standards.sublime-project")
	}
	if _, ok := fileMap[".clang-tidy"]; !ok {
		t.Errorf("missing .clang-tidy")
	}
}

func TestEditor_Positive_WriteAndVerify(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions()
	set, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	// 1. Write configuration files
	if err := Write(set, tmpDir); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Check files actually created on disk
	for _, f := range set.Files {
		p := filepath.Join(tmpDir, f.Path)
		if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
			t.Errorf("expected file %s to exist on disk", p)
		}
	}

	// 2. Verify clean match
	if err := Verify(set, tmpDir); err != nil {
		t.Errorf("Verify failed for cleanly written files: %v", err)
	}

	// 3. Detect tampering / out-of-sync configuration
	tamperPath := filepath.Join(tmpDir, ".vscode", "settings.json")
	if err := os.WriteFile(tamperPath, []byte(`{"tampered": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Verify(set, tmpDir); err == nil {
		t.Errorf("expected Verify to detect modified out-of-sync configuration file")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestEditor_Negative_EmptyOrInvalidEditors(t *testing.T) {
	// 1. Empty editors list
	_, err := Synthesize(Options{
		Editors: []string{},
	})
	if err == nil {
		t.Errorf("expected error for empty editors list")
	}
	if !strings.Contains(err.Error(), "no valid editors") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 2. Only unsupported editor names
	_, err = Synthesize(Options{
		Editors: []string{"notepad", "nano", "gedit"},
	})
	if err == nil {
		t.Errorf("expected error when no supported editors matched")
	}
}

func TestEditor_Negative_NilSetWriteAndVerify(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Write nil set
	if err := Write(nil, tmpDir); err == nil {
		t.Errorf("expected error writing nil config set")
	}

	// 2. Verify nil set
	if err := Verify(nil, tmpDir); err == nil {
		t.Errorf("expected error verifying nil config set")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestEditor_Boundary_SingleEditorSelection(t *testing.T) {
	// 1. Neovim only
	nvimSet, err := Synthesize(Options{
		Editors: []string{"neovim"},
	})
	if err != nil {
		t.Fatalf("Synthesize neovim failed: %v", err)
	}
	if len(nvimSet.Files) != 2 {
		t.Errorf("expected 2 files for neovim only, got %d", len(nvimSet.Files))
	}
	for _, f := range nvimSet.Files {
		if f.Editor != EditorNeovim {
			t.Errorf("unexpected editor %s for neovim-only set", f.Editor)
		}
	}

	// 2. JetBrains only
	ideaSet, err := Synthesize(Options{
		Editors: []string{"jetbrains"},
	})
	if err != nil {
		t.Fatalf("Synthesize jetbrains failed: %v", err)
	}
	if len(ideaSet.Files) != 2 {
		t.Errorf("expected 2 files for jetbrains only, got %d", len(ideaSet.Files))
	}
	for _, f := range ideaSet.Files {
		if f.Editor != EditorJetBrains {
			t.Errorf("unexpected editor %s for jetbrains-only set", f.Editor)
		}
	}
}

func TestEditor_Boundary_CustomBinaryDirAndFlags(t *testing.T) {
	root := evidenceWorkspace(t)
	customBin := "tools/custom-bin"
	writeExecutable(t, root, lspBinaryRel(customBin))
	settingsContent := fileContent(t, mustSynthesize(t, Options{
		WorkspaceRoot: root,
		Editors:       []string{"vscode"},
		BinaryDir:     customBin,
		IncludeMCP:    false,
		IncludeLSP:    true,
	}), ".vscode/settings.json")

	if !strings.Contains(settingsContent, "${workspaceFolder}/"+lspBinaryRel(customBin)) {
		t.Errorf("settings missing custom binary path: %s", settingsContent)
	}
	for _, forbidden := range []string{
		"standards.mcp.", "standards.modelTier", "gemini-2.5-pro",
		"antigravity.searchMaxWorkspaceFileCount", "files.watcherExclude",
	} {
		if strings.Contains(settingsContent, forbidden) {
			t.Errorf("workspace settings must not assert %s: %s", forbidden, settingsContent)
		}
	}

	// An absolute binary directory is outside the workspace, so the resolver cannot prove the
	// server is the repository's own; nothing about it is written.
	absolute := fileContent(t, mustSynthesize(t, Options{
		WorkspaceRoot: root,
		Editors:       []string{"vscode"},
		BinaryDir:     filepath.Join(root, customBin),
		IncludeLSP:    true,
	}), ".vscode/settings.json")
	if strings.Contains(absolute, "standards.lsp") {
		t.Errorf("an absolute binary directory must not produce LSP settings: %s", absolute)
	}
}

func TestEditor_Positive_ArchetypeNativeGPUSystems(t *testing.T) {
	opts := DefaultOptions()
	opts.Archetype = "native-gpu-systems"
	// The filetypes belong to the language server block, which needs a proven server.
	opts.WorkspaceRoot = evidenceWorkspace(t)
	set, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	fileMap := make(map[string]string)
	for _, f := range set.Files {
		fileMap[f.Path] = f.Content
	}

	// Verify clangd in VSCode settings
	vsSettings := fileMap[".vscode/settings.json"]
	if !strings.Contains(vsSettings, "clangd") {
		t.Errorf("expected clangd in VSCode settings for native-gpu-systems: %s", vsSettings)
	}
	// clangd finds compile_commands.json itself; a fixed directory pinned every adopter to
	// one repository's core/build layout (BUG-879).
	if strings.Contains(vsSettings, "--compile-commands-dir") || !strings.Contains(vsSettings, "--header-insertion=never") {
		t.Errorf("clangd arguments not the shared adopter-neutral set: %s", vsSettings)
	}

	// Verify ClangTidy in JetBrains
	ideaXML := fileMap[".idea/inspectionProfiles/standards.xml"]
	if !strings.Contains(ideaXML, "ClangTidyInspection") {
		t.Errorf("expected ClangTidyInspection in JetBrains XML for native-gpu-systems")
	}

	// Verify Neovim filetypes
	nvimLua := fileMap["lua/standards.lua"]
	if !strings.Contains(nvimLua, "cuda") || !strings.Contains(nvimLua, "cpp") {
		t.Errorf("expected cpp and cuda in Neovim filetypes for native-gpu-systems")
	}
}

// =========================================================================
// Antigravity editor target (#169)
// =========================================================================

func TestEditor_Positive_AntigravityAliasesAndKeys(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "antigravity", "settings.golden.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var wantSettings map[string]any
	if err := json.Unmarshal(golden, &wantSettings); err != nil {
		t.Fatalf("golden fixture is not valid JSON: %v", err)
	}

	for _, alias := range []string{"antigravity", "agy", "antigravity-ide"} {
		t.Run(alias, func(t *testing.T) {
			// WorkspaceRoot points at a temp directory holding only the language server, so
			// language detection observes no Go sources, matching the golden fixture; the real
			// repository (WorkspaceRoot defaulting to ".") would otherwise add unrelated
			// Go-derived keys.
			workspace := t.TempDir()
			writeExecutable(t, workspace, lspBinaryRel("bin"))
			wantSettings["standards.lsp.path"] = "${workspaceFolder}/" + lspBinaryRel("bin")
			opts := Options{
				Editors:       []string{alias},
				WorkspaceRoot: workspace,
				BinaryDir:     "bin",
				Archetype:     "framework",
				IncludeLSP:    true,
			}
			set, err := Synthesize(opts)
			if err != nil {
				t.Fatalf("Synthesize(%q) failed: %v", alias, err)
			}
			if len(set.Editors) != 1 || set.Editors[0] != EditorAntigravity {
				t.Fatalf("alias %q resolved to %v, want [%s]", alias, set.Editors, EditorAntigravity)
			}

			var settingsContent string
			var found bool
			for _, f := range set.Files {
				if f.Path == ".vscode/settings.json" {
					settingsContent, found = f.Content, true
					break
				}
			}
			if !found {
				t.Fatalf("alias %q produced no .vscode/settings.json", alias)
			}
			var gotSettings map[string]any
			if err := json.Unmarshal([]byte(settingsContent), &gotSettings); err != nil {
				t.Fatalf("generated settings.json is not valid JSON: %v", err)
			}
			if !reflect.DeepEqual(gotSettings, wantSettings) {
				t.Errorf("alias %q settings.json = %s, want %s", alias, settingsContent, golden)
			}

			tmpDir := t.TempDir()
			if err := Write(set, tmpDir); err != nil {
				t.Fatalf("Write failed: %v", err)
			}
			if err := Verify(set, tmpDir); err != nil {
				t.Errorf("Verify failed for antigravity-only set: %v", err)
			}
		})
	}
}

func TestEditor_Negative_UnknownEditorIDErrorsAndWritesNothing(t *testing.T) {
	set, err := Synthesize(Options{Editors: []string{"notarealeditor"}})
	if err == nil {
		t.Fatalf("expected error for unknown editor id, got set: %+v", set)
	}
	if set != nil {
		t.Errorf("expected nil set on unknown editor id, got %+v", set)
	}
	for _, want := range []string{"unknown editor id(s)", "notarealeditor", "supported:", EditorAntigravity} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestEditor_Boundary_MixedKnownAndUnknownEditorIDs(t *testing.T) {
	// A known id alongside an unknown one must fail the whole run rather than silently
	// synthesizing only the known subset (the bug this PR closes).
	set, err := Synthesize(Options{Editors: []string{"vscode", "notarealeditor"}})
	if err == nil {
		t.Fatalf("expected error for mixed known/unknown editors, got set: %+v", set)
	}
	if set != nil {
		t.Errorf("expected nil set on mixed known/unknown editors, got %+v", set)
	}
	if !strings.Contains(err.Error(), "notarealeditor") {
		t.Errorf("error %q does not name the unknown id", err.Error())
	}
}

func TestEditor_Boundary_DuplicateAntigravityAlias(t *testing.T) {
	set, err := Synthesize(Options{Editors: []string{"antigravity", "agy", "antigravity-ide"}})
	if err != nil {
		t.Fatalf("Synthesize with duplicate aliases failed: %v", err)
	}
	if len(set.Editors) != 1 || set.Editors[0] != EditorAntigravity {
		t.Errorf("duplicate aliases did not dedupe: %v", set.Editors)
	}
	if len(set.Files) != 3 {
		t.Errorf("expected 3 VS Code family files for deduped antigravity, got %d", len(set.Files))
	}
}
