package editor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
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
)

// Options configures editor generation.
type Options struct {
	WorkspaceRoot string   `json:"workspace_root"`
	BinaryDir     string   `json:"binary_dir"`
	Archetype     string   `json:"archetype"`
	Editors       []string `json:"editors"`
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

// DefaultOptions returns standard configuration targeting all supported IDEs.
func DefaultOptions() Options {
	return Options{
		WorkspaceRoot: ".",
		BinaryDir:     "bin",
		Archetype:     "framework",
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
		IncludeLSP: true,
	}
}

// Synthesize generates all declared IDE configuration files with hermetic zero-lookup templates.
func Synthesize(opts Options) (*EditorConfigSet, error) {
	editors := normalizeEditors(opts.Editors)
	if len(editors) == 0 {
		return nil, errors.New("no valid editors declared for synthesis")
	}

	binDir := opts.BinaryDir
	if binDir == "" {
		binDir = "bin"
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

	files := dispatchEditorFiles(editorMap, opts, binDir, arch)

	return &EditorConfigSet{
		Editors: editors,
		Files:   files,
	}, nil
}

func dispatchEditorFiles(editorMap map[string]bool, opts Options, binDir, arch string) []GeneratedFile {
	var files []GeneratedFile
	if editorMap[EditorUniversal] {
		files = append(files, generateUniversalEditorConfig()...)
	}
	if editorMap[EditorVSCode] || editorMap[EditorCursor] || editorMap[EditorWindsurf] {
		files = append(files, generateVSCodeFamily(binDir, opts.IncludeLSP, arch)...)
	}
	for _, generator := range []struct {
		editor   string
		generate func(string) []GeneratedFile
	}{
		{EditorJetBrains, generateJetBrains},
		{EditorNeovim, func(arch string) []GeneratedFile { return generateNeovim(binDir, arch) }},
		{EditorZed, generateZed},
		{EditorHelix, generateHelix},
		{EditorEmacs, generateEmacs},
		{EditorFleet, generateFleet},
		{EditorSublime, generateSublime},
		{EditorVisualStudio, generateVisualStudio},
	} {
		if editorMap[generator.editor] {
			files = append(files, generator.generate(arch)...)
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

func generateVSCodeFamily(binDir string, includeLSP bool, arch string) []GeneratedFile {
	settings := buildVSCodeSettings(binDir, includeLSP, arch)
	extensions := buildVSCodeExtensions(arch)
	tasks := buildVSCodeTasks()

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

func buildVSCodeSettings(binDir string, includeLSP bool, arch string) string {
	data := map[string]any{
		"standards.lsp.enabled":         includeLSP,
		"standards.lsp.path":            fmt.Sprintf("${workspaceFolder}/%s/standards-lsp", binDir),
		"standards.lsp.trace.server":    "messages",
		"standards.sentinel.headroomMB": 1024,
	}

	if arch == "native-gpu-systems" {
		data["clangd.path"] = "clangd"
		data["clangd.arguments"] = []string{
			"--compile-commands-dir=core/build",
			"--header-insertion=never",
		}
		data["[c]"] = map[string]any{
			"editor.defaultFormatter": "llvm-vs-code-extensions.vscode-clangd",
			"editor.formatOnSave":     true,
		}
		data["[cpp]"] = map[string]any{
			"editor.defaultFormatter": "llvm-vs-code-extensions.vscode-clangd",
			"editor.formatOnSave":     true,
		}
	} else {
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

func buildVSCodeExtensions(arch string) string {
	recs := []string{
		"cordanaLLM.standards-vscode",
		"github.copilot",
		"eamodio.gitlens",
	}
	if arch == "native-gpu-systems" {
		recs = append(recs, "llvm-vs-code-extensions.vscode-clangd", "mesonbuild.mesonbuild")
	} else {
		recs = append(recs, "golang.go")
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

func buildVSCodeTasks() string {
	data := map[string]any{
		"version": "2.0.0",
		"tasks": []map[string]any{
			{
				"label":   "Standards: Verify All",
				"type":    "shell",
				"command": "make verify-all",
				"group": map[string]any{
					"kind":      "test",
					"isDefault": true,
				},
				"problemMatcher": []string{"$go"},
			},
			{
				"label":   "Standards: Build Binaries",
				"type":    "shell",
				"command": "make build",
				"group": map[string]any{
					"kind":      "build",
					"isDefault": true,
				},
				"problemMatcher": []string{"$go"},
			},
			{
				"label":          "Standards: Compile Context",
				"type":           "shell",
				"command":        "standardsctl compile-context",
				"problemMatcher": []string{},
			},
			{
				"label":          "Standards: Audit Invariants",
				"type":           "shell",
				"command":        "standardsctl audit",
				"problemMatcher": []string{},
			},
		},
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

func generateJetBrains(arch string) []GeneratedFile {
	extraTools := jetBrainsExtraTools(arch)
	inspectionProfile := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<component name="InspectionProjectProfileManager">
  <profile version="1.0">
    <option name="myName" value="standards" />
    <inspection_tool class="GoCyclomaticComplexity" enabled="true" level="ERROR" enabled_by_default="true">
      <option name="m_limit" value="10" />
    </inspection_tool>
    <inspection_tool class="GoUnhandledErrorResult" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="GoInfiniteFor" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS01DAGControlFlow" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS02BoundedLoops" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="HISS04ComplexityLOC" enabled="true" level="ERROR" enabled_by_default="true">
      <option name="maxLoc" value="75" />
      <option name="maxStatements" value="50" />
    </inspection_tool>
    <inspection_tool class="HISS07ZeroUnwrap" enabled="true" level="ERROR" enabled_by_default="true" />%s
  </profile>
</component>
`, extraTools)

	workspaceHooks := `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="InspectionProjectProfileManager">
    <settings>
      <option name="PROJECT_PROFILE" value="standards" />
      <version value="1.0" />
    </settings>
  </component>
  <component name="ExternalTools">
    <tool name="Standards Audit" showInMainMenu="true" showInEditor="true">
      <exec>
        <option name="COMMAND" value="make" />
        <option name="PARAMETERS" value="audit" />
        <option name="WORKING_DIRECTORY" value="$ProjectFileDir$" />
      </exec>
    </tool>
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

func neovimLuaConfig(binDir, arch string) string {
	ft := `"go"`
	if arch == "native-gpu-systems" {
		ft = `"c", "cpp", "cuda", "go", "python"`
	}
	return fmt.Sprintf(`-- cordanaLLM/praetor Neovim LSP and Tool Configuration
local lspconfig = require("lspconfig")
local configs = require("lspconfig.configs")

if not configs.standards_lsp then
  configs.standards_lsp = {
    default_config = {
      cmd = { "./%s/standards-lsp" },
      filetypes = { %s },
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

vim.api.nvim_create_user_command("StandardsAudit", function()
  vim.cmd("!standardsctl audit")
end, { desc = "Audit repository against declared HISS invariants" })

vim.api.nvim_create_user_command("StandardsCompileContext", function()
  vim.cmd("!standardsctl compile-context")
end, { desc = "Compile AGENTS.md cross-agent contexts" })

vim.api.nvim_create_user_command("StandardsVerifyAll", function()
  vim.cmd("!make verify-all")
end, { desc = "Run full standards verification pipeline" })
`, binDir, ft)
}

func generateNeovim(binDir, arch string) []GeneratedFile {
	luaConfig := neovimLuaConfig(binDir, arch)
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

func zedTasks() string {
	return `[
  {
    "label": "Standards: Verify All",
    "command": "make",
    "args": ["verify-all"],
    "use_new_terminal": false,
    "allow_concurrent_runs": false
  },
  {
    "label": "Standards: Audit",
    "command": "standardsctl",
    "args": ["audit"],
    "use_new_terminal": false
  },
  {
    "label": "Standards: Compile Context",
    "command": "standardsctl",
    "args": ["compile-context", "--verify"],
    "use_new_terminal": false
  }
]
`
}

func generateZed(arch string) []GeneratedFile {
	return []GeneratedFile{
		{
			Path:    filepath.Join(".zed", "settings.json"),
			Content: zedSettings(),
			Editor:  EditorZed,
		},
		{
			Path:    filepath.Join(".zed", "tasks.json"),
			Content: zedTasks(),
			Editor:  EditorZed,
		},
	}
}

func generateHelix(arch string) []GeneratedFile {
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
			Path:    filepath.Join(".helix", "config.toml"),
			Content: config,
			Editor:  EditorHelix,
		},
		{
			Path:    filepath.Join(".helix", "languages.toml"),
			Content: languages,
			Editor:  EditorHelix,
		},
	}
}

func generateEmacs(arch string) []GeneratedFile {
	content := `;;; Directory Local Variables
;;; For more information see (info "(emacs) Directory Variables")

((nil . ((indent-tabs-mode . nil)
         (fill-column . 100)
         (compile-command . "make verify-all")))
 (c-mode . ((c-basic-offset . 4)
            (c-file-style . "linux")))
 (c++-mode . ((c-basic-offset . 4)
              (c-file-style . "linux")))
 (python-mode . ((python-indent-offset . 4)))
 (go-mode . ((indent-tabs-mode . t)
             (tab-width . 4))))
`
	return []GeneratedFile{
		{
			Path:    ".dir-locals.el",
			Content: content,
			Editor:  EditorEmacs,
		},
	}
}

func generateFleet(arch string) []GeneratedFile {
	settings := `{
  "editor.tabSize": 4,
  "editor.insertSpaces": true,
  "editor.formatOnSave": true
}
`
	run := `{
  "configurations": [
    {
      "type": "command",
      "name": "Standards: Verify All",
      "program": "make",
      "args": ["verify-all"]
    },
    {
      "type": "command",
      "name": "Standards: Audit",
      "program": "standardsctl",
      "args": ["audit"]
    }
  ]
}
`
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

func generateSublime(arch string) []GeneratedFile {
	content := `{
  "folders": [
    {
      "path": "."
    }
  ],
  "build_systems": [
    {
      "name": "Standards: Verify All",
      "shell_cmd": "make verify-all",
      "working_dir": "$project_path"
    },
    {
      "name": "Standards: Audit",
      "shell_cmd": "standardsctl audit",
      "working_dir": "$project_path"
    }
  ],
  "settings": {
    "tab_size": 4,
    "translate_tabs_to_spaces": true,
    "trim_trailing_white_space_on_save": true,
    "ensure_newline_at_eof_on_save": true
  }
}
`
	return []GeneratedFile{
		{
			Path:    "standards.sublime-project",
			Content: content,
			Editor:  EditorSublime,
		},
	}
}

func generateVisualStudio(arch string) []GeneratedFile {
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
	if err := validateEditorFiles(set); err != nil {
		return err
	}
	if rootDir == "" {
		rootDir = "."
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultIOTimeout)
	defer cancel()

	limit := len(set.Files)

	for i := 0; i < limit; i++ {
		f := set.Files[i]
		fullPath := filepath.Join(rootDir, f.Path)

		// For .clang-tidy or existing custom .editorconfig, preserve existing file if present
		if (f.Path == ".clang-tidy" || f.Path == ".editorconfig") && fileExists(fullPath) {
			continue
		}

		if err := writeSingleFileWithContext(ctx, fullPath, f.Content); err != nil {
			return fmt.Errorf("failed writing %s: %w", fullPath, err)
		}
	}

	return nil
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

// Verify checks that all files in set exist and match content in rootDir.
func Verify(set *EditorConfigSet, rootDir string) error {
	if err := validateEditorFiles(set); err != nil {
		return err
	}
	if rootDir == "" {
		rootDir = "."
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultIOTimeout)
	defer cancel()

	limit := len(set.Files)
	for i := 0; i < limit && i < maxFilesToGenerate; i++ {
		f := set.Files[i]
		fullPath := filepath.Join(rootDir, f.Path)

		existingBytes, err := readSingleFileWithContext(ctx, fullPath)
		if err != nil {
			return fmt.Errorf("missing expected configuration file %s: %w", f.Path, err)
		}

		if f.Path == ".clang-tidy" || f.Path == ".editorconfig" {
			continue
		}

		if string(existingBytes) != f.Content {
			return fmt.Errorf("configuration file %s is out of sync with standards policy", f.Path)
		}
	}

	return nil
}

func readSingleFileWithContext(ctx context.Context, path string) ([]byte, error) {
	return contextopt.ReadSnapshot(ctx, path)
}
