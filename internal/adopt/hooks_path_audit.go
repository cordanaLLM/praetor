package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The core.hooksPath rule of the installed-hook audit (#61).
//
// Git runs every hook from the directory core.hooksPath names, at whichever scope it is set, so a
// value that leaves the repository skips the hooks installed for it: /dev/null skips them all, and
// a directory elsewhere runs whatever it holds, a known runner's hook included, while the audit
// used to pass on it. The audit therefore accepts core.hooksPath only when every value git reads
// for the repository names the managed hooks directory, <git-common-dir>/hooks: git's default,
// where adoption's fallback hook and lefthook install write. No declared runner uses another
// directory. Lefthook 2.1.14 refuses to install while core.hooksPath is set globally, or locally
// to anything but <root>/.git/hooks (ensureHooksPathUnset in internal/command/install.go), and
// the pre-commit framework 4.6.2 refuses while it is set at all (install in
// pre_commit/commands/install_uninstall.py).
const (
	// hooksPathKey is the configuration key the rule reads.
	hooksPathKey = "core.hooksPath"
	// maxHooksPathConfigBytes bounds the configuration read; each value is one path.
	maxHooksPathConfigBytes = 64 << 10
	// maxHooksPathSettings bounds the values the rule reads (HISS-02). Git reads one per
	// configuration file and command-line option, so more than this is refused, not read in part.
	maxHooksPathSettings = 64
)

// hooksPathSetting is one core.hooksPath value git reads for a repository: the scope git reports
// (system, global, local, worktree or command), the origin (the file, or the command line) and the
// value with git's path expansion applied. included marks a value git reads from a file that an
// include.path or includeIf.<condition>.path names, rather than from its scope's own file.
type hooksPathSetting struct {
	scope, origin, value string
	included             bool
}

// auditHooksPath returns the finding when core.hooksPath is set at any scope git reads for rootDir
// to a value that does not name the managed hooks directory, and nil otherwise. The finding names
// every such value, its scope and origin, and how to remove it. It is the one implementation of
// the rule: the installed-hook audit fails with it (AuditInstalledGitHook), and adoption reports it
// instead of installing a hook where the audit refuses it (resolveHooksDirForInstall).
func auditHooksPath(ctx context.Context, rootDir string) error {
	settings, err := readHooksPathSettings(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("read %s for %s: %w", hooksPathKey, rootDir, err)
	}
	if len(settings) == 0 {
		return nil
	}
	managed, err := managedHooksDir(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("resolve the managed hooks directory of %s: %w", rootDir, err)
	}
	var refused []string
	resettable := false
	for _, setting := range settings {
		if !namesManagedHooksDir(rootDir, setting.value, managed) {
			refused = append(refused, setting.describe(rootDir))
			resettable = resettable || setting.lefthookResets()
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("%s is set to %s; git runs hooks from there instead of the managed hooks directory %s, "+
		"so the hooks installed for this repository do not run. Unset it, then reinstall the hooks with "+
		"'lefthook install' or 'praetorctl adopt'%s", hooksPathKey, strings.Join(refused, "; "), managed,
		lefthookResetHint(rootDir, resettable))
}

// readHooksPathSettings returns every core.hooksPath value git reads for rootDir, in git's order,
// or none when it is unset. When a value is set, a second read without includes marks the values
// that only an included file sets (markIncluded).
func readHooksPathSettings(ctx context.Context, rootDir string) ([]hooksPathSetting, error) {
	settings, err := readHooksPathValues(ctx, rootDir, "--includes")
	if err != nil || len(settings) == 0 {
		return settings, err
	}
	direct, err := readHooksPathValues(ctx, rootDir, "--no-includes")
	if err != nil {
		return nil, err
	}
	markIncluded(settings, direct)
	return settings, nil
}

// readHooksPathValues returns the core.hooksPath values git reads for rootDir, with includes
// either --includes or --no-includes. --type=path applies git's own expansion (~/ and
// %(prefix)/); a value git cannot expand fails the read.
func readHooksPathValues(ctx context.Context, rootDir, includes string) ([]hooksPathSetting, error) {
	result, err := util.RunGitBytes(ctx, rootDir, maxHooksPathConfigBytes,
		"config", includes, "-z", "--show-scope", "--show-origin", "--type=path", "--get-all", hooksPathKey)
	if err != nil {
		if util.GitAnsweredUnset(ctx, err) && len(result.Stdout) == 0 {
			return nil, nil
		}
		return nil, util.CommandDiagnostic(fmt.Errorf("git config %s --get-all %s: %w", includes, hooksPathKey, err), result.Stderr)
	}
	return parseHooksPathSettings(result.Stdout)
}

// markIncluded marks every setting that direct, the read without includes, lacks: a value that
// only a file an include.path or includeIf.<condition>.path names sets. Equal settings are matched
// one for one, so a value set both in a scope's own file and in a file it includes is marked once.
func markIncluded(settings, direct []hooksPathSetting) {
	remaining := make(map[hooksPathSetting]int, len(direct))
	for _, setting := range direct {
		remaining[setting]++
	}
	for i := range settings {
		if remaining[settings[i]] > 0 {
			remaining[settings[i]]--
			continue
		}
		settings[i].included = true
	}
}

// parseHooksPathSettings splits git config -z --show-scope --show-origin output: each value is
// three NUL-terminated fields, scope, origin and value. Output that does not end in a complete
// triple is refused rather than read in part.
func parseHooksPathSettings(out []byte) ([]hooksPathSetting, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if !bytes.HasSuffix(out, []byte{0}) {
		return nil, errors.New("git config output is not NUL-terminated")
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	if len(fields)%3 != 0 {
		return nil, fmt.Errorf("git config output holds %d fields, not scope, origin and value triples", len(fields))
	}
	if len(fields)/3 > maxHooksPathSettings {
		return nil, fmt.Errorf("git reads %d %s values, more than the %d the audit reads", len(fields)/3, hooksPathKey, maxHooksPathSettings)
	}
	settings := make([]hooksPathSetting, 0, len(fields)/3)
	for i := 0; i+2 < len(fields); i += 3 {
		settings = append(settings, hooksPathSetting{scope: string(fields[i]), origin: string(fields[i+1]), value: string(fields[i+2])})
	}
	return settings, nil
}

// managedHooksDir returns <git-common-dir>/hooks for rootDir: the directory git uses for hooks when
// core.hooksPath is unset, shared by every linked worktree of the repository.
func managedHooksDir(ctx context.Context, rootDir string) (string, error) {
	common, err := util.RunGit(ctx, rootDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	return filepath.Join(filepath.Clean(strings.TrimSpace(common)), "hooks"), nil
}

// namesManagedHooksDir reports whether value, resolved as git resolves core.hooksPath, is the
// managed hooks directory. A relative value is relative to the working tree root, where git runs
// hooks in a non-bare repository (githooks(5)). The spellings are compared first, so a managed
// directory not yet created still matches its own name, then by filesystem identity
// (util.SameDirectory), so a symlinked or differently cased spelling of it matches too. An empty
// value names no directory.
func namesManagedHooksDir(rootDir, value, managed string) bool {
	if value == "" {
		return false
	}
	resolved := value
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(rootDir, resolved)
	}
	resolved = filepath.Clean(resolved)
	return resolved == managed || util.SameDirectory(resolved, managed)
}

// describe names the value, its scope and its origin, and how to unset it.
func (s hooksPathSetting) describe(rootDir string) string {
	return fmt.Sprintf("%q (%s scope, %s; %s)", s.value, s.scope, s.origin, s.unsetHint(rootDir))
}

// unsetHint says how to remove the value: from the included file that sets it, from its scope's
// configuration file with git config, or, for a value passed with git -c or the
// GIT_CONFIG_PARAMETERS or GIT_CONFIG_COUNT variables, from the command that set it. git config
// --unset-all --<scope> edits the scope's own file only, so it cannot remove an included value.
func (s hooksPathSetting) unsetHint(rootDir string) string {
	if file, ok := s.includedFile(rootDir); ok {
		return fmt.Sprintf(`set in "%s", a file the %s configuration includes through include.path or includeIf; `+
			`edit that file, or run git config --file "%s" --unset-all %s`, file, s.scope, file, hooksPathKey)
	}
	switch s.scope {
	case "system", "global", "local", "worktree":
		return "git config --unset-all --" + s.scope + " " + hooksPathKey
	default:
		return "drop it from the git -c option or the GIT_CONFIG_PARAMETERS or GIT_CONFIG_COUNT variables that pass it"
	}
}

// includedFile returns the file that sets an included value, absolute: git reports a relative
// origin from the working tree root it runs in, rootDir.
func (s hooksPathSetting) includedFile(rootDir string) (string, bool) {
	file, isFile := strings.CutPrefix(s.origin, "file:")
	if !s.included || !isFile || file == "" {
		return "", false
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(rootDir, file)
	}
	return filepath.Clean(file), true
}

// lefthookResets reports whether lefthook install --reset-hooks-path removes the value: lefthook
// 2.1.14 unsets core.hooksPath with git config --local --unset-all and --global --unset-all
// (unsetHooksPathConfig in internal/command/install.go), which edit those scopes' own files only.
func (s hooksPathSetting) lefthookResets() bool {
	return !s.included && (s.scope == "local" || s.scope == "global")
}

// lefthookResetHint names lefthook install --reset-hooks-path where lefthook runs the hooks, the
// runner the configuration gate accepts lefthook.yml for (AuditGitHookConfig), and a refused value
// is one it removes (lefthookResets).
func lefthookResetHint(rootDir string, resettable bool) string {
	if !resettable || !fileExists(filepath.Join(rootDir, lefthookFile)) {
		return ""
	}
	return ". With lefthook as the runner, 'lefthook install --reset-hooks-path' unsets a local or global value " +
		"set in its scope's own file and reinstalls the hooks in one step"
}
