package editor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxFilesToGenerate = 50
	maxLoopBound       = 1000
	defaultIOTimeout   = 5 * time.Second
)

// Supported editor identifiers.
const (
	EditorVSCode    = "vscode"
	EditorCursor    = "cursor"
	EditorWindsurf  = "windsurf"
	EditorJetBrains = "jetbrains"
	EditorNeovim    = "neovim"
)

// Options configures editor generation.
type Options struct {
	WorkspaceRoot string   `json:"workspace_root"`
	BinaryDir     string   `json:"binary_dir"`
	Archetype     string   `json:"archetype"`
	Editors       []string `json:"editors"`
	IncludeMCP    bool     `json:"include_mcp"`
	IncludeLSP    bool     `json:"include_lsp"`
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
			EditorVSCode,
			EditorCursor,
			EditorWindsurf,
			EditorJetBrains,
			EditorNeovim,
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

	var files []GeneratedFile
	editorMap := make(map[string]bool)
	limit := len(editors)
	if limit > maxLoopBound {
		limit = maxLoopBound
	}

	for i := 0; i < limit; i++ {
		e := editors[i]
		editorMap[e] = true
	}

	if editorMap[EditorVSCode] || editorMap[EditorCursor] || editorMap[EditorWindsurf] {
		files = append(files, generateVSCodeFamily(binDir, opts.IncludeMCP, opts.IncludeLSP, arch)...)
	}

	if editorMap[EditorJetBrains] {
		files = append(files, generateJetBrains(arch)...)
	}

	if editorMap[EditorNeovim] {
		files = append(files, generateNeovim(binDir, arch)...)
	}

	return &EditorConfigSet{
		Editors: editors,
		Files:   files,
	}, nil
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
		e := strings.ToLower(strings.TrimSpace(input[i]))
		switch e {
		case "vscode", "code":
			e = EditorVSCode
		case "cursor":
			e = EditorCursor
		case "windsurf":
			e = EditorWindsurf
		case "jetbrains", "goland", "idea", "intellij":
			e = EditorJetBrains
		case "neovim", "nvim":
			e = EditorNeovim
		default:
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

func generateVSCodeFamily(binDir string, includeMCP, includeLSP bool, arch string) []GeneratedFile {
	settings := buildVSCodeSettings(binDir, includeMCP, includeLSP, arch)
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

func buildVSCodeSettings(binDir string, includeMCP, includeLSP bool, arch string) string {
	data := map[string]any{
		"standards.lsp.enabled":        includeLSP,
		"standards.lsp.path":           fmt.Sprintf("${workspaceFolder}/%s/standards-lsp", binDir),
		"standards.lsp.trace.server":   "messages",
		"standards.mcp.enabled":        includeMCP,
		"standards.mcp.path":           fmt.Sprintf("${workspaceFolder}/%s/standards-mcp", binDir),
		"standards.sentinel.headroomMB": 1024,
		"standards.modelTier":          "gemini-2.5-pro",
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

func generateJetBrains(arch string) []GeneratedFile {
	var extraTools string
	if arch == "native-gpu-systems" {
		extraTools = `
    <inspection_tool class="ClangTidyInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="OCUnusedGlobalDeclarationInspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="OCUnusedMacroInspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="OCDFAInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyUnresolvedReferencesInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyPep8Inspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="PyBroadExceptionInspection" enabled="true" level="ERROR" enabled_by_default="true" />`
	} else if arch == "app-service" {
		extraTools = `
    <inspection_tool class="PyUnresolvedReferencesInspection" enabled="true" level="ERROR" enabled_by_default="true" />
    <inspection_tool class="PyPep8Inspection" enabled="true" level="WARNING" enabled_by_default="true" />
    <inspection_tool class="PyBroadExceptionInspection" enabled="true" level="ERROR" enabled_by_default="true" />`
	}
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

func generateNeovim(binDir, arch string) []GeneratedFile {
	ft := `"go"`
	if arch == "native-gpu-systems" {
		ft = `"c", "cpp", "cuda", "go", "python"`
	}
	luaConfig := fmt.Sprintf(`-- cordanaLLM/standards Neovim LSP and Tool Configuration
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

	nvimRootLua := `-- Load project-level standards configuration
local status_ok, _ = pcall(require, "standards")
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

// Write writes all generated files to the target workspace root directory.
func Write(set *EditorConfigSet, rootDir string) error {
	if set == nil {
		return errors.New("cannot write nil config set")
	}
	if rootDir == "" {
		rootDir = "."
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultIOTimeout)
	defer cancel()

	limit := len(set.Files)
	if limit > maxFilesToGenerate {
		limit = maxFilesToGenerate
	}

	for i := 0; i < limit; i++ {
		f := set.Files[i]
		fullPath := filepath.Join(rootDir, f.Path)

		if err := writeSingleFileWithContext(ctx, fullPath, f.Content); err != nil {
			return fmt.Errorf("failed writing %s: %w", fullPath, err)
		}
	}

	return nil
}

func writeSingleFileWithContext(ctx context.Context, path, content string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed creating directory %s: %w", dir, err)
	}

	return os.WriteFile(path, []byte(content), 0644)
}

// Verify checks that all files in set exist and match content in rootDir.
func Verify(set *EditorConfigSet, rootDir string) error {
	if set == nil {
		return errors.New("cannot verify nil config set")
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

		if string(existingBytes) != f.Content {
			return fmt.Errorf("configuration file %s is out of sync with standards policy", f.Path)
		}
	}

	return nil
}

func readSingleFileWithContext(ctx context.Context, path string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return os.ReadFile(path)
}
