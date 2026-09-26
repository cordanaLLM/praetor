package editor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

const (
	maxFilesToGenerate = 50
	maxLoopBound       = 1000
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
	EditorAntigravity  = "antigravity"
)

// supportedEditorIDs lists canonical editor identifiers in a stable, reader-facing order for
// the "unknown editor id" error. It intentionally excludes aliases: an operator sees the id to
// pass, not every spelling that resolves to it.
var supportedEditorIDs = []string{
	EditorUniversal, EditorVSCode, EditorCursor, EditorWindsurf, EditorJetBrains,
	EditorNeovim, EditorZed, EditorHelix, EditorEmacs, EditorFleet, EditorSublime,
	EditorVisualStudio, EditorAntigravity,
}

// Options configures editor generation.
type Options struct {
	WorkspaceRoot string   `json:"workspace_root"`
	BinaryDir     string   `json:"binary_dir"`
	Archetype     string   `json:"archetype"`
	Editors       []string `json:"editors"`
	// The fields below carry observed or explicitly declared repository capabilities.
	// Editor configuration is derived from them rather than assumed: a repository without
	// Go no longer receives Go settings, inspections or problem matchers, and an LSP path
	// or extension recommendation needs evidence before it is written (BUG-776..778).
	Languages []string  `json:"languages,omitempty"`
	Commands  []Command `json:"commands,omitempty"`
	// Complexity carries the ceilings the caller resolved from the repository's policy.
	// Limits left unset are completed from config.HISSComplexityCeiling, so an unadopted
	// workspace is told the ceilings its first audit will enforce (issue #360).
	Complexity        config.ComplexityPolicy   `json:"complexity,omitempty"`
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

// WriteOutcome names what Write resolved for one generated file.
type WriteOutcome string

// Write outcomes reported by WriteWithReport.
const (
	// WriteCreated means the file was absent and its template was written.
	WriteCreated WriteOutcome = "CREATED"
	// WriteMerged means missing managed values were added to an existing JSON file.
	WriteMerged WriteOutcome = "MERGED"
	// WritePresent means the existing file already held every managed value.
	WritePresent WriteOutcome = "PRESENT"
	// WriteRewritten means an existing non-JSON template file that differed was replaced.
	WriteRewritten WriteOutcome = "REWRITTEN"
	// WritePreserved means a differing or unreadable .editorconfig or .clang-tidy was
	// left untouched and is not verified.
	WritePreserved WriteOutcome = "PRESERVED"
)

// WriteResult records the resolved outcome for one generated file.
type WriteResult struct {
	Path    string       `json:"path"`
	Editor  string       `json:"editor"`
	Outcome WriteOutcome `json:"outcome"`
}

// WriteReport lists the outcome of every generated file in set order.
type WriteReport struct {
	Files []WriteResult `json:"files"`
}

// VerificationReport distinguishes files whose managed requirements were checked
// from preserved non-JSON files that have no format-aware verifier.
type VerificationReport struct {
	Verified            []string `json:"verified"`
	PreservedUnverified []string `json:"preserved_unverified"`
}

// DefaultOptions returns standard configuration targeting all supported IDEs.
func DefaultOptions() Options {
	return Options{
		WorkspaceRoot: ".",
		BinaryDir:     "bin",
		Archetype:     "framework",
		Editors:       append([]string(nil), supportedEditorIDs...),
		IncludeMCP:    true,
		IncludeLSP:    true,
	}
}

// Synthesize generates all declared IDE configuration files from observed repository
// capabilities. It observes the workspace, so it takes a background context; callers that
// already hold one should use SynthesizeContext.
func Synthesize(opts Options) (*EditorConfigSet, error) {
	return SynthesizeContext(context.Background(), opts)
}

// SynthesizeContext resolves one capability plan within the caller's bounded context and
// generates editor configuration from it.
//
// Deriving rather than assuming is the point. Configuration used to follow the archetype, so
// every repository that was not native received Go settings, a Go language server and $go
// problem matchers whether or not it contained a line of Go (BUG-776, BUG-778).
func SynthesizeContext(ctx context.Context, opts Options) (_ *EditorConfigSet, err error) {
	if ctx == nil {
		return nil, errors.New("editor synthesis requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, defaultIOTimeout)
	defer cancel()
	editors, unknown := normalizeEditors(opts.Editors)
	if len(unknown) > 0 {
		return nil, unknownEditorsError(unknown)
	}
	if len(editors) == 0 {
		return nil, errors.New("no valid editors declared for synthesis")
	}
	plan, err := resolvePlan(ctx, opts, editors)
	if err != nil {
		return nil, err
	}

	arch := opts.Archetype
	if arch == "" {
		arch = "framework"
	}

	editorMap := make(map[string]bool)
	limit := len(editors)
	if limit > maxLoopBound {
		limit = maxLoopBound
	}

	for i := 0; i < limit; i++ {
		e := editors[i]
		editorMap[e] = true
	}

	files, err := dispatchEditorFiles(editorMap, arch, plan)
	if err != nil {
		return nil, err
	}

	return &EditorConfigSet{
		Editors: editors,
		Files:   files,
	}, nil
}

func dispatchEditorFiles(editorMap map[string]bool, arch string, plan Plan) ([]GeneratedFile, error) {
	var files []GeneratedFile
	if editorMap[EditorUniversal] {
		files = append(files, generateUniversalEditorConfig()...)
	}
	if editorMap[EditorVSCode] || editorMap[EditorCursor] || editorMap[EditorWindsurf] || editorMap[EditorAntigravity] {
		files = append(files, generateVSCodeFamily(arch, plan, editorMap[EditorAntigravity])...)
	}
	for _, generator := range []struct {
		editor   string
		generate func(string, Plan) []GeneratedFile
	}{
		{EditorJetBrains, generateJetBrains},
		{EditorNeovim, generateNeovim},
		{EditorZed, generateZed},
		{EditorHelix, generateHelix},
		{EditorEmacs, generateEmacs},
		{EditorFleet, generateFleet},
		{EditorSublime, generateSublime},
	} {
		if editorMap[generator.editor] {
			files = append(files, generator.generate(arch, plan)...)
		}
	}
	// Visual Studio renders a shared template, which can fail, so it sits outside the
	// table of generators that cannot. It stays last, as it was in the table.
	if !editorMap[EditorVisualStudio] {
		return files, nil
	}
	tidy, err := generateVisualStudio()
	if err != nil {
		return nil, err
	}
	return append(files, tidy...), nil
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
	"antigravity": EditorAntigravity, "agy": EditorAntigravity, "antigravity-ide": EditorAntigravity,
}

// normalizeEditors resolves input editor ids/aliases to canonical ids, deduplicated and
// sorted. It also reports every input token that resolved to no known editor: a caller must
// treat a non-empty unknown as fatal rather than silently synthesizing only the known subset
// (BUG: an unrecognized --editors value previously vanished instead of failing the run).
func normalizeEditors(input []string) (normalized, unknown []string) {
	if len(input) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	limit := len(input)
	if limit > maxLoopBound {
		limit = maxLoopBound
	}

	for i := 0; i < limit; i++ {
		e, supported := editorAliases[strings.ToLower(strings.TrimSpace(input[i]))]
		if !supported {
			unknown = append(unknown, input[i])
			continue
		}

		if !seen[e] {
			seen[e] = true
			normalized = append(normalized, e)
		}
	}
	sort.Strings(normalized)
	return normalized, unknown
}

func generateVSCodeFamily(arch string, plan Plan, includeAntigravity bool) []GeneratedFile {
	settings := buildVSCodeSettings(arch, plan, includeAntigravity)
	extensions := buildVSCodeExtensions(plan)
	tasks := buildVSCodeTasks(plan)

	return []GeneratedFile{
		{
			Path:    ".vscode/settings.json",
			Content: settings,
			Editor:  EditorVSCode,
		},
		{
			Path:    ".vscode/extensions.json",
			Content: extensions,
			Editor:  EditorVSCode,
		},
		{
			Path:    ".vscode/tasks.json",
			Content: tasks,
			Editor:  EditorVSCode,
		},
	}
}

// antigravitySearchMaxWorkspaceFileCount raises Jetski's per-workspace embedding scan bound
// above its shipped default of 5,000 files. Confirmed against the installed IDE:
// resources/app/extensions/antigravity/package.json, contributes.configuration
// (antigravity.searchMaxWorkspaceFileCount, type integer, default 5000); 50000 matches issue
// #167's measured need on repositories with large dependency trees and multi-worktree
// checkouts, and is the value already carried in the operator's own workspace settings.
const antigravitySearchMaxWorkspaceFileCount = 50000

// buildWatcherExclude extends the confirmed core VS Code setting files.watcherExclude
// (schema: patternProperties ".*" -> boolean, resources/app/out/vs/workbench in the installed
// IDE; Antigravity is a VS Code fork and reads the same workspace setting). It keeps build
// output and the isolated gate/dogfood run worktrees under .standards/worktrees (see
// .gitignore) out of the file watcher, plus every private directory the plan resolved --
// Plan.PrivateDirs used to be resolved and then ignored (issue #365). Addresses issue #167
// item 2; run retention (#167 item 1) is a separate, still-open fix.
func buildWatcherExclude(plan Plan) map[string]any {
	exclude := map[string]any{
		"**/bin/**":                  true,
		"**/dist/**":                 true,
		"**/.standards/worktrees/**": true,
	}
	for i := 0; i < len(plan.PrivateDirs) && i < maxLoopBound; i++ {
		exclude["**/"+plan.PrivateDirs[i]+"/**"] = true
	}
	return exclude
}

func buildVSCodeSettings(arch string, plan Plan, includeAntigravity bool) string {
	data := map[string]any{
		"standards.sentinel.headroomMB": 1024,
	}
	// Settings that point at the Praetor language server are written only when the resolver
	// found it. They used to be emitted unconditionally, so a workspace with no bin/standards-lsp
	// received an enabled language server pointing at a file that does not exist (issue #365).
	if plan.LSPPath != "" {
		data["standards.lsp.enabled"] = true
		data["standards.lsp.path"] = "${workspaceFolder}/" + plan.LSPPath
		data["standards.lsp.trace.server"] = "messages"
	}

	if includeAntigravity {
		data["antigravity.searchMaxWorkspaceFileCount"] = antigravitySearchMaxWorkspaceFileCount
		data["files.watcherExclude"] = buildWatcherExclude(plan)
	}

	if arch == "native-gpu-systems" {
		data["clangd.path"] = "clangd"
		data["clangd.arguments"] = util.ClangdArguments()
		data["[c]"] = map[string]any{
			"editor.defaultFormatter": "llvm-vs-code-extensions.vscode-clangd",
			"editor.formatOnSave":     true,
		}
		data["[cpp]"] = map[string]any{
			"editor.defaultFormatter": "llvm-vs-code-extensions.vscode-clangd",
			"editor.formatOnSave":     true,
		}
	} else if hasLanguage(plan, "go") {
		data["go.useLanguageServer"] = true
		data["[go]"] = map[string]any{
			"editor.defaultFormatter": "golang.go",
			"editor.formatOnSave":     true,
			"editor.codeActionsOnSave": map[string]any{
				"source.organizeImports": "always",
			},
		}
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

// buildVSCodeExtensions recommends exactly the extensions the caller proved are published in
// the configured registry. Four ids were hard-coded here, so every repository was told to
// install them whether or not any evidence for them existed (issue #365).
func buildVSCodeExtensions(plan Plan) string {
	recs := plan.Extensions
	if recs == nil {
		recs = []string{}
	}
	data := map[string]any{
		"recommendations": recs,
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

// buildVSCodeTasks binds exactly the repository commands the plan resolved. Four commands
// were hard-coded here, so a repository with no Makefile was still given `make verify-all`
// and `make build` tasks (issue #365).
func buildVSCodeTasks(plan Plan) string {
	commands := planCommands(plan)
	tasks := make([]map[string]any, 0, len(commands))
	defaulted := make(map[string]bool, len(commands))
	for i := 0; i < len(commands); i++ {
		command := commands[i]
		task := map[string]any{
			"label":          command.Label,
			"type":           "shell",
			"command":        commandShellLine(command),
			"problemMatcher": goProblemMatcher(plan),
		}
		if command.Group != "" {
			// Exactly one task per group may be the default one.
			task["group"] = map[string]any{"kind": command.Group, "isDefault": !defaulted[command.Group]}
			defaulted[command.Group] = true
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

func jetBrainsExtraTools(arch string) string {
	if arch == "native-gpu-systems" {
		return `
    <inspection_tool class="ClangTidyInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="OCUnusedGlobalDeclarationInspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="OCUnusedMacroInspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="OCDFAInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyUnresolvedReferencesInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyPep8Inspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="PyBroadExceptionInspection" enabled="true" level="ERROR" enabled_by_default="true" />`
	}
	if arch == "app-service" {
		return `
    <inspection_tool class="PyUnresolvedReferencesInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyPep8Inspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="PyBroadExceptionInspection" enabled="true" level="ERROR" enabled_by_default="true" />`
	}
	return ""
}

// generateJetBrains projects the resolved complexity ceilings into the inspection profile.
// The limits used to be literals here, so a repository that tightened max_func_loc got an IDE
// that accepted functions its own audit rejects (issue #360).
func generateJetBrains(arch string, plan Plan) []GeneratedFile {
	inspectionProfile := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<component name="InspectionProjectProfileManager">
  <profile version="1.0">
    <option name="myName" value="standards" />
    <inspection_tool class="GoCyclomaticComplexity" enabled="true" level="ERROR" enabled_by_default="true">
      <option name="m_limit" value="%d" />
    </inspection_tool>
    <inspection_tool class="GoUnhandledErrorResult" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="GoInfiniteFor" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS01DAGControlFlow" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS02BoundedLoops" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS04ComplexityLOC" enabled="true" level="ERROR" enabled_by_default="true">
      <option name="maxLoc" value="%d" />
      <option name="maxStatements" value="%d" />
    </inspection_tool>
    <inspection_tool class="HISS07ZeroUnwrap" enabled="true" level="ERROR" enabled_by_default="true" />%s
  </profile>
</component>
`, plan.Complexity.MaxCyclomatic, plan.Complexity.MaxFuncLOC, plan.Complexity.MaxStatements,
		jetBrainsExtraTools(arch))

	return []GeneratedFile{
		{
			Path:    ".idea/inspectionProfiles/standards.xml",
			Content: inspectionProfile,
			Editor:  EditorJetBrains,
		},
		{
			Path:    ".idea/workspace.xml",
			Content: jetBrainsWorkspace(plan),
			Editor:  EditorJetBrains,
		},
	}
}

// jetBrainsWorkspace registers one external tool per resolved repository command. A single
// `make audit` tool was hard-coded here and offered in repositories with no Makefile (#365).
func jetBrainsWorkspace(plan Plan) string {
	var tools strings.Builder
	commands := planCommands(plan)
	for i := 0; i < len(commands); i++ {
		fmt.Fprintf(&tools, `
    <tool name="%s" showInMainMenu="true" showInEditor="true">
      <exec>
        <option name="COMMAND" value="%s" />
        <option name="PARAMETERS" value="%s" />
        <option name="WORKING_DIRECTORY" value="$ProjectFileDir$" />
      </exec>
    </tool>`, xmlAttr(commands[i].Label), xmlAttr(commands[i].Program),
			xmlAttr(strings.Join(commands[i].Args, " ")))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="InspectionProjectProfileManager">
    <settings>
      <option name="PROJECT_PROFILE" value="standards" />
      <version value="1.0" />
    </settings>
  </component>
  <component name="ExternalTools">%s
  </component>
</project>
`, tools.String())
}

// neovimLSPBlock registers the Praetor language server only when the plan resolved a real
// executable; `./bin/standards-lsp` used to be registered unconditionally (issue #365).
func neovimLSPBlock(plan Plan, arch string) string {
	if plan.LSPPath == "" {
		return ""
	}
	filetypes := `"go"`
	if arch == "native-gpu-systems" {
		filetypes = `"c", "cpp", "cuda", "go", "python"`
	}
	return fmt.Sprintf(`
if not configs.standards_lsp then
  configs.standards_lsp = {
    default_config = {
      cmd = { "./%s" },
      filetypes = { %s },
      root_dir = function(fname)
        return lspconfig.util.root_pattern(".standards.yaml", "meson.build", "go.mod", ".git")(fname)
      end,
      settings = {
        standards = {
          hissEnforcement = true,
          maxLOC = %d,
          maxStatements = %d,
        },
      },
    },
  }
end

lspconfig.standards_lsp.setup({})
`, plan.LSPPath, filetypes, plan.Complexity.MaxFuncLOC, plan.Complexity.MaxStatements)
}

// neovimCommandBlock defines one :Standards<Name> command per resolved repository command.
func neovimCommandBlock(plan Plan) string {
	var block strings.Builder
	commands := planCommands(plan)
	for i := 0; i < len(commands); i++ {
		fmt.Fprintf(&block, "\nvim.api.nvim_create_user_command(%s, function()\n  vim.cmd(%s)\nend, { desc = %s })\n",
			luaString(neovimCommandName(commands[i].Label, i)),
			luaString("!"+commandShellLine(commands[i])),
			luaString("Run the repository command "+commands[i].Label))
	}
	return block.String()
}

func neovimLuaConfig(plan Plan, arch string) string {
	return "-- Praetor Neovim LSP and Tool Configuration\n" +
		"local lspconfig = require(\"lspconfig\")\n" +
		"local configs = require(\"lspconfig.configs\")\n" +
		neovimLSPBlock(plan, arch) + neovimCommandBlock(plan)
}

func generateNeovim(arch string, plan Plan) []GeneratedFile {
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
			Path:    "lua/standards.lua",
			Content: neovimLuaConfig(plan, arch),
			Editor:  EditorNeovim,
		},
		{
			Path:    ".nvim.lua",
			Content: nvimRootLua,
			Editor:  EditorNeovim,
		},
	}
}

func generateUniversalEditorConfig() []GeneratedFile {
	content := `# http://editorconfig.org
root = true

[*]
indent_style = space
indent_size = 4
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true
max_line_length = 120

[*.{c,cpp,cc,cxx,h,hpp,cu,hip}]
indent_style = space
indent_size = 4

[*.py]
indent_style = space
indent_size = 4

[*.go]
indent_style = tab
indent_size = 4

[*.rs]
indent_style = space
indent_size = 4

[*.{json,yaml,yml}]
indent_style = space
indent_size = 2

[*.md]
indent_style = space
indent_size = 2
trim_trailing_whitespace = false

[Makefile]
indent_style = tab
`
	return []GeneratedFile{
		{
			Path:    ".editorconfig",
			Content: content,
			Editor:  EditorUniversal,
		},
	}
}

func zedSettings() string {
	return `{
  "format_on_save": "on",
  "buffer_font_size": 14,
  "tab_size": 4,
  "hard_tabs": false,
  "preferred_line_length": 120,
  "languages": {
    "C": {
      "tab_size": 4,
      "preferred_line_length": 100
    },
    "C++": {
      "tab_size": 4,
      "preferred_line_length": 100
    },
    "Python": {
      "tab_size": 4
    },
    "Go": {
      "hard_tabs": true,
      "tab_size": 4
    }
  },
  "lsp": {
    "clangd": {
      "binary": {
        "path_lookup": true
      }
    }
  }
}
`
}

// zedTasks binds the resolved repository commands; three were hard-coded here (issue #365).
func zedTasks(plan Plan) string {
	commands := planCommands(plan)
	tasks := make([]map[string]any, 0, len(commands))
	for i := 0; i < len(commands); i++ {
		tasks = append(tasks, map[string]any{
			"label":            commands[i].Label,
			"command":          commands[i].Program,
			"args":             commandArgs(commands[i]),
			"use_new_terminal": false,
		})
	}
	bytes, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return "[]\n"
	}
	return string(bytes) + "\n"
}

func generateZed(arch string, plan Plan) []GeneratedFile {
	return []GeneratedFile{
		{
			Path:    ".zed/settings.json",
			Content: zedSettings(),
			Editor:  EditorZed,
		},
		{
			Path:    ".zed/tasks.json",
			Content: zedTasks(plan),
			Editor:  EditorZed,
		},
	}
}

func generateHelix(_ string, _ Plan) []GeneratedFile {
	config := `theme = "default"

[editor]
line-number = "relative"
cursorline = true
color-modes = true
auto-format = true

[editor.whitespace.render]
space = "all"
tab = "all"
newline = "none"

[editor.indent-guides]
render = true
character = "│"
`
	languages := `# Language configurations for Helix
[[language]]
name = "c"
auto-format = true
formatter = { command = "clang-format" }

[[language]]
name = "cpp"
auto-format = true
formatter = { command = "clang-format" }

[[language]]
name = "python"
auto-format = true
formatter = { command = "ruff", args = ["format", "-"] }

[[language]]
name = "go"
auto-format = true
formatter = { command = "gofmt" }
`
	return []GeneratedFile{
		{
			Path:    ".helix/config.toml",
			Content: config,
			Editor:  EditorHelix,
		},
		{
			Path:    ".helix/languages.toml",
			Content: languages,
			Editor:  EditorHelix,
		},
	}
}

// generateEmacs binds compile-command only when the plan resolved a repository command;
// `make verify-all` was hard-coded here and offered in repositories with no Makefile (#365).
func generateEmacs(_ string, plan Plan) []GeneratedFile {
	var compile string
	if commands := planCommands(plan); len(commands) > 0 {
		compile = fmt.Sprintf("\n         (compile-command . %q)", commandShellLine(commands[0]))
	}
	content := fmt.Sprintf(`;;; Directory Local Variables
;;; For more information see (info "(emacs) Directory Variables")

((nil . ((indent-tabs-mode . nil)
         (fill-column . 100)%s))
 (c-mode . ((c-basic-offset . 4)
            (c-file-style . "linux")))
 (c++-mode . ((c-basic-offset . 4)
              (c-file-style . "linux")))
 (python-mode . ((python-indent-offset . 4)))
 (go-mode . ((indent-tabs-mode . t)
             (tab-width . 4))))
`, compile)
	return []GeneratedFile{
		{
			Path:    ".dir-locals.el",
			Content: content,
			Editor:  EditorEmacs,
		},
	}
}

func generateFleet(_ string, plan Plan) []GeneratedFile {
	settings := `{
  "editor.tabSize": 4,
  "editor.insertSpaces": true,
  "editor.formatOnSave": true
}
`
	return []GeneratedFile{
		{
			Path:    ".fleet/settings.json",
			Content: settings,
			Editor:  EditorFleet,
		},
		{
			Path:    ".fleet/run.json",
			Content: fleetRun(plan),
			Editor:  EditorFleet,
		},
	}
}

// fleetRun lists the resolved repository commands; two were hard-coded here (issue #365).
func fleetRun(plan Plan) string {
	commands := planCommands(plan)
	configurations := make([]map[string]any, 0, len(commands))
	for i := 0; i < len(commands); i++ {
		configurations = append(configurations, map[string]any{
			"type":    "command",
			"name":    commands[i].Label,
			"program": commands[i].Program,
			"args":    commandArgs(commands[i]),
		})
	}
	bytes, err := json.MarshalIndent(map[string]any{"configurations": configurations}, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(bytes) + "\n"
}

// generateSublime builds one build system per resolved repository command; two were
// hard-coded here and offered in repositories with no Makefile (issue #365).
func generateSublime(_ string, plan Plan) []GeneratedFile {
	commands := planCommands(plan)
	systems := make([]map[string]any, 0, len(commands))
	for i := 0; i < len(commands); i++ {
		systems = append(systems, map[string]any{
			"name":        commands[i].Label,
			"shell_cmd":   commandShellLine(commands[i]),
			"working_dir": "$project_path",
		})
	}
	data := map[string]any{
		"folders":       []map[string]any{{"path": "."}},
		"build_systems": systems,
		"settings": map[string]any{
			"tab_size":                          4,
			"translate_tabs_to_spaces":          true,
			"trim_trailing_white_space_on_save": true,
			"ensure_newline_at_eof_on_save":     true,
		},
	}
	content := "{}\n"
	if bytes, err := json.MarshalIndent(data, "", "  "); err == nil {
		content = string(bytes) + "\n"
	}
	return []GeneratedFile{
		{
			Path:    "standards.sublime-project",
			Content: content,
			Editor:  EditorSublime,
		},
	}
}

// clangTidyTemplate is the one .clang-tidy body praetor ships. The native-gpu-systems flavor
// scaffolds the same template (internal/flavor/definitions.go), so the file an adopter gets
// does not depend on which command created it.
const clangTidyTemplate = "native/.clang-tidy.tmpl"

func generateVisualStudio() ([]GeneratedFile, error) {
	tidy, err := templates.RenderFile(clangTidyTemplate, templates.Context{})
	if err != nil {
		return nil, fmt.Errorf("render Visual Studio clang-tidy configuration: %w", err)
	}
	return []GeneratedFile{
		{
			Path:    ".clang-tidy",
			Content: tidy,
			Editor:  EditorVisualStudio,
		},
	}, nil
}

func fileExists(path string) bool {
	return util.FileExists(path)
}

// isPreservedEditorFile reports whether an existing file carries hand-tuned project
// settings that Write never replaces.
func isPreservedEditorFile(path string) bool {
	return path == ".clang-tidy" || path == ".editorconfig"
}

// Write writes all generated files to the target workspace root directory; see
// WriteWithReport for how existing files are treated.
func Write(set *EditorConfigSet, rootDir string) error {
	_, err := WriteWithReport(set, rootDir)
	return err
}

// WriteWithReport resolves every generated file before the first mutation and
// reports the outcome per file. An existing JSON file keeps its unrelated keys,
// list entries and exact number literals while missing managed values are added;
// invalid JSON, duplicate keys or a conflicting managed value abort the whole run
// without writing any file. An existing .editorconfig or .clang-tidy is preserved,
// and any other existing file that differs from its template is rewritten.
func WriteWithReport(set *EditorConfigSet, rootDir string) (WriteReport, error) {
	report := WriteReport{Files: []WriteResult{}}
	if err := validateEditorFiles(set); err != nil {
		return report, err
	}
	if rootDir == "" {
		rootDir = "."
	}

	pending, err := prepareEditorWrites(set, rootDir)
	if err != nil {
		return report, err
	}
	return publishPreparedEditorFiles(pending, newEditorIOContext, writeSingleFileWithContext)
}

type pendingEditorWrite struct {
	path    string
	content string
	write   bool
	result  WriteResult
}

type editorIOContextFactory func() (context.Context, context.CancelFunc)

type editorFileWriter func(context.Context, string, string) error

// newEditorIOContext gives each bounded file operation its own progress budget. A single
// deadline shared by the whole generated set made the last file depend on cumulative host
// latency: a hosted Windows run exhausted five seconds after several successful durable
// writes. maxFilesToGenerate still bounds the total number of independent operations.
func newEditorIOContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultIOTimeout)
}

func prepareEditorWrites(set *EditorConfigSet, rootDir string) ([]pendingEditorWrite, error) {
	pending := make([]pendingEditorWrite, 0, len(set.Files))
	for _, file := range set.Files {
		ctx, cancel := newEditorIOContext()
		write, err := prepareEditorWrite(ctx, file, filepath.Join(rootDir, file.Path))
		cancel()
		if err != nil {
			return nil, err
		}
		pending = append(pending, write)
	}
	return pending, nil
}

func publishPreparedEditorFiles(pending []pendingEditorWrite, newContext editorIOContextFactory, writer editorFileWriter) (WriteReport, error) {
	report := WriteReport{Files: make([]WriteResult, 0, len(pending))}
	for _, write := range pending {
		if write.write {
			ctx, cancel := newContext()
			err := writer(ctx, write.path, write.content)
			cancel()
			if err != nil {
				return WriteReport{Files: []WriteResult{}}, fmt.Errorf("failed writing %s: %w", write.path, err)
			}
		}
		report.Files = append(report.Files, write.result)
	}
	return report, nil
}

func prepareEditorWrite(ctx context.Context, file GeneratedFile, fullPath string) (pendingEditorWrite, error) {
	write := pendingEditorWrite{path: fullPath, content: file.Content, write: true,
		result: WriteResult{Path: file.Path, Editor: file.Editor, Outcome: WriteCreated}}
	if !fileExists(fullPath) {
		return write, nil
	}
	existing, err := readSingleFileWithContext(ctx, fullPath)
	if err != nil {
		if isPreservedEditorFile(file.Path) {
			// Preserved files are never replaced, so an unreadable one is left as is.
			write.write, write.result.Outcome = false, WritePreserved
			return write, nil
		}
		return write, fmt.Errorf("read existing %s: %w", file.Path, err)
	}
	outcome, content, err := resolveExistingEditorFile(file, existing)
	if err != nil {
		return write, err
	}
	write.content, write.result.Outcome = content, outcome
	write.write = outcome == WriteMerged || outcome == WriteRewritten
	return write, nil
}

func resolveExistingEditorFile(file GeneratedFile, existing []byte) (WriteOutcome, string, error) {
	switch {
	case isJSONEditorFile(file.Path):
		merged, changed, err := mergeJSONDocument(existing, []byte(file.Content))
		if err != nil {
			return "", "", fmt.Errorf("cannot safely merge existing %s: %w", file.Path, err)
		}
		if !changed {
			return WritePresent, "", nil
		}
		return WriteMerged, string(merged), nil
	case string(existing) == file.Content:
		return WritePresent, "", nil
	case isPreservedEditorFile(file.Path):
		return WritePreserved, "", nil
	default:
		return WriteRewritten, file.Content, nil
	}
}

func validateEditorFiles(set *EditorConfigSet) error {
	if set == nil {
		return errors.New("cannot write nil config set")
	}
	if len(set.Files) > maxFilesToGenerate {
		return errors.New("editor output exceeds file bound")
	}
	for _, file := range set.Files {
		// Editor files are declared as slash paths (".vscode/settings.json"); cleanliness is
		// judged in that form. filepath.Clean returns backslashes on Windows and refused every
		// declared file. IsLocal still decides containment on the host.
		if !filepath.IsLocal(file.Path) || path.Clean(file.Path) != file.Path || file.Path == "." {
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

// VerifyWithReport checks that every generated file exists in rootDir. A JSON file
// must contain every managed value, with unrelated keys and list entries allowed;
// any other file must match its template exactly. An .editorconfig or .clang-tidy
// that differs from its template is preserved by Write, so it is reported as
// PreservedUnverified instead of being counted as verified.
func VerifyWithReport(set *EditorConfigSet, rootDir string) (VerificationReport, error) {
	report := VerificationReport{Verified: []string{}, PreservedUnverified: []string{}}
	if err := validateEditorFiles(set); err != nil {
		return report, err
	}
	if rootDir == "" {
		rootDir = "."
	}

	limit := len(set.Files)
	for i := 0; i < limit && i < maxFilesToGenerate; i++ {
		f := set.Files[i]
		ctx, cancel := newEditorIOContext()
		verified, err := verifyEditorFile(ctx, rootDir, f)
		cancel()
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
	switch {
	case isJSONEditorFile(file.Path):
		contains, err := jsonDocumentContains(existing, []byte(file.Content))
		if err != nil {
			return false, fmt.Errorf("cannot verify managed configuration in %s: %w", file.Path, err)
		}
		if !contains {
			return false, fmt.Errorf("configuration file %s is missing managed standards policy", file.Path)
		}
		return true, nil
	case string(existing) == file.Content:
		return true, nil
	case isPreservedEditorFile(file.Path):
		return false, nil
	default:
		return false, fmt.Errorf("configuration file %s is out of sync with standards policy", file.Path)
	}
}

func readSingleFileWithContext(ctx context.Context, path string) ([]byte, error) {
	return contextopt.ReadSnapshot(ctx, path)
}

// goProblemMatcher returns the Go problem matcher only when the repository actually contains
// Go. A matcher for a language that is not present parses build output that can never appear,
// and VS Code reports it as a task misconfiguration rather than ignoring it (BUG-778).
func goProblemMatcher(plan Plan) []string {
	if hasLanguage(plan, "go") {
		return []string{"$go"}
	}
	return []string{}
}
