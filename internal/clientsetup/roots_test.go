package clientsetup

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	linuxHome   = "/home/u"
	darwinHome  = "/Users/u"
	windowsHome = `C:\Users\u`
)

func existsSet(paths ...string) func(string) bool {
	return func(path string) bool { return slices.Contains(paths, path) }
}

func envMap(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func hostEnv(goos string) Env {
	switch goos {
	case "windows":
		return Env{GOOS: goos, Home: windowsHome, AppData: windowsHome + `\AppData\Roaming`, LocalAppData: windowsHome + `\AppData\Local`}
	case "darwin":
		return Env{GOOS: goos, Home: darwinHome}
	default:
		return Env{GOOS: goos, Home: linuxHome}
	}
}

func TestResolveDefaultGlobalRootPerOS(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"linux":   "/home/u/.gemini/config",
		"darwin":  "/Users/u/.gemini/config",
		"windows": `C:\Users\u\.gemini\config`,
		"freebsd": "/home/u/.gemini/config",
	}
	for goos, want := range cases {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(AGY, ScopeGlobal, hostEnv(goos))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != (Resolution{Path: want, Source: SourceDefault, Verified: true}) {
				t.Fatalf("got %+v, want default %s", got, want)
			}
			path, err := Root(AGY, ScopeGlobal, hostEnv(goos))
			if err != nil || path != want {
				t.Fatalf("Root = %q, %v; want %q", path, err, want)
			}
		})
	}
}

func TestResolveOverridePrecedence(t *testing.T) {
	t.Parallel()
	variables := map[string]string{"ANTIGRAVITY_CONFIG_DIR": "/opt/agy", "GEMINI_CONFIG_DIR": "/opt/gemini"}
	exists := existsSet("/flag", "/settings", "/opt/agy", "/opt/gemini", "/opt/gemini/config")
	cases := []struct {
		name      string
		overrides []string
		variables map[string]string
		want      Resolution
	}{
		{"flag beats settings and variables", []string{"/flag", "/settings"}, variables,
			Resolution{Path: "/flag", Source: SourceOverride, Verified: true}},
		{"settings used when the flag is empty", []string{"", "/settings"}, variables,
			Resolution{Path: "/settings", Source: SourceOverride, Verified: true}},
		{"first variable beats the second and is unverified", nil, variables,
			Resolution{Path: "/opt/agy", Source: SourceEnvironment, Variable: "ANTIGRAVITY_CONFIG_DIR"}},
		{"second variable appends config and is unverified", []string{"", ""}, map[string]string{"GEMINI_CONFIG_DIR": "/opt/gemini"},
			Resolution{Path: "/opt/gemini/config", Source: SourceEnvironment, Variable: "GEMINI_CONFIG_DIR"}},
		{"no variable set falls to the default", nil, map[string]string{},
			Resolution{Path: "/home/u/.gemini/config", Source: SourceDefault, Verified: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := hostEnv("linux")
			env.Getenv, env.DirExists = envMap(tc.variables), exists
			got, err := Resolve(AGY, ScopeGlobal, env, tc.overrides...)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveWindowsOverrideAndVariable(t *testing.T) {
	t.Parallel()
	env := hostEnv("windows")
	env.DirExists = existsSet(`D:\agy`, `D:\gemini`, `D:\gemini\config`, `\\server\share\agy`)
	env.Getenv = envMap(map[string]string{"GEMINI_CONFIG_DIR": `D:\gemini\`})
	got, err := Resolve(AGY, ScopeGlobal, env)
	if err != nil || got.Path != `D:\gemini\config` || got.Verified {
		t.Fatalf("variable: got %+v, %v", got, err)
	}
	for _, override := range []string{`D:\agy`, `\\server\share\agy`} {
		got, err = Resolve(AGY, ScopeGlobal, env, override)
		if err != nil || got.Path != override || got.Source != SourceOverride {
			t.Fatalf("override %s: got %+v, %v", override, got, err)
		}
	}
}

func TestResolveRejects(t *testing.T) {
	t.Parallel()
	all := func(string) bool { return true }
	none := func(string) bool { return false }
	cases := []struct {
		name     string
		client   Client
		scope    Scope
		env      Env
		override string
		wantIs   error
		wantText string
	}{
		{name: "unknown client", client: Client("unknown"), scope: ScopeGlobal, env: hostEnv("linux"), wantIs: ErrNoRoot},
		{name: "known client without a recorded root", client: Codex, scope: ScopeGlobal, env: hostEnv("linux"), wantIs: ErrNoRoot},
		{name: "unknown scope", client: AGY, scope: Scope("user"), env: hostEnv("linux"), wantText: "unknown scope"},
		{name: "empty scope", client: AGY, env: hostEnv("linux"), wantText: "unknown scope"},
		{name: "workspace scope with override", client: AGY, scope: ScopeWorkspace, env: hostEnv("linux"), override: "/x", wantText: "global scope only"},
		{name: "relative override", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "linux", Home: linuxHome, DirExists: all}, override: "cfg/agy", wantText: "absolute"},
		{name: "posix override on windows", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "windows", Home: windowsHome, DirExists: all}, override: "/opt/agy", wantText: "absolute"},
		{name: "drive-relative override on windows", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "windows", Home: windowsHome, DirExists: all}, override: `C:agy`, wantText: "absolute"},
		{name: "unclean override", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "linux", Home: linuxHome, DirExists: all}, override: "/opt/../etc", wantText: "clean"},
		{name: "override to a missing directory", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "linux", Home: linuxHome, DirExists: none}, override: "/opt/agy", wantText: "does not exist"},
		{name: "override without an existence probe", client: AGY, scope: ScopeGlobal, env: hostEnv("linux"), override: "/opt/agy", wantText: "DirExists"},
		{name: "empty home", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "linux"}, wantText: "home directory is empty"},
		{name: "relative home", client: AGY, scope: ScopeGlobal, env: Env{GOOS: "linux", Home: "home/u"}, wantText: "absolute"},
		{name: "empty operating system", client: AGY, scope: ScopeGlobal, env: Env{Home: linuxHome}, wantText: "operating system is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(tc.client, tc.scope, tc.env, tc.override)
			if err == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("error %v is not %v", err, tc.wantIs)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error %q lacks %q", err, tc.wantText)
			}
			if got != (Resolution{}) {
				t.Fatalf("a failed resolution must be empty, got %+v", got)
			}
		})
	}
}

func TestResolveVariableRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		variables map[string]string
		exists    func(string) bool
		wantText  string
	}{
		{"relative variable", map[string]string{"ANTIGRAVITY_CONFIG_DIR": "agy"}, existsSet(), "absolute"},
		{"variable names a missing directory", map[string]string{"ANTIGRAVITY_CONFIG_DIR": "/opt/agy"}, existsSet(), "does not exist"},
		{"parent exists but parent/config does not", map[string]string{"GEMINI_CONFIG_DIR": "/opt/gemini"}, existsSet("/opt/gemini"), "/opt/gemini/config"},
		{"variable without an existence probe", map[string]string{"ANTIGRAVITY_CONFIG_DIR": "/opt/agy"}, nil, "DirExists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := hostEnv("linux")
			env.Getenv, env.DirExists = envMap(tc.variables), tc.exists
			_, err := Resolve(AGY, ScopeGlobal, env)
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error %v lacks %q", err, tc.wantText)
			}
		})
	}
}

func TestResolveWorkspaceNeedsNoHost(t *testing.T) {
	t.Parallel()
	got, err := Resolve(AGY, ScopeWorkspace, Env{})
	if err != nil || got != (Resolution{Path: ".agents", Source: SourceDefault, Verified: true}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLocatePerOS(t *testing.T) {
	t.Parallel()
	cases := []struct {
		goos     string
		location Location
		want     string
	}{
		{"linux", LocationAGYCLISettings, "/home/u/.gemini/antigravity-cli/settings.json"},
		{"darwin", LocationAGYCLISettings, "/Users/u/.gemini/antigravity-cli/settings.json"},
		{"windows", LocationAGYCLISettings, `C:\Users\u\.gemini\antigravity-cli\settings.json`},
		{"linux", LocationAGYIDESettings, "/home/u/.config/Antigravity/User/settings.json"},
		{"darwin", LocationAGYIDESettings, "/Users/u/Library/Application Support/Antigravity/User/settings.json"},
		{"windows", LocationAGYIDESettings, `C:\Users\u\AppData\Roaming\Antigravity\User\settings.json`},
		{"linux", LocationBinDir, "/home/u/.local/bin"},
		{"darwin", LocationBinDir, "/Users/u/.local/bin"},
		{"windows", LocationBinDir, `C:\Users\u\AppData\Local\Programs\praetor`},
		{"linux", LocationClaudeDesktopConfig, "/home/u/.config/Claude/claude_desktop_config.json"},
		{"darwin", LocationClaudeDesktopConfig, "/Users/u/Library/Application Support/Claude/claude_desktop_config.json"},
		{"windows", LocationClaudeDesktopConfig, `C:\Users\u\AppData\Roaming\Claude\claude_desktop_config.json`},
		{"windows", LocationPowerShellHistory, `C:\Users\u\AppData\Roaming\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt`},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+string(tc.location), func(t *testing.T) {
			t.Parallel()
			got, err := Locate(tc.location, hostEnv(tc.goos))
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLocateBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		env      Env
		location Location
		want     string
	}{
		{"XDG_CONFIG_HOME set", Env{GOOS: "linux", Home: linuxHome, ConfigHome: "/xdg"}, LocationAGYIDESettings, "/xdg/Antigravity/User/settings.json"},
		{"relative XDG_CONFIG_HOME is ignored", Env{GOOS: "linux", Home: linuxHome, ConfigHome: "xdg"}, LocationAGYIDESettings, "/home/u/.config/Antigravity/User/settings.json"},
		{"XDG_CONFIG_HOME does not move macOS", Env{GOOS: "darwin", Home: darwinHome, ConfigHome: "/xdg"}, LocationAGYIDESettings, "/Users/u/Library/Application Support/Antigravity/User/settings.json"},
		{"APPDATA unset derives from home", Env{GOOS: "windows", Home: windowsHome}, LocationAGYIDESettings, `C:\Users\u\AppData\Roaming\Antigravity\User\settings.json`},
		{"APPDATA relocated", Env{GOOS: "windows", Home: windowsHome, AppData: `D:\Roaming`}, LocationClaudeDesktopConfig, `D:\Roaming\Claude\claude_desktop_config.json`},
		{"LOCALAPPDATA unset derives from home", Env{GOOS: "windows", Home: windowsHome}, LocationBinDir, `C:\Users\u\AppData\Local\Programs\praetor`},
		{"LOCALAPPDATA relocated", Env{GOOS: "windows", Home: windowsHome, LocalAppData: `D:\Local`}, LocationBinDir, `D:\Local\Programs\praetor`},
		{"home with a trailing separator", Env{GOOS: "linux", Home: "/home/u/"}, LocationBinDir, "/home/u/.local/bin"},
		{"windows home with forward slashes", Env{GOOS: "windows", Home: "C:/Users/u/"}, LocationAGYCLISettings, `C:/Users/u\.gemini\antigravity-cli\settings.json`},
		{"root home", Env{GOOS: "linux", Home: "/"}, LocationBinDir, "/.local/bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Locate(tc.location, tc.env)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLocateRejects(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "darwin"} {
		if _, err := Locate(LocationPowerShellHistory, hostEnv(goos)); !errors.Is(err, ErrNotApplicable) {
			t.Fatalf("%s: error %v is not ErrNotApplicable", goos, err)
		}
	}
	if _, err := Locate(Location("unknown"), hostEnv("linux")); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("unknown location: error %v is not ErrNoRoot", err)
	}
	if _, err := Locate(LocationBinDir, Env{GOOS: "linux"}); err == nil {
		t.Fatal("empty home must be rejected")
	}
	if _, err := Locate(LocationBinDir, Env{GOOS: "linux", Home: "/home/../root"}); err == nil {
		t.Fatal("unclean home must be rejected")
	}
}

func TestBrainRootsOrderPerOS(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"linux":   {"/home/u/.gemini/antigravity/brain", "/home/u/.gemini/antigravity-ide/brain", "/home/u/.gemini/antigravity-cli/brain"},
		"darwin":  {"/Users/u/.gemini/antigravity/brain", "/Users/u/.gemini/antigravity-ide/brain", "/Users/u/.gemini/antigravity-cli/brain"},
		"windows": {`C:\Users\u\.gemini\antigravity\brain`, `C:\Users\u\.gemini\antigravity-ide\brain`, `C:\Users\u\.gemini\antigravity-cli\brain`},
	}
	for goos, want := range cases {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			got, err := BrainRoots(hostEnv(goos))
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("got %v, %v; want %v", got, err, want)
			}
		})
	}
	if _, err := BrainRoots(Env{GOOS: "linux"}); err == nil {
		t.Fatal("empty home must be rejected")
	}
}

func TestExistingBrainRoots(t *testing.T) {
	t.Parallel()
	all, err := BrainRoots(hostEnv("linux"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		exists []string
		want   []string
	}{
		{"none existing", nil, []string{}},
		{"only the ide spelling", []string{all[1]}, []string{all[1]}},
		{"both former spellings keep the winning order", []string{all[1], all[0]}, []string{all[0], all[1]}},
		{"all three", all, all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := hostEnv("linux")
			env.DirExists = existsSet(tc.exists...)
			got, err := ExistingBrainRoots(env)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := ExistingBrainRoots(hostEnv("linux")); err == nil {
		t.Fatal("a nil DirExists must be rejected")
	}
	if _, err := ExistingBrainRoots(Env{GOOS: "linux", DirExists: existsSet()}); err == nil {
		t.Fatal("empty home must be rejected")
	}
}
