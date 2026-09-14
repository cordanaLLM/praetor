package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxFilesToGenerate = 50
	maxLoopBound       = 1000
	maxJSONNodes       = 4096
	defaultIOTimeout   = 5 * time.Second
)

// Supported editor identifiers.
const (
	EditorVSCode       = "vscode"
	EditorCursor       = "cursor"
	EditorWindsurf     = "windsurf"
	EditorJetBrains    = "jetbrains"
	EditorNeovim       = "neovim"
	EditorUniversal    = "universal"
	EditorZed          = "zed"
	EditorHelix        = "helix"
	EditorEmacs        = "emacs"
	EditorFleet        = "fleet"
	EditorSublime      = "sublime"
	EditorVisualStudio = "visualstudio"
)

// Options configures editor generation.
type Options struct {
	WorkspaceRoot     string                    `json:"workspace_root"`
	BinaryDir         string                    `json:"binary_dir"`
	Archetype         string                    `json:"archetype"`
	Editors           []string                  `json:"editors"`
	Languages         []string                  `json:"languages,omitempty"`
	Commands          []Command                 `json:"commands,omitempty"`
	Extensions        []ExtensionRecommendation `json:"extensions,omitempty"`
	ExtensionRegistry string                    `json:"extension_registry,omitempty"`
	LSPPath           string                    `json:"lsp_path,omitempty"`
	PrivateDirs       []string                  `json:"private_dirs,omitempty"`
	// IncludeMCP is retained for input compatibility; MCP setup belongs to the
	// client setup pipeline and is never asserted by workspace settings.
	IncludeMCP bool `json:"include_mcp"`
	IncludeLSP bool `json:"include_lsp"`
}

// GeneratedFile holds relative path and payload of a synthesized configuration file.
type GeneratedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Editor  string `json:"editor"`
}

// EditorConfigSet holds the collection of generated configuration files across editors.
type EditorConfigSet struct {
	Editors []string        `json:"editors"`
	Files   []GeneratedFile `json:"files"`
}

// VerificationReport distinguishes files whose managed requirements were
// checked from preserved non-JSON files that have no format-aware verifier.
type VerificationReport struct {
	Verified            []string `json:"verified"`
	PreservedUnverified []string `json:"preserved_unverified"`
}

// DefaultOptions targets all supported IDEs without assuming a runtime, local
// binary, registry publication, or build command. Synthesize derives only
// observed repository capabilities and literal supported command targets.
func DefaultOptions() Options {
	return Options{
		WorkspaceRoot: ".",
		BinaryDir:     "bin",
		Archetype:     "",
		Editors: []string{
			EditorUniversal,
			EditorVSCode,
			EditorCursor,
			EditorWindsurf,
			EditorJetBrains,
			EditorNeovim,
			EditorZed,
			EditorHelix,
			EditorEmacs,
			EditorFleet,
			EditorSublime,
			EditorVisualStudio,
		},
		IncludeMCP: true,
		IncludeLSP: false,
	}
}

// Synthesize observes bounded local workspace capabilities, resolves one plan,
// and renders that same plan across the selected editor families.
func Synthesize(opts Options) (*EditorConfigSet, error) {
	return SynthesizeContext(context.Background(), opts)
}

// SynthesizeContext observes capabilities within the caller's bounded context.
func SynthesizeContext(ctx context.Context, opts Options) (*EditorConfigSet, error) {
	if ctx == nil {
		return nil, errors.New("editor synthesis requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultIOTimeout)
	defer cancel()
	plan, err := resolvePlan(ctx, opts)
	if err != nil {
		return nil, err
	}
	editorMap := make(map[string]bool)
	limit := len(plan.Editors)
	if limit > maxLoopBound {
		limit = maxLoopBound
	}

	for i := 0; i < limit; i++ {
		e := plan.Editors[i]
		editorMap[e] = true
	}

	files := dispatchEditorFiles(editorMap, plan)

	return &EditorConfigSet{
		Editors: plan.Editors,
		Files:   files,
	}, nil
}

func dispatchEditorFiles(editorMap map[string]bool, plan Plan) []GeneratedFile {
	var files []GeneratedFile
	if editorMap[EditorUniversal] {
		files = append(files, generateUniversalEditorConfig(plan)...)
	}
	if editorMap[EditorVSCode] || editorMap[EditorCursor] || editorMap[EditorWindsurf] {
		files = append(files, generateVSCodeFamily(plan)...)
	}
	for _, generator := range []struct {
		editor   string
		generate func(Plan) []GeneratedFile
	}{
		{EditorJetBrains, generateJetBrains},
		{EditorNeovim, generateNeovim},
		{EditorZed, generateZed},
		{EditorHelix, generateHelix},
		{EditorEmacs, generateEmacs},
		{EditorFleet, generateFleet},
		{EditorSublime, generateSublime},
		{EditorVisualStudio, generateVisualStudio},
	} {
		if editorMap[generator.editor] {
			files = append(files, generator.generate(plan)...)
		}
	}
	return files
}

var editorAliases = map[string]string{
	"universal": EditorUniversal, "editorconfig": EditorUniversal,
	"vscode": EditorVSCode, "code": EditorVSCode,
	"cursor": EditorCursor, "windsurf": EditorWindsurf,
	"jetbrains": EditorJetBrains, "goland": EditorJetBrains, "idea": EditorJetBrains, "intellij": EditorJetBrains,
	"neovim": EditorNeovim, "nvim": EditorNeovim,
	"zed": EditorZed, "helix": EditorHelix, "hx": EditorHelix,
	"emacs": EditorEmacs, "fleet": EditorFleet,
	"sublime": EditorSublime, "sublimetext": EditorSublime,
	"visualstudio": EditorVisualStudio, "vs": EditorVisualStudio,
}

func normalizeEditors(input []string) []string {
	if len(input) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var normalized []string
	limit := len(input)
	if limit > maxLoopBound {
		limit = maxLoopBound
	}

	for i := 0; i < limit; i++ {
		e, supported := editorAliases[strings.ToLower(strings.TrimSpace(input[i]))]
		if !supported {
			continue
		}

		if !seen[e] {
			seen[e] = true
			normalized = append(normalized, e)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func generateVSCodeFamily(plan Plan) []GeneratedFile {
	settings := buildVSCodeSettings(plan)
	extensions := buildVSCodeExtensions(plan)
	tasks := buildVSCodeTasks(plan)

	return []GeneratedFile{
		{
			Path:    filepath.Join(".vscode", "settings.json"),
			Content: settings,
			Editor:  EditorVSCode,
		},
		{
			Path:    filepath.Join(".vscode", "extensions.json"),
			Content: extensions,
			Editor:  EditorVSCode,
		},
		{
			Path:    filepath.Join(".vscode", "tasks.json"),
			Content: tasks,
			Editor:  EditorVSCode,
		},
	}
}

func buildVSCodeSettings(plan Plan) string {
	data := map[string]any{"files.insertFinalNewline": true}
	search := make(map[string]bool)
	watch := make(map[string]bool)
	for _, dir := range plan.PrivateDirs {
		search[dir+"/"] = true
		watch["**/"+dir+"/**"] = true
	}
	if len(search) > 0 {
		data["search.exclude"] = search
		data["files.watcherExclude"] = watch
	}
	if plan.LSPPath != "" {
		data["standards.lsp.enabled"] = true
		data["standards.lsp.path"] = "${workspaceFolder}/" + plan.LSPPath
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

func buildVSCodeExtensions(plan Plan) string {
	recommendations := plan.Extensions
	if recommendations == nil {
		recommendations = []string{}
	}
	data := map[string]any{
		"recommendations": recommendations,
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

func buildVSCodeTasks(plan Plan) string {
	tasks := make([]map[string]any, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		task := map[string]any{
			"label":          "Praetor: " + command.Label,
			"type":           "process",
			"command":        command.Program,
			"args":           command.Args,
			"problemMatcher": []string{},
		}
		if command.Group != "" {
			task["group"] = command.Group
		}
		tasks = append(tasks, task)
	}
	data := map[string]any{
		"version": "2.0.0",
		"tasks":   tasks,
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

func generateJetBrains(_ Plan) []GeneratedFile {
	inspectionProfile := `<?xml version="1.0" encoding="UTF-8"?>
<component name="InspectionProjectProfileManager">
  <profile version="1.0">
    <option name="myName" value="praetor" />
  </profile>
</component>
`

	workspaceHooks := `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="InspectionProjectProfileManager">
    <settings>
      <option name="PROJECT_PROFILE" value="praetor" />
      <version value="1.0" />
    </settings>
  </component>
</project>
`

	return []GeneratedFile{
		{
			Path:    filepath.Join(".idea", "inspectionProfiles", "standards.xml"),
			Content: inspectionProfile,
			Editor:  EditorJetBrains,
		},
		{
			Path:    filepath.Join(".idea", "workspace.xml"),
			Content: workspaceHooks,
			Editor:  EditorJetBrains,
		},
	}
}

func neovimLuaConfig(plan Plan) string {
	if plan.LSPPath == "" {
		return "-- Praetor: no verified Neovim language-server capability selected.\n"
	}
	return fmt.Sprintf(`-- cordanaLLM/praetor Neovim LSP configuration
local lspconfig = require("lspconfig")
local configs = require("lspconfig.configs")

if not configs.standards_lsp then
  configs.standards_lsp = {
    default_config = {
      cmd = { "./%s" },
      filetypes = { "go" },
      root_dir = function(fname)
        return lspconfig.util.root_pattern(".standards.yaml", "meson.build", "go.mod", ".git")(fname)
      end,
      settings = {
        standards = {
          hissEnforcement = true,
          maxLOC = 75,
          maxStatements = 50,
        },
      },
    },
  }
end

lspconfig.standards_lsp.setup({})
`, plan.LSPPath)
}

func generateNeovim(plan Plan) []GeneratedFile {
	luaConfig := neovimLuaConfig(plan)
	nvimRootLua := `-- Load project-level standards configuration
local status_ok, res = pcall(require, "standards")
if not status_ok then
  -- Fallback inline load if lua path is local
  local config_path = vim.fn.getcwd() .. "/lua/standards.lua"
  if vim.fn.filereadable(config_path) == 1 then
    dofile(config_path)
  end
end
`

	return []GeneratedFile{
		{
			Path:    filepath.Join("lua", "standards.lua"),
			Content: luaConfig,
			Editor:  EditorNeovim,
		},
		{
			Path:    ".nvim.lua",
			Content: nvimRootLua,
			Editor:  EditorNeovim,
		},
	}
}

func generateUniversalEditorConfig(plan Plan) []GeneratedFile {
	var content strings.Builder
	content.WriteString(`# http://editorconfig.org
root = true

[*]
indent_style = space
indent_size = 4
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true
max_line_length = 120
`)
	if hasLanguage(plan, "yaml") {
		content.WriteString("\n[*.{yaml,yml}]\nindent_style = space\nindent_size = 2\n")
	}
	if hasLanguage(plan, "markdown") {
		content.WriteString("\n[*.md]\nindent_style = space\nindent_size = 2\ntrim_trailing_whitespace = false\n")
	}
	if hasLanguage(plan, "make") {
		content.WriteString("\n[Makefile]\nindent_style = tab\n")
	}
	if hasLanguage(plan, "c") || hasLanguage(plan, "cpp") {
		content.WriteString("\n[*.{c,cpp,cc,cxx,h,hpp}]\nindent_style = space\nindent_size = 4\n")
	}
	if hasLanguage(plan, "python") {
		content.WriteString("\n[*.py]\nindent_style = space\nindent_size = 4\n")
	}
	if hasLanguage(plan, "go") {
		content.WriteString("\n[*.go]\nindent_style = tab\nindent_size = 4\n")
	}
	if hasLanguage(plan, "rust") {
		content.WriteString("\n[*.rs]\nindent_style = space\nindent_size = 4\n")
	}
	return []GeneratedFile{
		{
			Path:    ".editorconfig",
			Content: content.String(),
			Editor:  EditorUniversal,
		},
	}
}

func zedSettings(plan Plan) string {
	languages := make(map[string]any)
	for _, language := range plan.Languages {
		name := map[string]string{"c": "C", "cpp": "C++", "go": "Go", "python": "Python", "rust": "Rust", "svelte": "Svelte", "typescript": "TypeScript"}[language]
		if name != "" {
			languages[name] = map[string]any{"tab_size": 4}
		}
	}
	data := map[string]any{
		"format_on_save":        "off",
		"preferred_line_length": 120,
		"languages":             languages,
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

func zedTasks(plan Plan) string {
	tasks := make([]map[string]any, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		tasks = append(tasks, map[string]any{
			"label":                 "Praetor: " + command.Label,
			"command":               command.Program,
			"args":                  command.Args,
			"use_new_terminal":      false,
			"allow_concurrent_runs": false,
		})
	}
	bytes, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return "[]\n"
	}
	return string(bytes) + "\n"
}

func generateZed(plan Plan) []GeneratedFile {
	return []GeneratedFile{
		{
			Path:    filepath.Join(".zed", "settings.json"),
			Content: zedSettings(plan),
			Editor:  EditorZed,
		},
		{
			Path:    filepath.Join(".zed", "tasks.json"),
			Content: zedTasks(plan),
			Editor:  EditorZed,
		},
	}
}

func generateHelix(plan Plan) []GeneratedFile {
	config := `theme = "default"

[editor]
line-number = "relative"
cursorline = true
color-modes = true
auto-format = false

[editor.whitespace.render]
space = "all"
tab = "all"
newline = "none"

[editor.indent-guides]
render = true
character = "│"
`
	var languages strings.Builder
	languages.WriteString("# Praetor observed languages; formatter/LSP selection requires an explicit capability.\n")
	for _, language := range plan.Languages {
		if language == "markdown" || language == "make" || language == "shell" || language == "yaml" {
			continue
		}
		fmt.Fprintf(&languages, "\n[[language]]\nname = %q\nauto-format = false\n", language)
	}
	return []GeneratedFile{
		{
			Path:    filepath.Join(".helix", "config.toml"),
			Content: config,
			Editor:  EditorHelix,
		},
		{
			Path:    filepath.Join(".helix", "languages.toml"),
			Content: languages.String(),
			Editor:  EditorHelix,
		},
	}
}

func generateEmacs(plan Plan) []GeneratedFile {
	var modes strings.Builder
	if hasLanguage(plan, "c") {
		modes.WriteString("\n (c-mode . ((c-basic-offset . 4)))")
	}
	if hasLanguage(plan, "cpp") {
		modes.WriteString("\n (c++-mode . ((c-basic-offset . 4)))")
	}
	if hasLanguage(plan, "python") {
		modes.WriteString("\n (python-mode . ((python-indent-offset . 4)))")
	}
	if hasLanguage(plan, "go") {
		modes.WriteString("\n (go-mode . ((indent-tabs-mode . t) (tab-width . 4)))")
	}
	content := fmt.Sprintf(`;;; Directory Local Variables
;;; For more information see (info "(emacs) Directory Variables")

((nil . ((indent-tabs-mode . nil)
         (fill-column . 100)))%s)
`, modes.String())
	return []GeneratedFile{
		{
			Path:    ".dir-locals.el",
			Content: content,
			Editor:  EditorEmacs,
		},
	}
}

func generateFleet(plan Plan) []GeneratedFile {
	settings := `{
  "editor.tabSize": 4,
  "editor.insertSpaces": true,
  "editor.formatOnSave": false
}
`
	configurations := make([]map[string]any, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		configurations = append(configurations, map[string]any{"type": "command", "name": "Praetor: " + command.Label, "program": command.Program, "args": command.Args})
	}
	runBytes, err := json.MarshalIndent(map[string]any{"configurations": configurations}, "", "  ")
	if err != nil {
		runBytes = []byte("{}")
	}
	run := string(runBytes) + "\n"
	return []GeneratedFile{
		{
			Path:    filepath.Join(".fleet", "settings.json"),
			Content: settings,
			Editor:  EditorFleet,
		},
		{
			Path:    filepath.Join(".fleet", "run.json"),
			Content: run,
			Editor:  EditorFleet,
		},
	}
}

func generateSublime(plan Plan) []GeneratedFile {
	builds := make([]map[string]any, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		builds = append(builds, map[string]any{"name": "Praetor: " + command.Label, "cmd": append([]string{command.Program}, command.Args...), "working_dir": "$project_path"})
	}
	data := map[string]any{
		"folders":       []map[string]string{{"path": "."}},
		"build_systems": builds,
		"settings": map[string]any{"tab_size": 4, "translate_tabs_to_spaces": true,
			"trim_trailing_white_space_on_save": true, "ensure_newline_at_eof_on_save": true},
	}
	contentBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		contentBytes = []byte("{}")
	}
	content := string(contentBytes) + "\n"
	return []GeneratedFile{
		{
			Path:    "standards.sublime-project",
			Content: content,
			Editor:  EditorSublime,
		},
	}
}

func generateVisualStudio(plan Plan) []GeneratedFile {
	if !hasLanguage(plan, "c") && !hasLanguage(plan, "cpp") {
		return nil
	}
	tidy := `# cordanaLLM/praetor High-Integrity Systems Standards (HISS-16) Clang-Tidy Configuration
---
Checks: >
  -*,
  bugprone-*,
  cert-*,
  clang-analyzer-*,
  cppcoreguidelines-*,
  modernize-*,
  performance-*,
  readability-*,
  -readability-identifier-length

WarningsAsErrors: ''
HeaderFilterRegex: '.*'
FormatStyle: file
...
`
	return []GeneratedFile{
		{
			Path:    ".clang-tidy",
			Content: tidy,
			Editor:  EditorVisualStudio,
		},
	}
}

func fileExists(path string) bool {
	return util.FileExists(path)
}

// Write writes all generated files to the target workspace root directory.
func Write(set *EditorConfigSet, rootDir string) error {
	return WriteContext(context.Background(), set, rootDir)
}

// WriteContext merges and writes editor files within the caller's context.
func WriteContext(ctx context.Context, set *EditorConfigSet, rootDir string) error {
	if ctx == nil {
		return errors.New("editor write requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateEditorFiles(set); err != nil {
		return err
	}
	if rootDir == "" {
		rootDir = "."
	}

	ctx, cancel := context.WithTimeout(ctx, defaultIOTimeout)
	defer cancel()

	writes, err := prepareEditorWrites(ctx, set, rootDir)
	if err != nil {
		return err
	}
	for _, write := range writes {
		if err := writeSingleFileWithContext(ctx, write.path, write.content); err != nil {
			return fmt.Errorf("failed writing %s: %w", write.path, err)
		}
	}
	return nil
}

type pendingEditorWrite struct {
	path    string
	content string
}

// prepareEditorWrites resolves every merge before the first mutation so an
// unsupported human-file conflict cannot leave a partially updated workspace.
func prepareEditorWrites(ctx context.Context, set *EditorConfigSet, rootDir string) ([]pendingEditorWrite, error) {
	writes := make([]pendingEditorWrite, 0, len(set.Files))
	for _, file := range set.Files {
		fullPath := filepath.Join(rootDir, file.Path)
		write, err := prepareEditorWrite(ctx, file, fullPath)
		if err != nil {
			return nil, err
		}
		if write != nil {
			writes = append(writes, *write)
		}
	}
	return writes, nil
}

func prepareEditorWrite(ctx context.Context, file GeneratedFile, fullPath string) (*pendingEditorWrite, error) {
	if !fileExists(fullPath) {
		return &pendingEditorWrite{path: fullPath, content: file.Content}, nil
	}
	existing, err := readSingleFileWithContext(ctx, fullPath)
	if err != nil {
		return nil, fmt.Errorf("read existing %s: %w", file.Path, err)
	}
	if !isJSONEditorFile(file.Path) {
		return nil, legacyTextConflict(file.Path, existing, []byte(file.Content))
	}
	merged, changed, err := mergeJSONDocument(file.Path, existing, []byte(file.Content))
	if err != nil {
		return nil, fmt.Errorf("cannot safely merge existing %s: %w", file.Path, err)
	}
	if !changed {
		return nil, nil
	}
	return &pendingEditorWrite{path: fullPath, content: string(merged)}, nil
}

func validateEditorFiles(set *EditorConfigSet) error {
	if set == nil {
		return errors.New("cannot write nil config set")
	}
	if len(set.Files) > maxFilesToGenerate {
		return errors.New("editor output exceeds file bound")
	}
	for _, file := range set.Files {
		if !filepath.IsLocal(file.Path) || filepath.Clean(file.Path) != file.Path || file.Path == "." {
			return errors.New("editor output requires a clean relative file path")
		}
	}
	return nil
}

func writeSingleFileWithContext(ctx context.Context, path, content string) error {
	return contextopt.WriteSnapshot(ctx, path, []byte(content), 0o644)
}

// Verify is the compatibility wrapper for VerifyWithReport.
func Verify(set *EditorConfigSet, rootDir string) error {
	_, err := VerifyWithReport(set, rootDir)
	return err
}

// VerifyWithReport checks managed JSON requirements and exact generated files.
// Existing non-JSON files are preserved and reported as semantically unverified.
func VerifyWithReport(set *EditorConfigSet, rootDir string) (VerificationReport, error) {
	report := VerificationReport{Verified: []string{}, PreservedUnverified: []string{}}
	if err := validateEditorFiles(set); err != nil {
		return report, err
	}
	if rootDir == "" {
		rootDir = "."
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultIOTimeout)
	defer cancel()

	limit := len(set.Files)
	for i := 0; i < limit && i < maxFilesToGenerate; i++ {
		f := set.Files[i]
		verified, err := verifyEditorFile(ctx, rootDir, f)
		if err != nil {
			return report, err
		}
		if verified {
			report.Verified = append(report.Verified, f.Path)
		} else {
			report.PreservedUnverified = append(report.PreservedUnverified, f.Path)
		}
	}

	return report, nil
}

func verifyEditorFile(ctx context.Context, rootDir string, file GeneratedFile) (bool, error) {
	existing, err := readSingleFileWithContext(ctx, filepath.Join(rootDir, file.Path))
	if err != nil {
		return false, fmt.Errorf("missing expected configuration file %s: %w", file.Path, err)
	}
	desired := []byte(file.Content)
	if !isJSONEditorFile(file.Path) {
		if err := legacyTextConflict(file.Path, existing, desired); err != nil {
			return false, err
		}
		return bytes.Equal(existing, desired), nil
	}
	contains, err := jsonDocumentContainsForPath(file.Path, existing, desired)
	if err != nil {
		return false, fmt.Errorf("cannot verify managed configuration in %s: %w", file.Path, err)
	}
	if !contains {
		return false, fmt.Errorf("configuration file %s is missing managed standards policy", file.Path)
	}
	return true, nil
}

func readSingleFileWithContext(ctx context.Context, path string) ([]byte, error) {
	return contextopt.ReadSnapshot(ctx, path)
}

func isJSONEditorFile(path string) bool {
	return filepath.Ext(path) == ".json" || strings.HasSuffix(path, ".sublime-project")
}

func mergeJSONDocument(path string, existing, desired []byte) ([]byte, bool, error) {
	have, err := decodeEditorJSON(existing)
	if err != nil {
		return nil, false, fmt.Errorf("existing JSON is invalid: %w", err)
	}
	want, err := decodeEditorJSON(desired)
	if err != nil {
		return nil, false, fmt.Errorf("generated JSON is invalid: %w", err)
	}
	if err := legacyJSONConflict(path, have, want); err != nil {
		return nil, false, err
	}
	merged, changed, err := mergeJSONValue(have, want)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return existing, false, nil
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(data, '\n'), true, nil
}

type jsonMergeFrame struct {
	have   any
	want   any
	parent map[string]any
	key    string
}

func sortedJSONKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeJSONValue(have, want any) (any, bool, error) {
	root := have
	changed := false
	queue := []jsonMergeFrame{{have: have, want: want}}
	for index := 0; index < len(queue) && index < maxJSONNodes; index++ {
		frameChanged, err := mergeJSONFrameValue(&root, queue[index], &queue)
		if err != nil {
			return nil, false, err
		}
		changed = changed || frameChanged
	}
	return root, changed, nil
}

func mergeJSONFrameValue(root *any, frame jsonMergeFrame, queue *[]jsonMergeFrame) (bool, error) {
	switch desired := frame.want.(type) {
	case map[string]any:
		return mergeJSONObject(frame, desired, queue)
	case []any:
		return mergeJSONArray(root, frame, desired)
	default:
		if !reflect.DeepEqual(frame.have, frame.want) {
			return false, errors.New("managed scalar conflicts with existing value")
		}
		return false, nil
	}
}

func mergeJSONObject(frame jsonMergeFrame, desired map[string]any, queue *[]jsonMergeFrame) (bool, error) {
	existing, ok := frame.have.(map[string]any)
	if !ok {
		return false, errors.New("managed object conflicts with existing value")
	}
	changed := false
	for _, key := range sortedJSONKeys(desired) {
		desiredValue := desired[key]
		existingValue, found := existing[key]
		if !found {
			existing[key] = desiredValue
			changed = true
			continue
		}
		*queue = append(*queue, jsonMergeFrame{have: existingValue, want: desiredValue, parent: existing, key: key})
	}
	return changed, nil
}

func mergeJSONArray(root *any, frame jsonMergeFrame, desired []any) (bool, error) {
	existing, ok := frame.have.([]any)
	if !ok {
		return false, errors.New("managed array conflicts with existing value")
	}
	changed := false
	for _, desiredItem := range desired {
		if !jsonArrayContains(existing, desiredItem) {
			existing = append(existing, desiredItem)
			changed = true
		}
	}
	if frame.parent == nil {
		*root = existing
	} else {
		frame.parent[frame.key] = existing
	}
	return changed, nil
}

// JSONDocumentContains reports whether existing contains every managed value in
// desired while allowing unrelated human keys and list items.
func JSONDocumentContains(existing, desired []byte) (bool, error) {
	have, err := decodeEditorJSON(existing)
	if err != nil {
		return false, err
	}
	want, err := decodeEditorJSON(desired)
	if err != nil {
		return false, err
	}
	return jsonContains(have, want), nil
}

func jsonDocumentContainsForPath(path string, existing, desired []byte) (bool, error) {
	have, err := decodeEditorJSON(existing)
	if err != nil {
		return false, err
	}
	want, err := decodeEditorJSON(desired)
	if err != nil {
		return false, err
	}
	if err := legacyJSONConflict(path, have, want); err != nil {
		return false, err
	}
	return jsonContains(have, want), nil
}

func jsonContains(have, want any) bool {
	queue := []jsonMergeFrame{{have: have, want: want}}
	for index := 0; index < len(queue) && index < maxJSONNodes; index++ {
		if !jsonFrameContains(queue[index], &queue) {
			return false
		}
	}
	return len(queue) <= maxJSONNodes
}

func jsonFrameContains(frame jsonMergeFrame, queue *[]jsonMergeFrame) bool {
	switch desired := frame.want.(type) {
	case map[string]any:
		return jsonObjectContains(frame.have, desired, queue)
	case []any:
		return jsonArrayContainsAll(frame.have, desired)
	default:
		return reflect.DeepEqual(frame.have, frame.want)
	}
}

func jsonObjectContains(have any, desired map[string]any, queue *[]jsonMergeFrame) bool {
	existing, ok := have.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range sortedJSONKeys(desired) {
		existingValue, found := existing[key]
		if !found {
			return false
		}
		*queue = append(*queue, jsonMergeFrame{have: existingValue, want: desired[key]})
	}
	return true
}

func jsonArrayContainsAll(have any, desired []any) bool {
	existing, ok := have.([]any)
	if !ok {
		return false
	}
	for _, desiredItem := range desired {
		if !jsonArrayContains(existing, desiredItem) {
			return false
		}
	}
	return true
}

func jsonArrayContains(existing []any, desired any) bool {
	desiredID := jsonItemIdentity(desired)
	for i := 0; i < len(existing) && i < maxJSONNodes; i++ {
		item := existing[i]
		if reflect.DeepEqual(item, desired) {
			return true
		}
		if desiredID != "" && desiredID == jsonItemIdentity(item) {
			return true
		}
	}
	return false
}

func validateJSONNodeBound(root any) error {
	queue := []any{root}
	for index := 0; index < len(queue) && index < maxJSONNodes; index++ {
		switch value := queue[index].(type) {
		case map[string]any:
			for _, child := range value {
				queue = append(queue, child)
				if len(queue) > maxJSONNodes {
					return errors.New("editor JSON exceeds node bound")
				}
			}
		case []any:
			if len(value) > maxJSONNodes-len(queue) {
				return errors.New("editor JSON exceeds node bound")
			}
			queue = append(queue, value...)
		}
	}
	if len(queue) > maxJSONNodes {
		return errors.New("editor JSON exceeds node bound")
	}
	return nil
}

func jsonItemIdentity(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	program := stringJSONValue(item["program"])
	if program == "" {
		program = stringJSONValue(item["command"])
	}
	args := arrayJSONValue(item["args"])
	parts := []string{program}
	for _, arg := range args {
		if text, ok := arg.(string); ok {
			parts = append(parts, text)
		}
	}
	if len(args) == 0 {
		parts = strings.Fields(program)
	}
	return strings.Join(parts, "\x00")
}

func stringJSONValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func arrayJSONValue(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

func objectJSONValue(value any) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return object
}

func legacyTextConflict(path string, existing, desired []byte) error {
	markers := map[string][]string{
		".editorconfig": {"[*.go]"},
		filepath.Join(".helix", "languages.toml"): {`name = "go"`, "formatter = { command ="},
		".dir-locals.el": {"(go-mode"},
		filepath.Join(".idea", "inspectionProfiles", "standards.xml"): {"HISS04ComplexityLOC", "GoCyclomaticComplexity"},
		filepath.Join("lua", "standards.lua"):                         {"standards_lsp", "StandardsAudit", "StandardsCompileContext"},
	}
	for _, marker := range markers[path] {
		if strings.Contains(string(existing), marker) && !strings.Contains(string(desired), marker) {
			return fmt.Errorf("existing %s retains unsupported legacy editor policy %q", path, marker)
		}
	}
	return nil
}

func legacyJSONConflict(path string, have, want any) error {
	for _, check := range []func(string, any, any) error{
		legacyVSCodeSettingsConflict, legacyVSCodeExtensionConflict,
		legacyTaskConflict, legacyZedSettingsConflict,
	} {
		if err := check(path, have, want); err != nil {
			return err
		}
	}
	return nil
}

func legacyVSCodeSettingsConflict(path string, have, want any) error {
	if path != filepath.Join(".vscode", "settings.json") {
		return nil
	}
	existing, existingOK := have.(map[string]any)
	desired, desiredOK := want.(map[string]any)
	if !existingOK || !desiredOK {
		return nil
	}
	if key := unsupportedLegacySetting(existing, desired); key != "" {
		return fmt.Errorf("existing %s retains unsupported legacy key %q", path, key)
	}
	return nil
}

func legacyVSCodeExtensionConflict(path string, have, want any) error {
	if path != filepath.Join(".vscode", "extensions.json") {
		return nil
	}
	existing, existingOK := have.(map[string]any)
	desired, desiredOK := want.(map[string]any)
	if existingOK && desiredOK && legacyExtensionRecommendation(existing["recommendations"], desired["recommendations"]) {
		return fmt.Errorf("existing %s retains an unverified legacy extension recommendation", path)
	}
	return nil
}

func legacyTaskConflict(path string, have, want any) error {
	if legacyTaskItem(path, have, want) {
		return fmt.Errorf("existing %s retains an unsupported legacy task", path)
	}
	return nil
}

func legacyZedSettingsConflict(path string, have, want any) error {
	if path != filepath.Join(".zed", "settings.json") {
		return nil
	}
	existing, existingOK := have.(map[string]any)
	desired, desiredOK := want.(map[string]any)
	if existingOK && desiredOK && legacyZedGoLanguage(existing, desired) {
		return fmt.Errorf("existing %s retains an inapplicable legacy Go language block", path)
	}
	return nil
}

func unsupportedLegacySetting(existing, desired map[string]any) string {
	for _, key := range []string{"go.useLanguageServer", "[go]", "standards.lsp.trace.server", "standards.lsp.path"} {
		_, stale := existing[key]
		_, selected := desired[key]
		if stale && !selected {
			return key
		}
	}
	return ""
}

func legacyExtensionRecommendation(have, want any) bool {
	existing, ok := have.([]any)
	if !ok {
		return false
	}
	desired := arrayJSONValue(want)
	for i := 0; i < len(existing) && i < maxJSONNodes; i++ {
		id := stringJSONValue(existing[i])
		if id == "cordanaLLM.standards-vscode" && !jsonArrayContains(desired, id) {
			return true
		}
	}
	return false
}

func legacyTaskItem(path string, have, want any) bool {
	existing := taskArray(path, have)
	desired := taskArray(path, want)
	for i := 0; i < len(existing) && i < maxJSONNodes; i++ {
		item, ok := existing[i].(map[string]any)
		if !ok {
			continue
		}
		label := stringJSONValue(item[taskLabelKey(path)])
		if legacyTaskLabel(label) && !jsonArrayContains(desired, item) {
			return true
		}
		if label == "Standards: Verify All" && reflect.DeepEqual(item["problemMatcher"], []any{"$go"}) {
			return true
		}
	}
	return false
}

func taskArray(path string, root any) []any {
	if path == filepath.Join(".zed", "tasks.json") {
		return arrayJSONValue(root)
	}
	object, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	key := map[string]string{
		filepath.Join(".vscode", "tasks.json"): "tasks",
		filepath.Join(".fleet", "run.json"):    "configurations",
		"standards.sublime-project":            "build_systems",
	}[path]
	return arrayJSONValue(object[key])
}

func taskLabelKey(path string) string {
	if path == filepath.Join(".fleet", "run.json") || path == "standards.sublime-project" {
		return "name"
	}
	return "label"
}

func legacyTaskLabel(label string) bool {
	switch label {
	case "Standards: Build Binaries", "Standards: Audit", "Standards: Audit Invariants", "Standards: Compile Context":
		return true
	default:
		return false
	}
}

func legacyZedGoLanguage(existing, desired map[string]any) bool {
	existingLanguages := objectJSONValue(existing["languages"])
	desiredLanguages := objectJSONValue(desired["languages"])
	_, stale := existingLanguages["Go"]
	_, selected := desiredLanguages["Go"]
	return stale && !selected
}
