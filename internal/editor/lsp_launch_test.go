package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The language server path is the command a host launches, not the file it resolves to, so a
// built Windows checkout and a Unix one render the same .vscode/settings.json (HISS-21).

// lspOnlyWorkspace is a Go workspace holding exactly the given executables and directories.
func lspOnlyWorkspace(t *testing.T, executables, dirs []string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module fixture\n\ngo 1.27\n")
	for _, rel := range executables {
		writeExecutable(t, root, rel)
	}
	for _, rel := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func resolveFor(t *testing.T, root, explicit, goos string) string {
	t.Helper()
	return resolveLSPPathFor(Options{IncludeLSP: true, LSPPath: explicit}, root, []string{"go"}, goos)
}

// Positive: a built bin/standards-lsp.exe is the Windows launch target of bin/standards-lsp, and
// the rendered settings name the extension-less command on every host.
func TestLSPLaunch_Positive_BuiltWindowsBinaryKeepsCommandName(t *testing.T) {
	root := lspOnlyWorkspace(t, []string{"bin/standards-lsp.exe"}, nil)
	if got := resolveFor(t, root, "", "windows"); got != "bin/standards-lsp" {
		t.Fatalf("windows resolved %q, want the command bin/standards-lsp", got)
	}
	set := mustSynthesize(t, Options{WorkspaceRoot: root, Editors: []string{EditorVSCode}, IncludeLSP: true})
	var settings map[string]any
	decodeJSON(t, "settings", fileContent(t, set, ".vscode/settings.json"), &settings)
	got, present := settings["standards.lsp.path"]
	if runtime.GOOS == "windows" && got != "${workspaceFolder}/bin/standards-lsp" {
		t.Errorf("windows settings name %v, want ${workspaceFolder}/bin/standards-lsp", got)
	}
	if runtime.GOOS != "windows" && present {
		t.Errorf("execve does not append .exe, yet settings name %v", got)
	}
}

// Negative: files the host would not start for the command are not a language server.
func TestLSPLaunch_Negative_UnlaunchableFilesAreNotAServer(t *testing.T) {
	cases := map[string]struct {
		executables, dirs []string
		goos              string
	}{
		"windows, extension-less file only": {executables: []string{"bin/standards-lsp"}, goos: "windows"},
		"windows, .bat is never appended":   {executables: []string{"bin/standards-lsp.bat"}, goos: "windows"},
		"windows, .exe is a directory":      {dirs: []string{"bin/standards-lsp.exe"}, goos: "windows"},
		"posix, .exe file only":             {executables: []string{"bin/standards-lsp.exe"}, goos: "linux"},
	}
	for name, tc := range cases {
		root := lspOnlyWorkspace(t, tc.executables, tc.dirs)
		if got := resolveFor(t, root, "", tc.goos); got != "" {
			t.Errorf("%s: resolved %q, want no language server", name, got)
		}
	}
	for _, batch := range []string{"tools/lsp.bat", "tools/lsp.cmd"} {
		// Node's process_wrap rejects batch files in a shell-less spawn.
		if got := resolveFor(t, lspOnlyWorkspace(t, []string{batch}, nil), batch, "windows"); got != "" {
			t.Errorf("explicit batch file %s resolved %q", batch, got)
		}
	}
	root := lspOnlyWorkspace(t, []string{"bin/standards-lsp.exe"}, nil)
	if got := resolveFor(t, root, "tools/other-lsp", "windows"); got != "" {
		t.Errorf("explicit path elsewhere resolved %q from bin/standards-lsp.exe", got)
	}
}

// Boundary: with both built names present, or only .com, the command stays extension-less; an
// explicit path that carries its own extension is kept literally.
func TestLSPLaunch_Boundary_BothNamesAndExplicitExtension(t *testing.T) {
	both := lspOnlyWorkspace(t, []string{"bin/standards-lsp", "bin/standards-lsp.exe"}, nil)
	for _, goos := range []string{"windows", runtime.GOOS} {
		if got := resolveFor(t, both, "", goos); got != "bin/standards-lsp" {
			t.Errorf("%s with both names resolved %q, want bin/standards-lsp", goos, got)
		}
	}
	com := lspOnlyWorkspace(t, []string{"bin/standards-lsp.com"}, nil)
	if got := resolveFor(t, com, "", "windows"); got != "bin/standards-lsp" {
		t.Errorf("windows .com resolved %q, want bin/standards-lsp", got)
	}
	literal := lspOnlyWorkspace(t, []string{"tools/lsp.exe"}, nil)
	if got := resolveFor(t, literal, "tools/lsp.exe", "windows"); got != "tools/lsp.exe" {
		t.Errorf("explicit tools/lsp.exe resolved %q", got)
	}
}

// Boundary: launchedFiles follows libuv's search_path order, including its reading of a dot as
// an extension only when a character follows it.
func TestLSPLaunch_Boundary_LaunchOrderFollowsLibuv(t *testing.T) {
	cases := []struct {
		command, goos string
		want          []string
	}{
		{"bin/standards-lsp", "linux", []string{"bin/standards-lsp"}},
		{"bin/standards-lsp", "windows", []string{"bin/standards-lsp.com", "bin/standards-lsp.exe"}},
		{"tools/lsp.exe", "windows", []string{"tools/lsp.exe", "tools/lsp.exe.com", "tools/lsp.exe.exe"}},
		{"tools/lsp.", "windows", []string{"tools/lsp..com", "tools/lsp..exe"}},
		{"my.tools/lsp", "windows", []string{"my.tools/lsp.com", "my.tools/lsp.exe"}},
	}
	for _, tc := range cases {
		if got := launchedFiles(tc.command, tc.goos); !slices.Equal(got, tc.want) {
			t.Errorf("launchedFiles(%q, %s) = [%s], want [%s]", tc.command, tc.goos,
				strings.Join(got, " "), strings.Join(tc.want, " "))
		}
	}
}
