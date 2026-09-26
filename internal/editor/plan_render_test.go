package editor

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Renderers state what the resolved plan proves and nothing else (#360, #365, BUG-445, BUG-809).

// lspBinaryRel is the workspace-relative language server path the resolver accepts on this
// host. Windows carries executability in the file extension, not a permission bit (HISS-21).
func lspBinaryRel(dir string) string {
	name := dir + "/" + lspBinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func writeExecutable(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// addCapabilityEvidence gives root every capability a renderer may assert: Go sources, a
// Makefile verify-all target and an executable language server under bin/.
func addCapabilityEvidence(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, root, "go.mod", "module fixture\n\ngo 1.27\n")
	writeTestFile(t, root, "Makefile", "verify-all:\n\t@true\n")
	writeExecutable(t, root, lspBinaryRel("bin"))
}

func evidenceWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	addCapabilityEvidence(t, root)
	return root
}

// assertWellFormedXML decodes every token so an escaping defect fails the test.
func assertWellFormedXML(t *testing.T, name, content string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(content))
	for i := 0; i < 10000; i++ {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatalf("%s is not well-formed XML: %v\n%s", name, err, content)
		}
	}
	t.Fatalf("%s exceeded the token bound", name)
}

func decodeJSON(t *testing.T, name, content string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(content), target); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", name, err, content)
	}
}

// ---- complexity policy (#360, BUG-445) ----------------------------------------------------

func TestPlanRender_Positive_ResolvedComplexityReachesJetBrainsAndNeovim(t *testing.T) {
	set := mustSynthesize(t, Options{
		WorkspaceRoot: evidenceWorkspace(t), Editors: []string{EditorJetBrains, EditorNeovim}, IncludeLSP: true,
		Complexity: config.ComplexityPolicy{MaxCyclomatic: 7, MaxCognitive: 9, MaxFuncLOC: 42, MaxStatements: 33},
	})
	idea := fileContent(t, set, ".idea/inspectionProfiles/standards.xml")
	assertWellFormedXML(t, "inspection profile", idea)
	for _, want := range []string{`name="m_limit" value="7"`, `name="maxLoc" value="42"`, `name="maxStatements" value="33"`} {
		if !strings.Contains(idea, want) {
			t.Errorf("inspection profile lacks %s:\n%s", want, idea)
		}
	}
	lua := fileContent(t, set, "lua/standards.lua")
	for _, want := range []string{"maxLOC = 42,", "maxStatements = 33,"} {
		if !strings.Contains(lua, want) {
			t.Errorf("neovim config lacks %s:\n%s", want, lua)
		}
	}
	for name, content := range map[string]string{"idea": idea, "lua": lua} {
		if strings.Contains(content, "75") || strings.Contains(content, `"50"`) {
			t.Errorf("%s still states a literal ceiling:\n%s", name, content)
		}
	}
}

func TestPlanRender_Boundary_UnsetComplexityIsTheAuditCeiling(t *testing.T) {
	root := evidenceWorkspace(t)
	ceiling := config.HISSComplexityCeiling()
	idea := fileContent(t, mustSynthesize(t, Options{WorkspaceRoot: root, Editors: []string{EditorJetBrains}}),
		".idea/inspectionProfiles/standards.xml")
	if !strings.Contains(idea, `name="maxLoc" value="60"`) || ceiling.MaxFuncLOC != config.AuditMaxFuncLOC {
		t.Errorf("an unresolved policy must state the audit length %d:\n%s", config.AuditMaxFuncLOC, idea)
	}

	// One resolved limit is kept; the others are completed from the ceiling.
	lua := fileContent(t, mustSynthesize(t, Options{
		WorkspaceRoot: root, Editors: []string{EditorNeovim}, IncludeLSP: true,
		Complexity: config.ComplexityPolicy{MaxFuncLOC: 1},
	}), "lua/standards.lua")
	if !strings.Contains(lua, "maxLOC = 1,") || !strings.Contains(lua, "maxStatements = 50,") {
		t.Errorf("partial policy not completed from the ceiling:\n%s", lua)
	}
}

// ---- language server (#365) ---------------------------------------------------------------

func TestPlanRender_Positive_LSPWrittenOnlyWithEvidence(t *testing.T) {
	root := evidenceWorkspace(t)
	writeExecutable(t, root, "tools/custom-lsp")
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"conventional bin dir", Options{IncludeLSP: true}, lspBinaryRel("bin")},
		{"explicit path", Options{IncludeLSP: true, LSPPath: "tools/custom-lsp"}, "tools/custom-lsp"},
	}
	for _, tc := range cases {
		if runtime.GOOS == "windows" && tc.opts.LSPPath != "" {
			continue // Windows needs an executable extension, which this explicit path lacks.
		}
		tc.opts.WorkspaceRoot, tc.opts.Editors = root, []string{EditorVSCode, EditorNeovim}
		set := mustSynthesize(t, tc.opts)
		var settings map[string]any
		decodeJSON(t, "settings", fileContent(t, set, ".vscode/settings.json"), &settings)
		if settings["standards.lsp.enabled"] != true || settings["standards.lsp.path"] != "${workspaceFolder}/"+tc.want {
			t.Errorf("%s: settings = %+v", tc.name, settings)
		}
		if lua := fileContent(t, set, "lua/standards.lua"); !strings.Contains(lua, `cmd = { "./`+tc.want+`" }`) {
			t.Errorf("%s: neovim does not register %s:\n%s", tc.name, tc.want, lua)
		}
	}
}

func TestPlanRender_Negative_NoLSPWithoutEvidence(t *testing.T) {
	proven := evidenceWorkspace(t)
	bare := t.TempDir()
	writeTestFile(t, bare, "go.mod", "module bare\n")
	nonGo := t.TempDir()
	writeTestFile(t, nonGo, "app.ts", "export const a = 1\n")
	writeExecutable(t, nonGo, lspBinaryRel("bin"))
	dirOnly := t.TempDir()
	writeTestFile(t, dirOnly, "go.mod", "module dir\n")
	if err := os.MkdirAll(filepath.Join(dirOnly, "bin", lspBinaryName), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Options{
		"binary absent":           {WorkspaceRoot: bare, IncludeLSP: true},
		"LSP not requested":       {WorkspaceRoot: proven, IncludeLSP: false},
		"no Go in the workspace":  {WorkspaceRoot: nonGo, IncludeLSP: true, Archetype: "pages-site"},
		"absolute binary dir":     {WorkspaceRoot: proven, IncludeLSP: true, BinaryDir: filepath.Join(proven, "bin")},
		"path escapes workspace":  {WorkspaceRoot: proven, IncludeLSP: true, LSPPath: "../bin/standards-lsp"},
		"directory, not a binary": {WorkspaceRoot: dirOnly, IncludeLSP: true},
	}
	if runtime.GOOS != "windows" { // Windows has no permission bit to withhold.
		plain := t.TempDir()
		writeTestFile(t, plain, "go.mod", "module plain\n")
		writeTestFile(t, plain, "bin/"+lspBinaryName, "not executable\n")
		cases["binary not executable"] = Options{WorkspaceRoot: plain, IncludeLSP: true}
	}
	for name, opts := range cases {
		opts.Editors = []string{EditorVSCode, EditorAntigravity, EditorNeovim}
		set := mustSynthesize(t, opts)
		if settings := fileContent(t, set, ".vscode/settings.json"); strings.Contains(settings, "standards.lsp") {
			t.Errorf("%s: settings assert a language server:\n%s", name, settings)
		}
		if lua := fileContent(t, set, "lua/standards.lua"); strings.Contains(lua, "standards_lsp") || strings.Contains(lua, "maxLOC") {
			t.Errorf("%s: neovim registers a language server:\n%s", name, lua)
		}
	}
}

// ---- extensions (#365, BUG-809) -----------------------------------------------------------

func TestPlanRender_ExtensionsFollowRegistryEvidence(t *testing.T) {
	goRoot := evidenceWorkspace(t)
	tsRoot := t.TempDir()
	writeTestFile(t, tsRoot, "app.ts", "export const a = 1\n")
	evidence := []ExtensionRecommendation{
		{ID: "golang.go", Registry: "open-vsx", Verified: true},
		{ID: "unverified.ext", Registry: "open-vsx", Verified: false},
		{ID: "elsewhere.ext", Registry: "marketplace", Verified: true},
	}
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{"Go workspace without evidence", Options{WorkspaceRoot: goRoot}, []string{}},
		{"non-Go workspace without evidence", Options{WorkspaceRoot: tsRoot, Archetype: "pages-site"}, []string{}},
		{"evidence without a registry", Options{WorkspaceRoot: goRoot, Extensions: evidence}, []string{}},
		{"verified in the configured registry", Options{WorkspaceRoot: goRoot, Extensions: evidence, ExtensionRegistry: "open-vsx"}, []string{"golang.go"}},
	}
	for _, tc := range cases {
		tc.opts.Editors = []string{EditorVSCode}
		var got struct {
			Recommendations []string `json:"recommendations"`
		}
		content := fileContent(t, mustSynthesize(t, tc.opts), ".vscode/extensions.json")
		decodeJSON(t, "extensions", content, &got)
		if got.Recommendations == nil || !slices.Equal(got.Recommendations, tc.want) {
			t.Errorf("%s: recommendations = %#v, want %#v", tc.name, got.Recommendations, tc.want)
		}
	}
}

// ---- repository commands (#365) -----------------------------------------------------------

var commandEditors = []string{EditorVSCode, EditorJetBrains, EditorNeovim, EditorZed, EditorEmacs, EditorFleet, EditorSublime}

func TestPlanRender_Boundary_EmptyCommandPlanBindsNothing(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module bare\n")
	set := mustSynthesize(t, Options{WorkspaceRoot: root, Editors: commandEditors})

	var tasks struct {
		Tasks []any `json:"tasks"`
	}
	decodeJSON(t, "vscode tasks", fileContent(t, set, ".vscode/tasks.json"), &tasks)
	var zed []any
	decodeJSON(t, "zed tasks", fileContent(t, set, ".zed/tasks.json"), &zed)
	var fleet struct {
		Configurations []any `json:"configurations"`
	}
	decodeJSON(t, "fleet run", fileContent(t, set, ".fleet/run.json"), &fleet)
	var sublime struct {
		BuildSystems []any `json:"build_systems"`
	}
	decodeJSON(t, "sublime", fileContent(t, set, "standards.sublime-project"), &sublime)
	if tasks.Tasks == nil || len(tasks.Tasks)+len(zed)+len(fleet.Configurations)+len(sublime.BuildSystems) != 0 || zed == nil {
		t.Errorf("an empty command plan produced commands: vscode=%v zed=%v fleet=%v sublime=%v",
			tasks.Tasks, zed, fleet.Configurations, sublime.BuildSystems)
	}
	workspace := fileContent(t, set, ".idea/workspace.xml")
	assertWellFormedXML(t, "jetbrains workspace", workspace)
	for path, forbidden := range map[string]string{
		".idea/workspace.xml": "<tool ", "lua/standards.lua": "nvim_create_user_command", ".dir-locals.el": "compile-command",
	} {
		if content := fileContent(t, set, path); strings.Contains(content, forbidden) {
			t.Errorf("%s binds a command without a plan:\n%s", path, content)
		}
	}
	for _, file := range set.Files {
		if strings.Contains(file.Content, "make ") || strings.Contains(file.Content, "praetorctl ") {
			t.Errorf("%s names a command the repository never proved:\n%s", file.Path, file.Content)
		}
	}
}

func TestPlanRender_Positive_MakefileVerifyAllReachesEveryEditor(t *testing.T) {
	set := mustSynthesize(t, Options{WorkspaceRoot: evidenceWorkspace(t), Editors: commandEditors})
	for path, want := range map[string]string{
		".vscode/tasks.json":        `"command": "make verify-all"`,
		".idea/workspace.xml":       `<option name="PARAMETERS" value="verify-all" />`,
		"lua/standards.lua":         `"StandardsVerifyAll"`,
		".zed/tasks.json":           `"verify-all"`,
		".dir-locals.el":            `(compile-command . "make verify-all")`,
		".fleet/run.json":           `"program": "make"`,
		"standards.sublime-project": `"shell_cmd": "make verify-all"`,
	} {
		if content := fileContent(t, set, path); !strings.Contains(content, want) {
			t.Errorf("%s lacks %s:\n%s", path, want, content)
		}
	}
}

func TestPlanRender_Positive_ExplicitCommandsAreEscapedAndGrouped(t *testing.T) {
	commands := []Command{
		{Label: "Lint & <Check>", Program: "make", Args: []string{"lint"}, Group: "test"},
		{Label: "Unit", Program: "go", Args: []string{"test", "./..."}, Group: "test"},
		{Label: `Say "hi"`, Program: "echo"},
	}
	set := mustSynthesize(t, Options{WorkspaceRoot: evidenceWorkspace(t), Editors: commandEditors, Commands: commands})

	var tasks struct {
		Tasks []struct {
			Label   string         `json:"label"`
			Command string         `json:"command"`
			Group   map[string]any `json:"group"`
		} `json:"tasks"`
	}
	decodeJSON(t, "vscode tasks", fileContent(t, set, ".vscode/tasks.json"), &tasks)
	if len(tasks.Tasks) != 3 || tasks.Tasks[1].Command != "go test ./..." || tasks.Tasks[2].Command != "echo" {
		t.Fatalf("tasks = %+v", tasks.Tasks)
	}
	if tasks.Tasks[0].Group["isDefault"] != true || tasks.Tasks[1].Group["isDefault"] != false || tasks.Tasks[2].Group != nil {
		t.Errorf("exactly the first task of a group is its default: %+v", tasks.Tasks)
	}
	workspace := fileContent(t, set, ".idea/workspace.xml")
	assertWellFormedXML(t, "jetbrains workspace", workspace)
	if !strings.Contains(workspace, "Lint &amp; &lt;Check&gt;") {
		t.Errorf("label not escaped:\n%s", workspace)
	}
	lua := fileContent(t, set, "lua/standards.lua")
	for _, want := range []string{`"LintCheck"`, `"Unit"`, `"SayHi"`, `"Run the repository command Say \"hi\""`} {
		if !strings.Contains(lua, want) {
			t.Errorf("neovim lacks %s:\n%s", want, lua)
		}
	}
	var zed []struct {
		Args []string `json:"args"`
	}
	decodeJSON(t, "zed tasks", fileContent(t, set, ".zed/tasks.json"), &zed)
	if len(zed) != 3 || zed[2].Args == nil {
		t.Errorf("a command without arguments must encode as [], got %+v", zed)
	}
}

func TestPlanRender_Negative_InvalidCommandsRejected(t *testing.T) {
	for _, bad := range []Command{
		{Label: "", Program: "make"},
		{Label: "spaces", Program: "make verify-all"},
		{Label: "absolute", Program: filepath.Join(t.TempDir(), "tool")},
	} {
		if _, err := Synthesize(Options{WorkspaceRoot: t.TempDir(), Editors: []string{EditorVSCode}, Commands: []Command{bad}}); err == nil {
			t.Errorf("command %+v accepted", bad)
		}
	}
}

// ---- private directories (#365) -----------------------------------------------------------

func TestPlanRender_PrivateDirsReachWatcherExclude(t *testing.T) {
	root := t.TempDir()
	exclude := func(dirs []string) map[string]any {
		var settings struct {
			Exclude map[string]any `json:"files.watcherExclude"`
		}
		content := fileContent(t, mustSynthesize(t, Options{WorkspaceRoot: root, Editors: []string{EditorAntigravity}, PrivateDirs: dirs}),
			".vscode/settings.json")
		decodeJSON(t, "settings", content, &settings)
		return settings.Exclude
	}
	defaults := exclude(nil)
	for _, want := range []string{"**/.workingdir/**", "**/.workingdir2/**", "**/bin/**"} {
		if defaults[want] != true {
			t.Errorf("default exclusions lack %s: %v", want, defaults)
		}
	}
	custom := exclude([]string{".agent-state"})
	if custom["**/.agent-state/**"] != true || custom["**/.workingdir/**"] != nil {
		t.Errorf("custom private dirs not applied: %v", custom)
	}
	if none := exclude([]string{}); none["**/.workingdir/**"] != nil || none["**/dist/**"] != true {
		t.Errorf("an empty private-dir list must keep only the build exclusions: %v", none)
	}
}

// ---- render helpers -----------------------------------------------------------------------

func TestRenderHelpers(t *testing.T) {
	if got := luaString("a\\b\"c\nd\re"); got != `"a\\b\"c\nd\re"` {
		t.Errorf("luaString = %s", got)
	}
	if got := luaString(strings.Repeat("x", 5000)); len(got) != 5002 {
		t.Errorf("luaString truncated a long value to %d bytes", len(got))
	}
	for label, want := range map[string]string{
		"Standards: Verify All": "StandardsVerifyAll",
		"":                      "StandardsCommandC",
		"123 go":                "StandardsCommandC",
		"-- ::":                 "StandardsCommandC",
		"Überprüfen":            "BerprFen",
	} {
		if got := neovimCommandName(label, 2); got != want {
			t.Errorf("neovimCommandName(%q) = %q, want %q", label, got, want)
		}
	}
	if got := neovimCommandName(strings.Repeat("a", 500), 0); len(got) != maxCommandNameRunes {
		t.Errorf("command name not bounded: %d", len(got))
	}
	if got := commandShellLine(Command{Program: "make"}); got != "make" {
		t.Errorf("commandShellLine = %q", got)
	}
	if got := commandArgs(Command{}); got == nil || len(got) != 0 {
		t.Errorf("commandArgs(nil) = %#v", got)
	}
	long := make([]Command, maxLoopBound+5)
	if got := planCommands(Plan{Commands: long}); len(got) != maxLoopBound {
		t.Errorf("planCommands not bounded: %d", len(got))
	}
}
