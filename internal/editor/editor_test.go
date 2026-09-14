package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEditorSafeDefaultsForEmptyAndPlanningRepositories(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		archetype string
		files     map[string]string
	}{
		{name: "empty", files: map[string]string{
			".worktrees/recovery/main.go":      "package recovery\n",
			".claude/worktrees/repair/main.go": "package repair\n",
		}},
		{name: "planning", archetype: "planning-artifacts", files: map[string]string{
			".standards.yaml": "profiles:\n  - planning-artifacts\n",
			"README.md":       "# Plan\n",
			"Makefile":        "build:\n\t@echo 'This repository has no build target' >&2\n\t@exit 1\n",
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := writeFixture(t, fixture.files)
			opts := DefaultOptions()
			opts.WorkspaceRoot = root
			opts.Archetype = fixture.archetype
			set := mustSynthesize(t, opts)
			content := allContent(set)
			for _, forbidden := range []string{"golang.go", "go.useLanguageServer", "standards-vscode", "standards-lsp", "make build"} {
				if strings.Contains(content, forbidden) {
					t.Errorf("safe defaults contain unsupported capability %q", forbidden)
				}
			}
			settings := fileContent(t, set, filepath.Join(".vscode", "settings.json"))
			for _, required := range []string{`".workingdir/": true`, `"**/.workingdir/**": true`} {
				if !strings.Contains(settings, required) {
					t.Errorf("VS Code private-path policy missing %s", required)
				}
			}
			if fixture.name == "empty" && strings.Contains(content, "[*.go]") {
				t.Fatal("nested recovery worktrees leaked a Go capability into the parent repository")
			}
		})
	}
}

func TestEditorGoCapabilityRequiresExistingExplicitLSP(t *testing.T) {
	root := writeFixture(t, map[string]string{"go.mod": "module example.test/repo\n", "main.go": "package main\n"})
	opts := DefaultOptions()
	opts.WorkspaceRoot = root
	opts.Editors = []string{EditorVSCode, EditorNeovim}
	opts.IncludeLSP = true
	opts.LSPPath = "bin/standards-lsp"

	withoutBinary := mustSynthesize(t, opts)
	if strings.Contains(allContent(withoutBinary), "standards_lsp") || strings.Contains(allContent(withoutBinary), "standards.lsp.path") {
		t.Fatal("missing local LSP binary was advertised")
	}
	writeTestFile(t, root, "bin/standards-lsp", "")
	if err := os.Chmod(filepath.Join(root, "bin", "standards-lsp"), 0o755); err != nil {
		t.Fatal(err)
	}
	withBinary := mustSynthesize(t, opts)
	for _, path := range []string{filepath.Join(".vscode", "settings.json"), filepath.Join("lua", "standards.lua")} {
		if !strings.Contains(fileContent(t, withBinary, path), "standards-lsp") && !strings.Contains(fileContent(t, withBinary, path), "standards_lsp") {
			t.Errorf("verified local LSP missing from %s", path)
		}
	}
	if strings.Contains(fileContent(t, withBinary, filepath.Join(".vscode", "extensions.json")), "golang.go") {
		t.Fatal("observing Go must not claim a registry extension")
	}
}

func TestEditorCapabilityObservationRejectsIncompleteOrAmbiguousEvidence(t *testing.T) {
	root := writeFixture(t, map[string]string{"Makefile": "verify-all := not-a-target\n"})
	opts := Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}}
	if strings.Contains(allContent(mustSynthesize(t, opts)), "verify-all") {
		t.Fatal("Make variable assignment was treated as an executable target")
	}
	writeTestFile(t, root, "Makefile", "verify-all:\n\t@true\n")
	if !strings.Contains(allContent(mustSynthesize(t, opts)), "verify-all") {
		t.Fatal("literal Make target was not observed")
	}
	many := t.TempDir()
	for index := 0; index < 1000; index++ {
		writeTestFile(t, many, filepath.Join("a", fmt.Sprintf("%04d.go", index)), "package fixture\n")
	}
	writeTestFile(t, many, "z.py", "print('visible')\n")
	languages, err := DetectWorkspaceLanguages(many)
	if err != nil || !slices.Contains(languages, "go") || !slices.Contains(languages, "python") {
		t.Fatalf("deduplication hid a language within the complete scan: %v, %v", languages, err)
	}

	bounded := t.TempDir()
	for i := 0; i <= maxWorkspaceFiles; i++ {
		writeTestFile(t, bounded, filepath.Join("files", strings.Repeat("x", 8)+string(rune(0x1000+i))), "")
	}
	if _, err := DetectWorkspaceLanguages(bounded); !errors.Is(err, errWorkspaceScanBound) {
		t.Fatalf("incomplete bounded scan was not reported: %v", err)
	}
}

func TestEditorLSPRejectsNonExecutableAndSymlink(t *testing.T) {
	root := writeFixture(t, map[string]string{"go.mod": "module fixture\n", "bin/standards-lsp": "binary"})
	opts := Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}, IncludeLSP: true, LSPPath: "bin/standards-lsp"}
	if strings.Contains(allContent(mustSynthesize(t, opts)), "standards.lsp.path") {
		t.Fatal("non-executable regular file was advertised as an LSP")
	}
	path := filepath.Join(root, "bin", "standards-lsp")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(allContent(mustSynthesize(t, opts)), "standards.lsp.path") {
		t.Fatal("symlink was advertised as a portable local LSP")
	}
}

func TestEditorMixedCapabilitiesAcrossFamilies(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n",
		"native.c":   "int main(void) { return 0; }\n", "native.hpp": "#pragma once\n",
		"ui.svelte": "<h1>fixture</h1>\n", "app.ts": "export const fixture = true;\n", "tool.py": "print('fixture')\n",
		"config.yaml": "enabled: true\n", "README.md": "# Fixture\n", "check.sh": "#!/bin/sh\n",
		"Makefile": "verify-all:\n\t@true\n\nbuild:\n\t@false\n",
	})
	opts := DefaultOptions()
	opts.WorkspaceRoot = root
	opts.ExtensionRegistry = "openvsx"
	opts.Extensions = []ExtensionRecommendation{
		{ID: "rust-lang.rust-analyzer", Registry: "openvsx", Verified: true},
		{ID: "cordanaLLM.standards-vscode", Registry: "openvsx", Verified: false},
		{ID: "ms-python.python", Registry: "marketplace", Verified: true},
	}
	set := mustSynthesize(t, opts)
	content := strings.ToLower(allContent(set))
	for _, language := range []string{"rust", "c", "c++", "python", "svelte", "typescript"} {
		if !strings.Contains(content, language) {
			t.Errorf("observed language %q is absent across generated families", language)
		}
	}
	for _, forbidden := range []string{"cordanallm.standards-vscode", "ms-python.python", "make build", "hiss04complexityloc", "clangtidyinspection"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("unsupported generated claim %q", forbidden)
		}
	}
	if !strings.Contains(content, "rust-lang.rust-analyzer") || !strings.Contains(content, "make\"") || !strings.Contains(content, "verify-all") {
		t.Fatal("verified extension or literal verify-all command missing")
	}
	if findFile(set, ".clang-tidy") == nil {
		t.Fatal("C/C++ repository did not receive applicable Visual Studio policy")
	}
}

func TestEditorWriteMergesJSONAndPreservesHumanFiles(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"Makefile":                "verify-all:\n\t@true\n",
		".vscode/settings.json":   `{"editor.fontSize":42}`,
		".vscode/extensions.json": `{"recommendations":["human.extension"],"unwantedRecommendations":["bad.extension"]}`,
		".vscode/tasks.json":      `{"version":"2.0.0","tasks":[{"label":"Human","type":"shell","command":"echo human"}]}`,
		".dir-locals.el":          "; human managed\n",
	})
	opts := DefaultOptions()
	opts.WorkspaceRoot = root
	opts.Editors = []string{EditorVSCode, EditorEmacs}
	set := mustSynthesize(t, opts)
	if err := Write(set, root); err != nil {
		t.Fatal(err)
	}
	assertContainsFile(t, root, ".vscode/settings.json", `"editor.fontSize": 42`, `"files.insertFinalNewline": true`)
	assertContainsFile(t, root, ".vscode/extensions.json", "human.extension", "unwantedRecommendations")
	assertContainsFile(t, root, ".vscode/tasks.json", "echo human", "verify-all")
	if got := mustRead(t, filepath.Join(root, ".dir-locals.el")); got != "; human managed\n" {
		t.Fatalf("human non-JSON config changed: %q", got)
	}
	if err := Verify(set, root); err != nil {
		t.Fatalf("merged configuration did not verify: %v", err)
	}
	report, err := VerifyWithReport(set, root)
	if err != nil || !slices.Contains(report.PreservedUnverified, ".dir-locals.el") {
		t.Fatalf("preserved non-JSON file was not reported as unverified: %+v, %v", report, err)
	}

	settingsPath := filepath.Join(root, ".vscode", "settings.json")
	writeTestFile(t, root, ".vscode/settings.json", `{"files.insertFinalNewline":false,"human":true}`)
	before := mustRead(t, settingsPath)
	conflictingSet := &EditorConfigSet{Files: append([]GeneratedFile{{Path: "would-have-been-created.json", Content: "{}\n"}}, set.Files...)}
	if err := Write(conflictingSet, root); err == nil || !strings.Contains(err.Error(), "cannot safely merge") {
		t.Fatalf("managed conflict was not rejected: %v", err)
	}
	if after := mustRead(t, settingsPath); after != before {
		t.Fatal("conflicting human configuration was mutated")
	}
	if _, err := os.Stat(filepath.Join(root, "would-have-been-created.json")); !os.IsNotExist(err) {
		t.Fatalf("conflict left a partial workspace mutation: %v", err)
	}
	if err := Verify(set, root); err == nil {
		t.Fatal("managed conflict unexpectedly verified")
	}
}

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
	if _, err := JSONDocumentContains([]byte(duplicate), []byte(desired)); err == nil {
		t.Fatal("JSON containment accepted ambiguous duplicate keys")
	}
}

func TestEditorAegisStyleVSCodeConfigurationVerifies(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"README.md": "# Planning repository\n", ".standards.yaml": "profiles:\n  - planning-artifacts\n",
		"Makefile": "verify-all:\n\t@true\n\nbuild:\n\t@echo unsupported >&2\n\t@exit 1\n",
		".vscode/settings.json": `{
  "editor.formatOnSave": false,
  "files.insertFinalNewline": true,
  "search.exclude": {".workingdir/": true, ".workingdir2/": true, "site/": true},
  "files.watcherExclude": {"**/.workingdir/**": true, "**/site/**": true},
  "rust-analyzer.check.command": "clippy",
  "standards.sentinel.headroomMB": 1024
}`,
		".vscode/extensions.json": `{
  "recommendations": ["rust-lang.rust-analyzer", "github.copilot"],
  "unwantedRecommendations": ["golang.go"]
}`,
		".vscode/tasks.json": `{
  "version": "2.0.0",
  "tasks": [
    {"label":"Aegis: preparation gate","type":"shell","command":"make verify-all","group":{"kind":"test","isDefault":true},"problemMatcher":[]},
    {"label":"Aegis: readiness","type":"shell","command":"make readiness","problemMatcher":[]}
  ]
}`,
	})
	opts := DefaultOptions()
	opts.WorkspaceRoot = root
	opts.Archetype = "planning-artifacts"
	opts.Editors = []string{EditorVSCode}
	set := mustSynthesize(t, opts)
	if err := Verify(set, root); err != nil {
		t.Fatalf("valid human-managed Aegis configuration rejected: %v", err)
	}
	content := allContent(set)
	for _, forbidden := range []string{"golang.go", "standards-lsp", "make build"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("planning fixture synthesized unsupported %q", forbidden)
		}
	}
}

func TestEditorKnownLegacyPolicyIsReportedAsConflict(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		editor  string
		path    string
		content string
	}{
		{name: "go settings", editor: EditorVSCode, path: ".vscode/settings.json", content: `{"files.insertFinalNewline":true,"go.useLanguageServer":true}`},
		{name: "unpublished extension", editor: EditorVSCode, path: ".vscode/extensions.json", content: `{"recommendations":["cordanaLLM.standards-vscode"]}`},
		{name: "go problem matcher", editor: EditorVSCode, path: ".vscode/tasks.json", content: `{"version":"2.0.0","tasks":[{"label":"Standards: Verify All","type":"shell","command":"make verify-all","problemMatcher":["$go"]}]}`},
		{name: "go editorconfig", editor: EditorUniversal, path: ".editorconfig", content: "root = true\n[*.go]\nindent_style = tab\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := writeFixture(t, map[string]string{"Makefile": "verify-all:\n\t@true\n"})
			opts := Options{WorkspaceRoot: root, Editors: []string{fixture.editor}}
			set := mustSynthesize(t, opts)
			if err := Write(set, root); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, root, fixture.path, fixture.content)
			before := mustRead(t, filepath.Join(root, fixture.path))
			if err := Verify(set, root); err == nil || !strings.Contains(err.Error(), "legacy") {
				t.Fatalf("legacy policy verified without conflict: %v", err)
			}
			if err := Write(set, root); err == nil || !strings.Contains(err.Error(), "legacy") {
				t.Fatalf("legacy policy regenerated without conflict: %v", err)
			}
			if after := mustRead(t, filepath.Join(root, fixture.path)); after != before {
				t.Fatal("legacy conflict was overwritten")
			}
		})
	}
}

func TestEditorSelectionAndInvalidInputs(t *testing.T) {
	root := t.TempDir()
	if _, err := Synthesize(Options{WorkspaceRoot: root}); err == nil {
		t.Fatal("empty editor selection accepted")
	}
	if _, err := Synthesize(Options{WorkspaceRoot: root, Editors: []string{"notepad"}}); err == nil {
		t.Fatal("unsupported editor selection accepted")
	}
	if _, err := Synthesize(Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}, Commands: []Command{{Label: "Bad", Program: "make verify-all"}}}); err == nil {
		t.Fatal("shell command string accepted as a process executable")
	}
	set := mustSynthesize(t, Options{WorkspaceRoot: root, Editors: []string{"nvim"}, Archetype: "framework"})
	if len(set.Files) != 2 || set.Editors[0] != EditorNeovim {
		t.Fatalf("alias/profile synthesis mismatch: %#v", set)
	}
	if !strings.Contains(allContent(set), `filetypes = { "go" }`) && strings.Contains(allContent(set), "standards_lsp") {
		t.Fatal("profile language should never imply an unverified LSP")
	}
	if err := Write(nil, root); err == nil {
		t.Fatal("nil config set accepted")
	}
	if err := Verify(nil, root); err == nil {
		t.Fatal("nil config set verified")
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

func findFile(set *EditorConfigSet, path string) *GeneratedFile {
	for i := range set.Files {
		if set.Files[i].Path == path {
			return &set.Files[i]
		}
	}
	return nil
}

func fileContent(t *testing.T, set *EditorConfigSet, path string) string {
	t.Helper()
	file := findFile(set, path)
	if file == nil {
		t.Fatalf("missing generated file %s", path)
	}
	return file.Content
}

func allContent(set *EditorConfigSet) string {
	var result strings.Builder
	for _, file := range set.Files {
		result.WriteString(file.Content)
	}
	return result.String()
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

func TestEditorGeneratedJSONIsValid(t *testing.T) {
	root := writeFixture(t, map[string]string{"main.py": "print('ok')\n"})
	set := mustSynthesize(t, Options{WorkspaceRoot: root, Editors: DefaultOptions().Editors})
	for _, file := range set.Files {
		if !isJSONEditorFile(file.Path) {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(file.Content), &value); err != nil {
			t.Errorf("%s is invalid JSON: %v", file.Path, err)
		}
	}
}
