package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The hook runners the audit accepts and how it recognises each one's pre-commit hook (#175).
// Every marker below is the one the runner itself uses to recognise its own hook file, read from
// the runner's source at the version named, so the audit and the runner agree on what is theirs.
const (
	// preCommitConfigFile is the pre-commit framework's configuration (CONFIG_FILE in
	// pre_commit/constants.py, pre-commit 4.6.2).
	preCommitConfigFile = ".pre-commit-config.yaml"
	// lefthookHookFingerprint is how Lefthook 2.1.14 tells a hook file it wrote from any other:
	// a line holding it (hookContentFingerprint and isLefthookFile in internal/command/lefthook.go).
	lefthookHookFingerprint = "LEFTHOOK"
	// lefthookPreCommitDispatch is the last line of Lefthook's hook template for the pre-commit
	// hook (call_lefthook run "{{.HookName}}" "$@" in internal/templates/hook.tmpl, 2.1.14). The
	// audit asks for it too, so a file that only mentions the fingerprint is not lefthook's hook.
	lefthookPreCommitDispatch = `call_lefthook run "pre-commit"`
	// maxInstalledHookBytes bounds the read of an installed hook. The largest runner hook, Lefthook's,
	// is under 4 KiB.
	maxInstalledHookBytes = 64 << 10
	// maxHookLines bounds the line scan of an installed hook (HISS-02).
	maxHookLines = 4096
	// maxPreCommitRepos and maxPreCommitHooks bound the scan of .pre-commit-config.yaml; a larger
	// configuration is refused rather than read in part (HISS-02).
	maxPreCommitRepos = 256
	maxPreCommitHooks = 1024
	// hookSummaryBytes bounds the quoted first line a failure shows of an unrecognised hook.
	hookSummaryBytes = 72
)

// preCommitFrameworkHookIDs are the IDs by which the pre-commit framework recognises a hook file
// it installed: CURRENT_HASH, which resources/hook-tmpl writes as "# ID: <hash>", and the
// PRIOR_HASHES of earlier templates (is_our_script in pre_commit/commands/install_uninstall.py,
// pre-commit 4.6.2).
var preCommitFrameworkHookIDs = [...]string{
	"138fd403232d2ddd5efb44317e38bf03",
	"4d9958c90bc262f47553e2c073f14cfe",
	"d8ee923c46731b42cd95cc869add4062",
	"49fd668cb42069aa1b6048464be5d395",
	"79f09a650522a87b0da915d0d983b2de",
	"e358c9dae00eac5d06b38dfdb1e33a8c",
}

// hookRunner names what wrote an installed pre-commit hook.
type hookRunner string

const (
	runnerUnknown   hookRunner = ""
	runnerLefthook  hookRunner = "lefthook"
	runnerFallback  hookRunner = "the praetor fallback hook"
	runnerPreCommit hookRunner = "the pre-commit framework"
)

// GitHookConfig is the hook configuration AuditGitHookConfig accepted.
type GitHookConfig struct {
	// Line is the gate's verdict line.
	Line string
	// Declined is set when adoption.decline lists git-hooks: the repository owns its hooks, so
	// neither a configuration nor an installed hook is required.
	Declined bool
	// File is the runner configuration verified, lefthook.yml or .pre-commit-config.yaml, and
	// Runner the runner it configures. Both are empty when Declined is set.
	File   string
	Runner string
}

// AuditGitHookConfig is the hook configuration gate the CLI and MCP audits share, for a Git
// checkout. A declined git-hooks step passes with the decline named. Otherwise lefthook.yml
// passes; without it, a .pre-commit-config.yaml that runs praetorctl compile-context --verify and
// praetorctl audit as local pre-commit hooks passes for the pre-commit framework. Which of the two
// the checkout runs is decided by the installed hook (AuditInstalledGitHook), which this gate
// does not read: with both files present lefthook.yml satisfies it.
func AuditGitHookRunnerConfig(ctx context.Context, manifest *config.Manifest, rootDir string) (GitHookConfig, error) {
	verdict, err := AuditDecline(manifest, "git-hooks")
	if err != nil {
		return GitHookConfig{}, fmt.Errorf("[FAIL] Git hook audit failed: %w", err)
	}
	if verdict.Declined {
		return GitHookConfig{Line: verdict.Line("Git hooks (" + lefthookFile + " and its activation)"), Declined: true}, nil
	}
	if fileExists(filepath.Join(rootDir, lefthookFile)) {
		return GitHookConfig{
			Line: "[PASS] Git hook configuration " + lefthookFile + " verified.",
			File: lefthookFile, Runner: string(runnerLefthook),
		}, nil
	}
	if !fileExists(filepath.Join(rootDir, preCommitConfigFile)) {
		return GitHookConfig{}, errors.New("[FAIL] " + lefthookFile + " configuration is missing from repository root, and no " +
			preCommitConfigFile + " names the pre-commit framework as the hook runner")
	}
	if err := auditPreCommitConfig(ctx, rootDir); err != nil {
		return GitHookConfig{}, fmt.Errorf("[FAIL] %s configuration is missing from repository root, and %w", lefthookFile, err)
	}
	return GitHookConfig{
		Line: "[PASS] Git hook configuration " + preCommitConfigFile + " (" + string(runnerPreCommit) + ") verified.",
		File: preCommitConfigFile, Runner: string(runnerPreCommit),
	}, nil
}

// AuditInstalledGitHook verifies the pre-commit hook in the directory git consults for rootDir
// (core.hooksPath and linked worktrees included). The hook must be one a known runner wrote:
// Lefthook's, Praetor's fallback hook, or the pre-commit framework's. It must be runnable as git
// decides it (hookRunnable), and the configuration its runner reads must be present: lefthook.yml
// for Lefthook and the fallback hook, and for the framework a .pre-commit-config.yaml that runs
// both praetor commands. The verdict line names the runner found.
func AuditInstalledGitHook(ctx context.Context, rootDir string) (string, error) {
	hooksDir, err := ResolveGitHooksDir(ctx, rootDir)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Resolve git hooks directory: %w", err)
	}
	hookPath := filepath.Join(hooksDir, preCommitHook)
	info, data, err := readInstalledHook(hookPath)
	if err != nil {
		return "", err
	}
	runner := classifyPreCommitHook(data)
	if runner == runnerUnknown {
		return "", fmt.Errorf("[FAIL] Pre-commit hook %s was not written by a known hook runner (lefthook, the praetor fallback hook "+
			"or the pre-commit framework); found %s. Run 'lefthook install', 'praetorctl adopt' or 'pre-commit install'", hookPath, describeHook(data))
	}
	if err := hookRunnable(info, data, runtime.GOOS); err != nil {
		return "", fmt.Errorf("[FAIL] Pre-commit hook %s, written by %s, %w", hookPath, runner, err)
	}
	configFile, err := runnerConfiguration(ctx, rootDir, runner, data)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Pre-commit hook %s was written by %s, but %w", hookPath, runner, err)
	}
	return fmt.Sprintf("[PASS] Local Git hooks (%s via %s, %s) verified active.", hookPath, runner, configFile), nil
}

// readInstalledHook stats and reads the hook at hookPath, following a link as git does.
func readInstalledHook(hookPath string) (os.FileInfo, []byte, error) {
	info, err := os.Stat(hookPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("[FAIL] Pre-commit hook %s is missing or inactive; run 'lefthook install' or 'praetorctl adopt' "+
			"(or 'pre-commit install' for the pre-commit framework) to activate", hookPath)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("[FAIL] Pre-commit hook %s cannot be inspected: %w", hookPath, err)
	}
	data, err := util.ReadFileLimited(hookPath, maxInstalledHookBytes)
	switch {
	case errors.Is(err, util.ErrNotRegularFile):
		return nil, nil, fmt.Errorf("[FAIL] Pre-commit hook %s is not a regular file, so git does not run it", hookPath)
	case errors.Is(err, util.ErrFileTooLarge):
		return nil, nil, fmt.Errorf("[FAIL] Pre-commit hook %s is larger than %d bytes, which no known hook runner writes", hookPath, maxInstalledHookBytes)
	case err != nil:
		return nil, nil, fmt.Errorf("[FAIL] Pre-commit hook %s cannot be read: %w", hookPath, err)
	}
	return info, data, nil
}

// classifyPreCommitHook names the runner whose marker data carries, or runnerUnknown.
func classifyPreCommitHook(data []byte) hookRunner {
	switch {
	case isPreCommitFrameworkHook(data):
		return runnerPreCommit
	case isFallbackPreCommitHook(data):
		return runnerFallback
	case bytes.Contains(data, []byte(lefthookHookFingerprint)) && bytes.Contains(data, []byte(lefthookPreCommitDispatch)):
		return runnerLefthook
	default:
		return runnerUnknown
	}
}

// isFallbackPreCommitHook reports whether data is a pre-commit hook praetor wrote
// (buildFallbackPreCommitScript).
func isFallbackPreCommitHook(data []byte) bool {
	return bytes.Contains(data, []byte(fallbackPreCommitMarker))
}

// isPreCommitFrameworkHook reports whether data carries an ID the pre-commit framework
// recognises as its own hook's.
func isPreCommitFrameworkHook(data []byte) bool {
	for _, id := range preCommitFrameworkHookIDs {
		if bytes.Contains(data, []byte(id)) {
			return true
		}
	}
	return false
}

// hookRunnable applies git's own test for a runnable hook (is_executable in run-command.c). On
// Unix the owner execute bit must be set; a hook without it is skipped with a hint. Windows has
// no execute bit, so git runs a file that starts with "#!" instead, and that is the rule there.
func hookRunnable(info os.FileInfo, data []byte, goos string) error {
	if goos == "windows" {
		if !bytes.HasPrefix(data, []byte("#!")) {
			return errors.New("does not start with #!, so git for Windows does not run it")
		}
		return nil
	}
	if info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("is not executable (mode %s), so git skips it; run 'chmod u+x' on it or reinstall it", info.Mode().Perm())
	}
	return nil
}

// runnerConfiguration returns the configuration runner reads, after checking that it is present
// and, for the pre-commit framework, that the hook reads .pre-commit-config.yaml and that file
// runs both praetor commands.
func runnerConfiguration(ctx context.Context, rootDir string, runner hookRunner, data []byte) (string, error) {
	if runner != runnerPreCommit {
		if !fileExists(filepath.Join(rootDir, lefthookFile)) {
			return "", errors.New(lefthookFile + " is missing from the repository root")
		}
		return lefthookFile, nil
	}
	if configured := preCommitHookConfig(data); configured != preCommitConfigFile {
		return "", fmt.Errorf("the hook reads %q, not %s; reinstall it with 'pre-commit install'", configured, preCommitConfigFile)
	}
	if !fileExists(filepath.Join(rootDir, preCommitConfigFile)) {
		return "", errors.New(preCommitConfigFile + " is missing from the repository root")
	}
	if err := auditPreCommitConfig(ctx, rootDir); err != nil {
		return "", err
	}
	return preCommitConfigFile, nil
}

// preCommitHookConfig returns the configuration a pre-commit framework hook passes as --config on
// its templated ARGS line (_install_hook_script in pre_commit/commands/install_uninstall.py), as
// a slash path relative to the repository root, or .pre-commit-config.yaml when the hook names
// none, as earlier templates did not.
func preCommitHookConfig(data []byte) string {
	lines := bytes.Split(data, []byte("\n"))
	for i := 0; i < len(lines) && i < maxHookLines; i++ {
		args, ok := strings.CutPrefix(strings.TrimSpace(string(lines[i])), "ARGS=(")
		if !ok {
			continue
		}
		for _, field := range strings.Fields(strings.TrimSuffix(args, ")")) {
			if value, ok := strings.CutPrefix(strings.Trim(field, `'"`), "--config="); ok {
				return path.Clean(strings.ReplaceAll(value, `\`, "/"))
			}
		}
	}
	return preCommitConfigFile
}

// describeHook summarises an unrecognised hook for a failure: its size and its first line.
func describeHook(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Sprintf("a %d-byte file with no content", len(data))
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	first = bytes.TrimRight(first, "\r")
	if len(first) > hookSummaryBytes {
		first = first[:hookSummaryBytes]
	}
	return fmt.Sprintf("a %d-byte file whose first line is %q", len(data), first)
}

// preCommitConfig is the part of .pre-commit-config.yaml the hook audit reads. Other keys are the
// framework's (pre_commit/clientlib.py) and are left to it.
type preCommitConfig struct {
	DefaultStages []string        `yaml:"default_stages"`
	Repos         []preCommitRepo `yaml:"repos"`
}

// preCommitRepo is one entry of repos; only repo: local hooks run a command from the repository.
type preCommitRepo struct {
	Repo  string                `yaml:"repo"`
	Hooks []preCommitConfigHook `yaml:"hooks"`
}

// preCommitConfigHook is one hook of a repo: the framework runs entry, split as a shell would,
// with args appended, at each of its stages.
type preCommitConfigHook struct {
	Entry  string   `yaml:"entry"`
	Args   []string `yaml:"args"`
	Stages []string `yaml:"stages"`
}

// preCommitRequirement is one praetor command the framework's configuration must run.
type preCommitRequirement struct {
	label string
	runs  func(args []string) bool
}

// preCommitRequirements are the commands every praetor pre-commit hook runs
// (buildFallbackPreCommitScript, and the generated lefthook.yml's pre-commit jobs).
var preCommitRequirements = [...]preCommitRequirement{
	{label: util.PraetorCLI + " compile-context --verify", runs: func(args []string) bool {
		return len(args) > 0 && args[0] == "compile-context" && slices.ContainsFunc(args[1:], isVerifyFlag)
	}},
	{label: util.PraetorCLI + " audit", runs: func(args []string) bool {
		return len(args) > 0 && args[0] == "audit"
	}},
}

// auditPreCommitConfig reads .pre-commit-config.yaml below rootDir with the repository's YAML
// reader (config.ReadYAMLDocument: one document, duplicate keys refused, bounded) and fails
// unless its local hooks run every preCommitRequirements command at the pre-commit stage.
func auditPreCommitConfig(ctx context.Context, rootDir string) error {
	var doc preCommitConfig
	if err := config.ReadYAMLDocument(ctx, filepath.Join(rootDir, preCommitConfigFile), &doc, util.YAMLDocumentOptions{}); err != nil {
		return fmt.Errorf("%s cannot be read: %w", preCommitConfigFile, err)
	}
	commands, err := doc.localPreCommitCommands()
	if err != nil {
		return err
	}
	var missing []string
	for _, requirement := range preCommitRequirements {
		if !slices.ContainsFunc(commands, requirement.runs) {
			missing = append(missing, "'"+requirement.label+"'")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s does not run %s as a repo: local hook at the pre-commit stage", preCommitConfigFile, strings.Join(missing, " or "))
	}
	return nil
}

// localPreCommitCommands returns the praetor argument lists of the repo: local hooks that run at
// the pre-commit stage. It refuses a configuration past the scan bounds instead of reading part.
func (c preCommitConfig) localPreCommitCommands() ([][]string, error) {
	if len(c.Repos) > maxPreCommitRepos {
		return nil, fmt.Errorf("%s lists %d repos, more than the %d the audit reads", preCommitConfigFile, len(c.Repos), maxPreCommitRepos)
	}
	var commands [][]string
	hooks := 0
	for _, repo := range c.Repos {
		hooks += len(repo.Hooks)
		if hooks > maxPreCommitHooks {
			return nil, fmt.Errorf("%s lists more than the %d hooks the audit reads", preCommitConfigFile, maxPreCommitHooks)
		}
		if repo.Repo != "local" {
			continue
		}
		for _, hook := range repo.Hooks {
			if args, ok := hook.praetorArgs(); ok && runsAtPreCommit(hook.Stages, c.DefaultStages) {
				commands = append(commands, args)
			}
		}
	}
	return commands, nil
}

// praetorArgs returns the arguments the hook passes to praetor, under either of its names, bare
// or by path, or false when the hook runs something else.
func (h preCommitConfigHook) praetorArgs() ([]string, bool) {
	command := append(strings.Fields(h.Entry), h.Args...)
	if len(command) == 0 || !isPraetorCommand(command[0]) {
		return nil, false
	}
	return command[1:], true
}

// runsAtPreCommit applies the framework's stage rule: a hook's stages default to default_stages,
// which default to every stage, and the legacy name commit means pre-commit (_STAGES in
// pre_commit/clientlib.py).
func runsAtPreCommit(stages, defaultStages []string) bool {
	if len(stages) == 0 {
		stages = defaultStages
	}
	return len(stages) == 0 || slices.Contains(stages, "pre-commit") || slices.Contains(stages, "commit")
}

// isVerifyFlag reports whether arg sets compile-context's --verify flag, in any spelling Go's
// flag package accepts.
func isVerifyFlag(arg string) bool {
	name := strings.TrimLeft(arg, "-")
	return strings.HasPrefix(arg, "-") && (name == "verify" || name == "verify=true")
}
