// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clientsetup

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

// Every client the operator settings accept must resolve a global root: workstation status
// reports one row per clientid.Known() client, and a missing row reads as "no root recorded".
func TestGlobalRootsCoverSharedClientList(t *testing.T) {
	t.Parallel()
	rows := make([]clientid.ID, 0, len(globalRoots))
	for client := range globalRoots {
		rows = append(rows, client)
	}
	slices.Sort(rows)
	if known := clientid.Known(); !slices.Equal(rows, known) {
		t.Fatalf("root rows %v differ from known clients %v", rows, known)
	}
}

// Positive: the documented default of every known client on every OS.
func TestResolveEveryKnownClientPerOS(t *testing.T) {
	t.Parallel()
	want := map[Client][3]string{ // linux, darwin, windows
		AGY:        {"/home/u/.gemini/config", "/Users/u/.gemini/config", `C:\Users\u\.gemini\config`},
		Claude:     {"/home/u/.claude", "/Users/u/.claude", `C:\Users\u\.claude`},
		Cline:      {"/home/u/.cline", "/Users/u/.cline", `C:\Users\u\.cline`},
		Codex:      {"/home/u/.codex", "/Users/u/.codex", `C:\Users\u\.codex`},
		Continue:   {"/home/u/.continue", "/Users/u/.continue", `C:\Users\u\.continue`},
		Gemini:     {"/home/u/.gemini", "/Users/u/.gemini", `C:\Users\u\.gemini`},
		Kilo:       {"/home/u/.config/kilo", "/Users/u/.config/kilo", `C:\Users\u\.config\kilo`},
		OpenCodeV1: {"/home/u/.config/opencode", "/Users/u/.config/opencode", `C:\Users\u\.config\opencode`},
	}
	if len(want) != len(clientid.Known()) {
		t.Fatalf("expectation rows %d, known clients %d", len(want), len(clientid.Known()))
	}
	for client, paths := range want {
		for i, goos := range []string{"linux", "darwin", "windows"} {
			got, err := Resolve(client, ScopeGlobal, hostEnv(goos))
			if err != nil {
				t.Fatalf("%s/%s: %v", client, goos, err)
			}
			if got != (Resolution{Path: paths[i], Source: SourceDefault, Verified: true}) {
				t.Fatalf("%s/%s: got %+v, want default %s", client, goos, got, paths[i])
			}
		}
	}
}

// Positive: each verified relocation variable moves its client's root and stays verified.
func TestResolveVerifiedRelocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		client   Client
		variable string
		value    string
		want     string
	}{
		{Claude, "CLAUDE_CONFIG_DIR", "/opt/claude", "/opt/claude"},
		{Cline, "CLINE_DIR", "/opt/cline", "/opt/cline"},
		{Codex, "CODEX_HOME", "/opt/codex", "/opt/codex"},
		{Continue, "CONTINUE_GLOBAL_DIR", "/opt/continue", "/opt/continue"},
		{Gemini, "GEMINI_CLI_HOME", "/opt/gemini-home", "/opt/gemini-home/.gemini"},
	}
	for _, tc := range cases {
		t.Run(string(tc.client), func(t *testing.T) {
			t.Parallel()
			env := hostEnv("linux")
			env.Getenv = envMap(map[string]string{tc.variable: tc.value})
			env.DirExists = existsSet(tc.value, tc.want)
			got, err := Resolve(tc.client, ScopeGlobal, env)
			want := Resolution{Path: tc.want, Source: SourceEnvironment, Variable: tc.variable, Verified: true}
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// Negative: a relocation naming a missing directory, or a GEMINI_CLI_HOME without the
// .gemini folder inside it, is an error rather than a fallback to the default.
func TestResolveClientRelocationRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		client   Client
		vars     map[string]string
		exists   func(string) bool
		wantText string
	}{
		{"missing CODEX_HOME", Codex, map[string]string{"CODEX_HOME": "/opt/codex"}, existsSet(), "does not exist"},
		{"relative CLAUDE_CONFIG_DIR", Claude, map[string]string{"CLAUDE_CONFIG_DIR": "claude"}, existsSet(), "absolute"},
		{"GEMINI_CLI_HOME without .gemini", Gemini, map[string]string{"GEMINI_CLI_HOME": "/opt/g"}, existsSet("/opt/g"), "/opt/g/.gemini"},
		{"unclean CONTINUE_GLOBAL_DIR", Continue, map[string]string{"CONTINUE_GLOBAL_DIR": "/opt/../c"}, existsSet(), "clean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := hostEnv("linux")
			env.Getenv, env.DirExists = envMap(tc.vars), tc.exists
			got, err := Resolve(tc.client, ScopeGlobal, env)
			if err == nil || !strings.Contains(err.Error(), tc.wantText) || got != (Resolution{}) {
				t.Fatalf("got %+v, %v; want an error containing %q", got, err, tc.wantText)
			}
		})
	}
	for _, client := range clientid.Known() {
		if _, err := Resolve(client, ScopeGlobal, Env{GOOS: "linux"}); err == nil {
			t.Fatalf("%s: an empty home must be rejected", client)
		}
	}
}

// Boundary: XDG_CONFIG_HOME moves the XDG-rooted clients on every OS, since xdg-basedir
// ignores the platform, but never the home-rooted ones; a relative value is ignored.
func TestResolveXDGRootedClients(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		env    Env
		client Client
		want   string
	}{
		{"linux XDG_CONFIG_HOME", Env{GOOS: "linux", Home: linuxHome, ConfigHome: "/xdg"}, OpenCodeV1, "/xdg/opencode"},
		{"macOS XDG_CONFIG_HOME", Env{GOOS: "darwin", Home: darwinHome, ConfigHome: "/xdg"}, Kilo, "/xdg/kilo"},
		{"windows XDG_CONFIG_HOME", Env{GOOS: "windows", Home: windowsHome, ConfigHome: `D:\xdg`}, OpenCodeV1, `D:\xdg\opencode`},
		{"windows APPDATA does not move it", Env{GOOS: "windows", Home: windowsHome, AppData: `D:\Roaming`}, Kilo, `C:\Users\u\.config\kilo`},
		{"relative XDG_CONFIG_HOME is ignored", Env{GOOS: "linux", Home: linuxHome, ConfigHome: "xdg"}, Kilo, "/home/u/.config/kilo"},
		{"home-rooted client ignores XDG_CONFIG_HOME", Env{GOOS: "linux", Home: linuxHome, ConfigHome: "/xdg"}, Claude, "/home/u/.claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Root(tc.client, ScopeGlobal, tc.env)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestVerifiedGetenv(t *testing.T) {
	t.Parallel()
	if VerifiedGetenv(nil) != nil {
		t.Fatal("a nil getenv must stay nil")
	}
	source := envMap(map[string]string{
		"CODEX_HOME": "/opt/codex", "GEMINI_CLI_HOME": "/opt/g",
		"ANTIGRAVITY_CONFIG_DIR": "/opt/agy", "GEMINI_CONFIG_DIR": "/opt/gc", "PATH": "/usr/bin",
	})
	getenv := VerifiedGetenv(source)
	for name, want := range map[string]string{
		"CODEX_HOME": "/opt/codex", "GEMINI_CLI_HOME": "/opt/g",
		"ANTIGRAVITY_CONFIG_DIR": "", "GEMINI_CONFIG_DIR": "", "PATH": "", "": "",
	} {
		if got := getenv(name); got != want {
			t.Fatalf("%q: got %q, want %q", name, got, want)
		}
	}
	// The filtered reader keeps AGY on its default even when its variables are set.
	env := hostEnv("linux")
	env.Getenv, env.DirExists = getenv, existsSet("/opt/agy", "/opt/gc", "/opt/gc/config")
	got, err := Resolve(AGY, ScopeGlobal, env)
	if err != nil || got != (Resolution{Path: "/home/u/.gemini/config", Source: SourceDefault, Verified: true}) {
		t.Fatalf("AGY through the verified reader: got %+v, %v", got, err)
	}
	if _, err := Resolve(Client("x"), ScopeGlobal, env); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("an unknown client must still report ErrNoRoot, got %v", err)
	}
}
