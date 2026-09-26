package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	lefthookFile    = "lefthook.yml"
	evasionHookFile = ".config/agent/hooks/block_evasion.py"
	preCommitHook   = "pre-commit"
	hookBackupExt   = ".bak"
	// fallbackPreCommitMarker identifies a pre-commit hook written by praetor.
	fallbackPreCommitMarker = "# praetor-managed pre-commit hook"
)

// ErrHooksDirEscapesRepo is returned when git discovers a different top-level directory
// than the adoption target, which happens when the target's .git is not a repository
// and discovery walks up into an enclosing checkout.
var ErrHooksDirEscapesRepo = errors.New("adopt: git hooks directory belongs to a different repository")

// ResolveGitHooksDir returns the directory git consults for hooks of repoPath. It
// honours core.hooksPath, linked worktrees and submodule gitlinks by asking git itself,
// and refuses to answer when git resolves repoPath to another repository's top level.
func ResolveGitHooksDir(ctx context.Context, repoPath string) (string, error) {
	top, err := util.RunGit(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel in %s: %w", repoPath, err)
	}
	if !util.SameDirectory(top, repoPath) {
		return "", fmt.Errorf("%w: %s resolves to top-level %s", ErrHooksDirEscapesRepo, repoPath, top)
	}
	out, err := util.RunGit(ctx, repoPath, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-path hooks in %s: %w", repoPath, err)
	}
	hooksDir := strings.TrimSpace(out)
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(repoPath, hooksDir)
	}
	return filepath.Clean(hooksDir), nil
}

// lefthookGovernedCommand renders a lefthook run line that executes a praetor
// subcommand and fails closed: a failing command blocks, and so does a missing binary.
// It resolves either installed name through util.ShellCLI, the same resolution generated
// Makefiles use, so a repository whose make verify-all passes cannot fail every hook
// because only the legacy name is on PATH (BUG-805).
func lefthookGovernedCommand(args string) string {
	return util.ShellCLI(args, "HISS governance hook cannot run because neither "+
		util.PraetorCLI+" nor "+util.LegacyCLI+" is installed")
}

// optionalToolCommand renders a lefthook run line for a third-party tool that is skipped
// when absent but blocks when it fails.
func optionalToolCommand(tool, args string) string {
	return "if command -v " + tool + " >/dev/null 2>&1; then " + tool + " " + args +
		"; else echo " + tool + " is not installed, skipping >&2; fi"
}

// goModuleCommand renders a lefthook run line for a Go module tool. It runs command only
// where the repository root holds a go.mod and otherwise skips with the reason, as the
// gate's own security and test stages already do (internal/gating/pipeline.go). Without the
// guard a repository with no root module failed every push on govulncheck and every commit
// touching a .go file on go vet (#242). The reason avoids ": " so the line stays one plain
// YAML scalar.
func goModuleCommand(skipped, command string) string {
	return "if [ -f go.mod ]; then " + command +
		"; else echo no go.mod at the repository root, skipping " + skipped + " >&2; fi"
}

// buildLefthookYAML renders the scaffolded lefthook configuration.
func buildLefthookYAML() string {
	return buildLefthookYAMLFor(false)
}

func buildLefthookYAMLFor(checkpoint bool) string {
	governed := lefthookGovernedCommand
	checkpointJobs := ""
	if checkpoint {
		checkpointJobs = "agent-checkpoint-tool:\n  commands:\n    checkpoint:\n      run: python3 -B .config/lefthook/scripts/checkpoint.py --event tool --json --marker\nagent-checkpoint-stop:\n  commands:\n    checkpoint:\n      run: python3 -B .config/lefthook/scripts/checkpoint.py --event stop --json --marker\n"
	}
	return "# Lefthook Configuration (Go 1.27+ & HISS Governance)\n" +
		"# Governance commands fail closed: a failing or missing praetorctl blocks the commit or push.\n" +
		checkpointJobs + "pre-commit:\n" +
		"  parallel: true\n" +
		"  commands:\n" +
		"    gofmt:\n      glob: \"*.go\"\n      run: gofmt -w {staged_files}\n      stage_fixed: true\n" +
		"    govet:\n      glob: \"*.go\"\n      run: " + goModuleCommand("go vet", "go vet ./...") + "\n" +
		"    context-check:\n      run: " + governed("compile-context --verify") + "\n" +
		"    hiss-audit:\n      run: " + governed("audit") + "\n" +
		"\n" +
		"post-commit:\n" +
		"  commands:\n" +
		"    state-sync:\n      run: " + governed("state sync .") + "\n" +
		"    dedupe-cadence:\n      run: " + governed("dedupe cadence --threshold=20 --record .") + "\n" +
		"\n" +
		"pre-push:\n" +
		"  parallel: false\n" +
		"  commands:\n" +
		"    security:\n      run: " + goModuleCommand("govulncheck", optionalToolCommand("govulncheck", "./...")) + "\n" +
		"    flavor-audit:\n      run: " + governed("flavor audit .") + "\n" +
		"    audit:\n      run: " + governed("audit") + "\n" +
		"    gate:\n      run: " + governed("gate run --path=.") + "\n"
}

// blockEvasionPY is the agent PreToolUse interceptor. It is not a git hook: git skips
// hooks entirely on --no-verify, so only the agent harness can observe the command.
const blockEvasionPY = `#!/usr/bin/env python3
"""Agent PreToolUse evasion interceptor (HISS).

Wire this script as a PreToolUse hook of the agent harness. The harness passes the
pending tool call as JSON on stdin (the shell command lives at tool_input.command) or
as argv. Exit code 2 blocks the call and returns the reason to the agent.

A git hook cannot observe --no-verify because git skips hooks entirely, so this script
is deliberately not part of lefthook.yml.
"""
import json
import os
import re
import sys

BLOCKED_PATTERNS = [
    r"--no-verify\b",
    r"\bgit\s+commit\b.*\s-n\b",
    r"LEFTHOOK=0\b",
    r"SKIP=.*git",
    r"core\.hooksPath\s*=\s*/dev/null",
    r"rm\s+(-rf?\s+)?\.git/hooks",
]

BLOCK_EXIT = 2


def pending_command():
    """Return the command under review from argv or the JSON hook payload."""
    if len(sys.argv) > 1:
        return " ".join(sys.argv[1:])
    if sys.stdin.isatty():
        return ""
    raw = sys.stdin.read()
    if not raw.strip():
        return ""
    try:
        payload = json.loads(raw)
    except ValueError:
        return raw
    tool_input = payload.get("tool_input") or {}
    return str(tool_input.get("command", ""))


def main():
    if os.environ.get("LEFTHOOK") == "0":
        sys.stderr.write("[BLOCKED BY HISS] LEFTHOOK=0 detected in environment.\n")
        sys.exit(BLOCK_EXIT)
    cmd = pending_command()
    for pattern in BLOCKED_PATTERNS:
        if re.search(pattern, cmd):
            sys.stderr.write(f"[BLOCKED BY HISS] Verification evasion prohibited: {pattern}\n")
            sys.exit(BLOCK_EXIT)
    sys.exit(0)


if __name__ == "__main__":
    main()
`

// buildFallbackPreCommitScript renders the hook installed when lefthook is unavailable.
// It only runs binaries found on PATH and fails closed when none is installed.
func buildFallbackPreCommitScript() string {
	missing := "'[HISS] neither " + util.PraetorCLI + " nor " + util.LegacyCLI +
		" is installed; refusing to commit unverified changes'"
	return "#!/usr/bin/env bash\n" +
		fallbackPreCommitMarker + " (installed because lefthook is not available)\n" +
		"set -euo pipefail\n" +
		util.ShellCLI("compile-context --verify", missing) + "\n" +
		util.ShellCLI("audit", missing) + "\n"
}

// reconcileGitHooks scaffolds lefthook.yml and the agent evasion interceptor, then
// activates local git hooks for configurations praetor itself wrote. An earlier Praetor
// rendering is migrated to the current one; a configuration that extends the canonical
// policy or adds jobs to the generated ones is never replaced, --force included, and neither
// are the checkpoint scripts vendored beside such a policy.
func reconcileGitHooks(ctx context.Context, s *adoptSession) error {
	existing, err := s.readExistingLefthook()
	if err != nil {
		return err
	}
	checkpointReady, err := reconcileCheckpointLifecycle(ctx, s, lefthookExtendsCanonical(existing))
	if err != nil {
		return err
	}
	current := buildLefthookYAMLFor(checkpointReady)
	identity := classifyLefthookConfig(existing, current)
	if identity.reason != "" {
		s.report.recordSkipped(lefthookFile, identity.reason)
		return reconcileEvasionHook(s, identity.canonical)
	}
	lefthookWritten, err := s.writeLefthookConfig(current, identity.prior)
	if err != nil {
		return err
	}
	warnPreservedCheckpoint(s, checkpointReady, lefthookWritten)
	if err := reconcileEvasionHook(s, false); err != nil {
		return err
	}
	if s.opts.DryRun || s.opts.SkipHookActivation {
		return nil
	}
	return s.activateGitHooks(ctx, lefthookWritten)
}

// writeLefthookConfig writes the current rendering over an earlier Praetor rendering, and
// otherwise scaffolds it under the usual --force contract. It reports whether it wrote.
func (s *adoptSession) writeLefthookConfig(current string, prior bool) (bool, error) {
	if !prior {
		return s.scaffoldFile(scaffold{
			rel:      lefthookFile,
			perm:     filePerm,
			content:  []byte(current),
			force:    true,
			created:  "Scaffolded Lefthook configuration for local pre-commit and pre-push enforcement",
			verified: "Existing Lefthook configuration verified present",
		})
	}
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil {
		return false, err
	}
	if err := s.write(full, []byte(current), filePerm); err != nil {
		return false, err
	}
	s.report.recordReconciled(lefthookFile, "Migrated an earlier Praetor-generated Lefthook configuration to the current template")
	return true, nil
}

// reconcileCheckpointLifecycle installs the checkpoint bundle. With vendored set, the
// repository carries the canonical hook policy, whose checkpoint scripts belong to that
// vendored bundle (reconcileCheckpointBundle).
func reconcileCheckpointLifecycle(ctx context.Context, s *adoptSession, vendored bool) (bool, error) {
	ready, err := reconcileCheckpointBundle(ctx, s, vendored)
	if err == nil {
		return ready, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	s.report.addWarning("checkpoint lifecycle unavailable: %v", err)
	return false, nil
}

func warnPreservedCheckpoint(s *adoptSession, ready, written bool) {
	if !ready || written {
		return
	}
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil {
		return
	}
	data, err := readRepoFile(full)
	if err == nil && !bytes.Contains(data, []byte("agent-checkpoint-tool:")) {
		s.report.addWarning("existing lefthook.yml was preserved without checkpoint lifecycle jobs")
	}
}

// reconcileEvasionHook scaffolds the interceptor. With vendored set, the repository carries
// the canonical hook policy, whose interceptor belongs to that vendored bundle, so --force
// does not replace it with the generated one.
func reconcileEvasionHook(s *adoptSession, vendored bool) error {
	_, err := s.scaffoldFile(scaffold{
		rel:      evasionHookFile,
		perm:     execPerm,
		content:  []byte(blockEvasionPY),
		force:    !vendored,
		created:  "Scaffolded agent PreToolUse anti-evasion interceptor (wire it into the agent harness hooks)",
		verified: "Existing agent anti-evasion interceptor verified present",
	})
	return err
}

// activateGitHooks installs hooks through lefthook, or the fallback hook when lefthook
// is unavailable. It never activates a lefthook.yml that praetor did not write: hook
// commands are shell executed at the adopter's next commit.
func (s *adoptSession) activateGitHooks(ctx context.Context, lefthookWritten bool) error {
	if !lefthookWritten && !s.lefthookConfigIsPraetor() {
		s.report.recordSkipped(lefthookFile, "existing lefthook.yml was not generated by praetor; hooks were not activated. Review its run: commands and run 'lefthook install' yourself, or re-run adopt with --force to replace it with the praetor configuration")
		return nil
	}
	hooksDir, err := s.resolveHooksDirForInstall(ctx)
	if err != nil {
		s.report.addError("git hooks: %v", err)
		return nil
	}
	hookPath := filepath.Join(hooksDir, preCommitHook)
	if _, err := util.RunCommand(ctx, s.repoPath, "lefthook", "install"); err == nil {
		if !fileExists(hookPath) {
			s.report.addError("git hooks: lefthook install completed but %s was not created", hookPath)
			return nil
		}
		s.report.recordCreated(displayHookPath(s.repoPath, hookPath), "Activated local Git hooks via lefthook install")
		return nil
	}
	return s.installFallbackHook(hookPath)
}

// lefthookConfigIsPraetor reports whether the existing lefthook.yml is byte-identical to
// the configuration praetor scaffolds, i.e. safe to activate without review.
func (s *adoptSession) lefthookConfigIsPraetor() bool {
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil {
		return false
	}
	data, err := readRepoFile(full)
	if err != nil {
		return false
	}
	current, checkpoint := currentLefthookRendering(data)
	return current && (!checkpoint || checkpointFilesPresent(s.repoPath))
}

// resolveHooksDirForInstall asks git for the hooks directory. Without git on PATH it
// falls back to <repo>/.git/hooks for plain checkouts and refuses gitlink checkouts.
func (s *adoptSession) resolveHooksDirForInstall(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		gitDir := filepath.Join(s.repoPath, ".git")
		if !util.DirExists(gitDir) {
			return "", fmt.Errorf("git is not installed and %s is not a directory: %w", gitDir, err)
		}
		s.report.addWarning("git hooks: git is not on PATH; assumed the default hooks directory .git/hooks")
		return filepath.Join(gitDir, "hooks"), nil
	}
	return ResolveGitHooksDir(ctx, s.repoPath)
}

// installFallbackHook writes the praetor pre-commit hook. An existing hook that praetor
// did not write is preserved unless Force is set, in which case it is renamed to
// pre-commit.bak before the praetor hook replaces it.
func (s *adoptSession) installFallbackHook(hookPath string) error {
	display := displayHookPath(s.repoPath, hookPath)
	if fileExists(hookPath) {
		// #nosec G304 -- hookPath is the hooks directory git reported for this repository.
		existing, err := os.ReadFile(hookPath)
		if err != nil {
			return fmt.Errorf("read existing hook %s: %w", hookPath, err)
		}
		if !bytes.Contains(existing, []byte(fallbackPreCommitMarker)) {
			if !s.opts.Force {
				s.report.recordSkipped(display, "existing pre-commit hook was not written by praetor and was preserved; re-run with --force to replace it (a .bak copy is kept)")
				return nil
			}
			if err := os.Rename(hookPath, hookPath+hookBackupExt); err != nil {
				return fmt.Errorf("back up existing hook %s: %w", hookPath, err)
			}
			s.report.addWarning("git hooks: existing %s moved to %s%s", display, display, hookBackupExt)
		}
	}
	if err := writeRepoFile(hookPath, []byte(buildFallbackPreCommitScript()), execPerm); err != nil {
		return err
	}
	s.report.recordCreated(display, "Installed fallback pre-commit hook (lefthook is not available)")
	return nil
}

// displayHookPath renders a hook path relative to the repository when possible.
func displayHookPath(repoPath, hookPath string) string {
	rel, err := filepath.Rel(repoPath, hookPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return hookPath
	}
	return filepath.ToSlash(rel)
}
