package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestEditor_Positive_SynthesizeAllEditors(t *testing.T) {
	opts := DefaultOptions()
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

	// 1. VS Code / Cursor / Windsurf checks
	vscodeSettings, ok := fileMap[filepath.Join(".vscode", "settings.json")]
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

	vscodeTasks, ok := fileMap[filepath.Join(".vscode", "tasks.json")]
	if !ok {
		t.Errorf("missing .vscode/tasks.json")
	}
	var tasksJSON map[string]any
	if err := json.Unmarshal([]byte(vscodeTasks), &tasksJSON); err != nil {
		t.Errorf(".vscode/tasks.json is not valid JSON: %v", err)
	}

	// 2. JetBrains checks
	ideaInspection, ok := fileMap[filepath.Join(".idea", "inspectionProfiles", "standards.xml")]
	if !ok {
		t.Errorf("missing .idea/inspectionProfiles/standards.xml")
	}
	if !strings.Contains(ideaInspection, "HISS04ComplexityLOC") {
		t.Errorf("idea inspection profile missing HISS04ComplexityLOC")
	}

	// 3. Neovim checks
	nvimLua, ok := fileMap[filepath.Join("lua", "standards.lua")]
	if !ok {
		t.Errorf("missing lua/standards.lua")
	}
	if !strings.Contains(nvimLua, "standards_lsp") {
		t.Errorf("lua/standards.lua missing standards_lsp registration")
	}
	if !strings.Contains(nvimLua, "StandardsAudit") {
		t.Errorf("lua/standards.lua missing user commands")
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
	_ = os.WriteFile(tamperPath, []byte(`{"tampered": true}`), 0644)

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
		Editors: []string{"emacs", "nano", "gedit"},
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
	customBin := "/opt/cordana/custom-bin"
	set, err := Synthesize(Options{
		Editors:    []string{"vscode"},
		BinaryDir:  customBin,
		IncludeMCP: false,
		IncludeLSP: true,
	})
	if err != nil {
		t.Fatalf("Synthesize with custom options failed: %v", err)
	}

	var settingsContent string
	for _, f := range set.Files {
		if f.Path == filepath.Join(".vscode", "settings.json") {
			settingsContent = f.Content
			break
		}
	}

	if !strings.Contains(settingsContent, customBin+"/standards-lsp") {
		t.Errorf("settings missing custom binary path: %s", settingsContent)
	}
	if !strings.Contains(settingsContent, `"standards.mcp.enabled": false`) {
		t.Errorf("expected standards.mcp.enabled to be false: %s", settingsContent)
	}
}
