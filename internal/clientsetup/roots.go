package clientsetup

import (
	"errors"
	"fmt"
	"strings"
)

// Scope selects which configuration root of a client is resolved.
type Scope string

const (
	ScopeGlobal    Scope = "global"
	ScopeWorkspace Scope = "workspace"
)

// WorkspaceDir is the only workspace customization directory the engine emits.
// It is relative to the repository root, which this package never discovers.
const WorkspaceDir = ".agents"

const osWindows = "windows"

// Env is the host description the command layer supplies. Nothing in this file
// reads the process environment, the filesystem or runtime.GOOS: every input
// arrives here, so all three operating-system tables are testable on any host.
type Env struct {
	GOOS         string
	Home         string
	AppData      string
	LocalAppData string
	ConfigHome   string
	// Getenv reads one variable. Nil means no relocation variable is consulted.
	Getenv func(string) string
	// DirExists reports whether a directory exists. It is required whenever an
	// override or a relocation variable is in play, and for probing brain roots.
	DirExists func(string) bool
}

// RootSource records how a root was chosen.
type RootSource string

const (
	SourceOverride    RootSource = "override"
	SourceEnvironment RootSource = "environment"
	SourceDefault     RootSource = "default"
)

// Resolution is a resolved root together with the evidence class of the rule that
// produced it. Verified is false when the rule is carried as unverified table data:
// such a root may be used only after a native readback agrees with it.
type Resolution struct {
	Path     string     `json:"path"`
	Source   RootSource `json:"source"`
	Variable string     `json:"variable,omitempty"`
	Verified bool       `json:"verified"`
}

// Location names one per-OS file or directory that is not a client root.
type Location string

const (
	LocationAGYCLISettings      Location = "agy-cli-settings"
	LocationAGYIDESettings      Location = "agy-ide-settings"
	LocationBinDir              Location = "bin-dir"
	LocationClaudeDesktopConfig Location = "claude-desktop-config"
	LocationPowerShellHistory   Location = "powershell-history"
)

var (
	// ErrNoRoot reports a client or location this table has no entry for.
	ErrNoRoot = errors.New("no root recorded")
	// ErrNotApplicable reports a location that does not exist on the given OS.
	ErrNotApplicable = errors.New("location does not apply to this operating system")
)

// relocation is one environment variable that may move a global root.
type relocation struct {
	variable string
	suffix   []string
	verified bool
}

// rootBase names the directory a default global root is joined onto.
type rootBase int

const (
	// baseHome joins the segments onto the home directory on every OS.
	baseHome rootBase = iota
	// baseXDGConfig joins onto $XDG_CONFIG_HOME, or <home>/.config, on every OS,
	// macOS and Windows included: the npm xdg-basedir package ignores the platform.
	baseXDGConfig
)

type globalRoot struct {
	base        rootBase
	segments    []string
	relocations []relocation
}

// globalRoots is table data with one row per clientid.Known() client. Neither
// Antigravity variable occurs in the agy 1.2.5 binary, and whether GEMINI_CONFIG_DIR
// names the root or its parent is unknown, so both AGY rows are unverified and a root
// chosen through them resolves with Verified=false. Every other row was read from the
// client's current documentation or source, cited on the row. OPENCODE_CONFIG_DIR and
// KILO_CONFIG_DIR are not relocations: both add a directory after the global root,
// which stays loaded (packages/opencode/src/config/paths.ts in either repository).
var globalRoots = map[Client]globalRoot{
	AGY: {
		segments: []string{".gemini", "config"},
		relocations: []relocation{
			{variable: "ANTIGRAVITY_CONFIG_DIR"},
			{variable: "GEMINI_CONFIG_DIR", suffix: []string{"config"}},
		},
	},
	// https://code.claude.com/docs/en/settings: ~/.claude, %USERPROFILE%\.claude on
	// Windows; CLAUDE_CONFIG_DIR moves settings, session history and plugins.
	Claude: {
		segments:    []string{".claude"},
		relocations: []relocation{{variable: "CLAUDE_CONFIG_DIR", verified: true}},
	},
	// cline/cline sdk/packages/shared/src/storage/paths.ts resolveClineDir:
	// CLINE_DIR, else <home>/.cline; settings live under its data directory.
	Cline: {
		segments:    []string{".cline"},
		relocations: []relocation{{variable: "CLINE_DIR", verified: true}},
	},
	// https://learn.chatgpt.com/docs/config-file/config-advanced: local state lives under
	// CODEX_HOME, which defaults to ~/.codex.
	Codex: {
		segments:    []string{".codex"},
		relocations: []relocation{{variable: "CODEX_HOME", verified: true}},
	},
	// continuedev/continue core/util/paths.ts: CONTINUE_GLOBAL_DIR, else
	// <home>/.continue (%USERPROFILE%\.continue on Windows per the configuration docs).
	Continue: {
		segments:    []string{".continue"},
		relocations: []relocation{{variable: "CONTINUE_GLOBAL_DIR", verified: true}},
	},
	// https://geminicli.com/docs/reference/configuration/: ~/.gemini on every OS;
	// GEMINI_CLI_HOME replaces the home directory the .gemini folder is created in.
	Gemini: {
		segments:    []string{".gemini"},
		relocations: []relocation{{variable: "GEMINI_CLI_HOME", suffix: []string{".gemini"}, verified: true}},
	},
	// Kilo-Org/kilocode packages/core/src/global.ts: path.join(xdgConfig, "kilo").
	Kilo: {base: baseXDGConfig, segments: []string{"kilo"}},
	// anomalyco/opencode packages/core/src/global.ts: path.join(xdgConfig, "opencode").
	OpenCodeV1: {base: baseXDGConfig, segments: []string{"opencode"}},
}

// VerifiedGetenv wraps getenv so a resolver reads only the relocation variables the
// table records as verified; every other name reads as unset. The command layer uses
// it for the running user's environment, so an unverified variable never moves a root
// that is relied on without a native readback. A nil getenv stays nil.
func VerifiedGetenv(getenv func(string) string) func(string) string {
	if getenv == nil {
		return nil
	}
	return func(name string) string {
		if !verifiedVariable(name) {
			return ""
		}
		return getenv(name)
	}
}

func verifiedVariable(name string) bool {
	for _, entry := range globalRoots {
		for _, candidate := range entry.relocations {
			if candidate.variable == name && candidate.verified {
				return true
			}
		}
	}
	return false
}

// brainParents lists, in winning order, the directories under <home>/.gemini that
// hold an Antigravity brain. All three were measured on a Linux host with agy 1.2.5.
var brainParents = []string{"antigravity", "antigravity-ide", "antigravity-cli"}

// Root returns the path of Resolve.
func Root(client Client, scope Scope, env Env, overrides ...string) (string, error) {
	resolution, err := Resolve(client, scope, env, overrides...)
	return resolution.Path, err
}

// Resolve picks a client's configuration root. For the global scope the order is:
// the first non-empty override (the caller passes the flag, then the settings value),
// then the client's relocation variables, then the per-OS default. An override or a
// variable that is relative, unclean or names a missing directory is an error, never
// a fallback. The workspace scope returns WorkspaceDir and accepts no override.
func Resolve(client Client, scope Scope, env Env, overrides ...string) (Resolution, error) {
	entry, ok := globalRoots[client]
	if !ok {
		return Resolution{}, fmt.Errorf("client %q: %w", client, ErrNoRoot)
	}
	override := firstNonEmpty(overrides)
	switch scope {
	case ScopeWorkspace:
		if override != "" {
			return Resolution{}, fmt.Errorf("client %q: a root override applies to the global scope only", client)
		}
		return Resolution{Path: WorkspaceDir, Source: SourceDefault, Verified: true}, nil
	case ScopeGlobal:
		return resolveGlobal(entry, env, override)
	default:
		return Resolution{}, fmt.Errorf("unknown scope %q", scope)
	}
}

func resolveGlobal(entry globalRoot, env Env, override string) (Resolution, error) {
	if err := checkHost(env); err != nil {
		return Resolution{}, err
	}
	if override != "" {
		override = joinFor(env.GOOS, override)
		if err := checkChosenDir(env, "root override", override); err != nil {
			return Resolution{}, err
		}
		return Resolution{Path: override, Source: SourceOverride, Verified: true}, nil
	}
	if relocated, found, err := relocatedRoot(entry, env); err != nil || found {
		return relocated, err
	}
	base := env.Home
	if entry.base == baseXDGConfig {
		base = xdgConfigHome(env)
	}
	return Resolution{Path: joinFor(env.GOOS, base, entry.segments...), Source: SourceDefault, Verified: true}, nil
}

func relocatedRoot(entry globalRoot, env Env) (Resolution, bool, error) {
	if env.Getenv == nil {
		return Resolution{}, false, nil
	}
	for _, candidate := range entry.relocations {
		value := joinFor(env.GOOS, env.Getenv(candidate.variable))
		if value == "" {
			continue
		}
		if err := checkChosenDir(env, candidate.variable, value); err != nil {
			return Resolution{}, false, err
		}
		path := joinFor(env.GOOS, value, candidate.suffix...)
		if !env.DirExists(path) {
			return Resolution{}, false, fmt.Errorf("%s resolves to %s: directory does not exist", candidate.variable, path)
		}
		return Resolution{Path: path, Source: SourceEnvironment, Variable: candidate.variable, Verified: candidate.verified}, true, nil
	}
	return Resolution{}, false, nil
}

// Locate returns one per-OS file or directory.
func Locate(location Location, env Env) (string, error) {
	if err := checkHost(env); err != nil {
		return "", err
	}
	switch location {
	case LocationAGYCLISettings:
		return joinFor(env.GOOS, env.Home, ".gemini", "antigravity-cli", "settings.json"), nil
	case LocationAGYIDESettings:
		return joinFor(env.GOOS, userConfigDir(env), "Antigravity", "User", "settings.json"), nil
	case LocationClaudeDesktopConfig:
		return joinFor(env.GOOS, userConfigDir(env), "Claude", "claude_desktop_config.json"), nil
	case LocationBinDir:
		if env.GOOS == osWindows {
			return joinFor(env.GOOS, localAppData(env), "Programs", "praetor"), nil
		}
		return joinFor(env.GOOS, env.Home, ".local", "bin"), nil
	case LocationPowerShellHistory:
		if env.GOOS != osWindows {
			return "", fmt.Errorf("%s on %s: %w", location, env.GOOS, ErrNotApplicable)
		}
		return joinFor(env.GOOS, userConfigDir(env), "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt"), nil
	default:
		return "", fmt.Errorf("location %q: %w", location, ErrNoRoot)
	}
}

// BrainRoots returns every candidate Antigravity brain directory in winning order.
func BrainRoots(env Env) ([]string, error) {
	if err := checkHost(env); err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(brainParents))
	for _, parent := range brainParents {
		roots = append(roots, joinFor(env.GOOS, env.Home, ".gemini", parent, "brain"))
	}
	return roots, nil
}

// ExistingBrainRoots probes BrainRoots through env.DirExists. The first element wins;
// more than one element means the host holds several brains and the caller reports it.
func ExistingBrainRoots(env Env) ([]string, error) {
	candidates, err := BrainRoots(env)
	if err != nil {
		return nil, err
	}
	if env.DirExists == nil {
		return nil, errors.New("probing brain roots requires Env.DirExists")
	}
	existing := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if env.DirExists(candidate) {
			existing = append(existing, candidate)
		}
	}
	return existing, nil
}

// userConfigDir mirrors os.UserConfigDir over the injected Env. A relative
// XDG_CONFIG_HOME is invalid per the XDG base directory specification and is ignored.
func userConfigDir(env Env) string {
	switch env.GOOS {
	case osWindows:
		if isAbsFor(env.GOOS, env.AppData) {
			return env.AppData
		}
		return joinFor(env.GOOS, env.Home, "AppData", "Roaming")
	case "darwin":
		return joinFor(env.GOOS, env.Home, "Library", "Application Support")
	default:
		return xdgConfigHome(env)
	}
}

// xdgConfigHome is $XDG_CONFIG_HOME, or <home>/.config when it is unset or relative, on
// every OS. It is the base of the XDG-rooted clients and the Linux user config directory.
func xdgConfigHome(env Env) string {
	if isAbsFor(env.GOOS, env.ConfigHome) {
		return env.ConfigHome
	}
	return joinFor(env.GOOS, env.Home, ".config")
}

func localAppData(env Env) string {
	if isAbsFor(env.GOOS, env.LocalAppData) {
		return env.LocalAppData
	}
	return joinFor(env.GOOS, env.Home, "AppData", "Local")
}

func checkHost(env Env) error {
	if env.GOOS == "" {
		return errors.New("operating system is empty")
	}
	if env.Home == "" {
		return errors.New("home directory is empty")
	}
	return checkCleanAbs(env.GOOS, "home directory", env.Home)
}

func checkChosenDir(env Env, label, path string) error {
	if err := checkCleanAbs(env.GOOS, label, path); err != nil {
		return err
	}
	if env.DirExists == nil {
		return fmt.Errorf("%s %s: checking it requires Env.DirExists", label, path)
	}
	if !env.DirExists(path) {
		return fmt.Errorf("%s %s: directory does not exist", label, path)
	}
	return nil
}

func checkCleanAbs(goos, label, path string) error {
	if !isAbsFor(goos, path) {
		return fmt.Errorf("%s %q must be an absolute path", label, path)
	}
	segments := strings.FieldsFunc(path, func(r rune) bool { return isSeparatorFor(goos, r) })
	for _, segment := range segments {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%s %q must be a clean path without %q segments", label, path, segment)
		}
	}
	return nil
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isSeparatorFor(goos string, r rune) bool {
	return r == '/' || (goos == osWindows && r == '\\')
}

// isAbsFor is filepath.IsAbs for a named OS rather than the host: a drive-letter or
// UNC path on Windows, a leading slash elsewhere.
func isAbsFor(goos, path string) bool {
	if goos != osWindows {
		return strings.HasPrefix(path, "/")
	}
	if len(path) >= 3 && isDriveLetter(path[0]) && path[1] == ':' && isSeparatorFor(goos, rune(path[2])) {
		return true
	}
	return len(path) > 2 && isSeparatorFor(goos, rune(path[0])) && isSeparatorFor(goos, rune(path[1]))
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// joinFor joins with the separator of the named OS, so a Windows table renders
// backslashes on a Linux test host. base is already validated as absolute and clean.
func joinFor(goos, base string, segments ...string) string {
	separator, cutset := "/", "/"
	if goos == osWindows {
		separator, cutset = `\`, `/\`
	}
	trimmed := strings.TrimRight(base, cutset)
	if len(segments) == 0 {
		if trimmed == "" || strings.HasSuffix(trimmed, ":") {
			return base
		}
		return trimmed
	}
	return trimmed + separator + strings.Join(segments, separator)
}
