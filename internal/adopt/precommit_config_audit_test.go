package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuditGitHookConfig_Positive_PreCommitFramework (#175): without lefthook.yml, a framework
// configuration whose local hooks run both praetor commands passes the shared configuration gate,
// which names the file and the runner.
func TestAuditGitHookConfig_Positive_PreCommitFramework(t *testing.T) {
	root, _ := hookRunnerRepo(t, false, preCommitBothCommands)
	hooks, err := AuditGitHookConfig(t.Context(), nil, root)
	if err != nil || hooks.Declined || hooks.File != preCommitConfigFile || hooks.Runner != "the pre-commit framework" ||
		hooks.Line != "[PASS] Git hook configuration .pre-commit-config.yaml (the pre-commit framework) verified." {
		t.Fatalf("framework configuration: %+v, %v", hooks, err)
	}
}

// TestAuditGitHookConfig_Negative_PreCommitFrameworkIncomplete (#175): without lefthook.yml, a
// framework configuration that omits a command, or runs it only where no commit reaches it, fails
// and names what is missing; a file the strict reader refuses fails too.
func TestAuditGitHookConfig_Negative_PreCommitFrameworkIncomplete(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"audit omitted", preCommitContextHook, "does not run 'praetorctl audit' as a repo: local hook"},
		{"verify flag omitted", strings.Replace(preCommitBothCommands, "compile-context --verify", "compile-context", 1),
			"does not run 'praetorctl compile-context --verify'"},
		{"audit at pre-push only", strings.Replace(preCommitBothCommands, "args: [audit, --offline]", "args: [audit]\n        stages: [pre-push]", 1),
			"does not run 'praetorctl audit'"},
		{"default stages exclude pre-commit", "default_stages: [manual]\n" + preCommitBothCommands,
			"does not run 'praetorctl compile-context --verify' or 'praetorctl audit'"},
		{"remote repository", strings.Replace(preCommitBothCommands, "repo: local", "repo: https://example.invalid/hooks", 1),
			"does not run 'praetorctl compile-context --verify' or 'praetorctl audit'"},
		{"duplicate key", "repos: []\nrepos: []\n", "cannot be read"},
		{"second document", preCommitBothCommands + "---\nrepos: []\n", "cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := hookRunnerRepo(t, false, tc.config)
			_, err := AuditGitHookConfig(t.Context(), nil, root)
			if err == nil || !strings.Contains(err.Error(), "lefthook.yml configuration is missing") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v; want %q", err, tc.want)
			}
		})
	}
	root, _ := hookRunnerRepo(t, false, "")
	if _, err := AuditGitHookConfig(t.Context(), nil, root); err == nil || !strings.Contains(err.Error(), "no .pre-commit-config.yaml") {
		t.Fatalf("neither configuration: %v", err)
	}
}

// TestPreCommitConfig_Boundary_CommandsAndStages: the legacy stage name commit, a hook-level
// stages list overriding default_stages, the legacy binary name by path and the -verify=true
// spelling all count; a configuration past the scan bound is refused, not read in part.
func TestPreCommitConfig_Boundary_CommandsAndStages(t *testing.T) {
	config := "default_stages: [pre-push]\nrepos:\n  - repo: local\n    hooks:\n" +
		"      - {id: c, name: c, entry: ./bin/standardsctl.exe compile-context -verify=true, language: system, stages: [commit]}\n" +
		"      - {id: a, name: a, entry: praetorctl audit, language: system, stages: [pre-commit]}\n"
	root, _ := hookRunnerRepo(t, false, config)
	if err := auditPreCommitConfig(t.Context(), root); err != nil {
		t.Fatalf("legacy spellings: %v", err)
	}
	many := preCommitConfig{Repos: make([]preCommitRepo, maxPreCommitRepos+1)}
	if _, err := many.localPreCommitCommands(); err == nil || !strings.Contains(err.Error(), "more than the 256") {
		t.Fatalf("repos past the bound: %v", err)
	}
	atBound := preCommitConfig{Repos: []preCommitRepo{{Repo: "local", Hooks: make([]preCommitConfigHook, maxPreCommitHooks)}}}
	if _, err := atBound.localPreCommitCommands(); err != nil {
		t.Fatalf("hooks at the bound: %v", err)
	}
	atBound.Repos = append(atBound.Repos, preCommitRepo{Repo: "local", Hooks: make([]preCommitConfigHook, 1)})
	if _, err := atBound.localPreCommitCommands(); err == nil {
		t.Fatal("hooks past the bound were read")
	}
}

// TestAuditInstalledGitHook_Positive_RunnersInstalledHooks replays the markers against the hooks
// the runners on PATH write: `lefthook install` and `pre-commit install` in a scratch checkout.
// Each half skips, naming the tool, where the runner is not installed; CI installs lefthook.
func TestAuditInstalledGitHook_Positive_RunnersInstalledHooks(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args []string
		want hookRunner
	}{
		{"lefthook", []string{"install"}, runnerLefthook},
		{"pre-commit", []string{"install"}, runnerPreCommit},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			toolPath, err := exec.LookPath(tc.tool)
			if err != nil {
				t.Skipf("%s is not on PATH; the excerpt tests cover its marker", tc.tool)
			}
			root, hooks := hookRunnerRepo(t, true, preCommitBothCommands)
			runInstaller(t, root, toolPath, tc.args)
			data, err := os.ReadFile(filepath.Join(hooks, preCommitHook))
			if err != nil {
				t.Fatalf("%s wrote no pre-commit hook: %v", tc.tool, err)
			}
			if got := classifyPreCommitHook(data); got != tc.want {
				t.Fatalf("%s hook classified as %q, want %q", tc.tool, got, tc.want)
			}
			if line, err := AuditInstalledGitHook(t.Context(), root); err != nil || !strings.Contains(line, "via "+string(tc.want)) {
				t.Fatalf("%s hook: %q, %v", tc.tool, line, err)
			}
		})
	}
}

// runInstaller runs a runner's install command in root with a scratch home, bounded in time.
func runInstaller(t *testing.T, root, toolPath string, args []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, toolPath, args...)
	cmd.Dir = root
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PRE_COMMIT_HOME="+filepath.Join(home, "pre-commit"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v (%s)", filepath.Base(toolPath), args, err, out)
	}
}
