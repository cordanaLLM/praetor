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
// value with git's path expansion applied.
type hooksPathSetting struct {
	scope, origin, value string
}

// auditHooksPath fails when core.hooksPath is set at any scope git reads for rootDir to a value
// that does not name the managed hooks directory. The failure names every such value, its scope
// and origin, and how to unset it.
func auditHooksPath(ctx context.Context, rootDir string) error {
	settings, err := readHooksPathSettings(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Read %s for %s: %w", hooksPathKey, rootDir, err)
	}
	if len(settings) == 0 {
		return nil
	}
	managed, err := managedHooksDir(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve the managed hooks directory of %s: %w", rootDir, err)
	}
	var refused []string
	for _, setting := range settings {
		if !namesManagedHooksDir(rootDir, setting.value, managed) {
			refused = append(refused, setting.describe())
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("[FAIL] %s is set to %s; git runs hooks from there instead of the managed hooks directory %s, "+
		"so the hooks installed for this repository do not run. Unset it, then reinstall the hooks with "+
		"'lefthook install' or 'praetorctl adopt'", hooksPathKey, strings.Join(refused, "; "), managed)
}

// readHooksPathSettings returns every core.hooksPath value git reads for rootDir, in git's order,
// or none when it is unset. --type=path applies git's own expansion (~/ and %(prefix)/); a value
// git cannot expand fails the read.
func readHooksPathSettings(ctx context.Context, rootDir string) ([]hooksPathSetting, error) {
	result, err := util.RunGitBytes(ctx, rootDir, maxHooksPathConfigBytes,
		"config", "-z", "--show-scope", "--show-origin", "--type=path", "--get-all", hooksPathKey)
	if err != nil {
		if util.GitAnsweredUnset(ctx, err) && len(result.Stdout) == 0 {
			return nil, nil
		}
		return nil, util.CommandDiagnostic(fmt.Errorf("git config --get-all %s: %w", hooksPathKey, err), result.Stderr)
	}
	return parseHooksPathSettings(result.Stdout)
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
func (s hooksPathSetting) describe() string {
	return fmt.Sprintf("%q (%s scope, %s; %s)", s.value, s.scope, s.origin, s.unsetHint())
}

// unsetHint says how to remove the value: from its configuration file with git config, or, for a
// value passed with git -c or the GIT_CONFIG_PARAMETERS or GIT_CONFIG_COUNT variables, from the
// command that set it.
func (s hooksPathSetting) unsetHint() string {
	switch s.scope {
	case "system", "global", "local", "worktree":
		return "git config --unset-all --" + s.scope + " " + hooksPathKey
	default:
		return "drop it from the git -c option or the GIT_CONFIG_PARAMETERS or GIT_CONFIG_COUNT variables that pass it"
	}
}
