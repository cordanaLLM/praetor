package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	lefthookFile    = "lefthook.yml"
	evasionHookFile = ".config/agent/hooks/block_evasion.py"
	preCommitHook   = "pre-commit"
	hookBackupExt   = ".bak"
	// fallbackPreCommitMarker identifies a pre-commit hook written by praetor.
	fallbackPreCommitMarker = "# praetor-managed pre-commit hook"
	// preCommitAuditArgs is the audit every generated pre-commit hook runs. It passes --offline,
	// so no commit waits on the forge or fails on live Actions drift: the live checks
	// (cmd/standardsctl/audit_forge.go) ask the GitHub API up to about 130 times, each request
	// bounded at 15 seconds and the whole read at three minutes. The pre-push audit and CI run
	// the audit online, since a push needs the network anyway (docs/guides/actions-live-checks.md).
	preCommitAuditArgs = "audit --offline"
	// prePushAuditArgs is the audit the generated pre-push hook runs: online, the live checks
	// included.
	prePushAuditArgs = "audit"
	// prePushGateArgs is the gate the generated pre-push hook runs. It passes
	// gating.AdmitUnsupportedFlag, so a root with neither go.mod nor Cargo.lock (Meson, CMake,
	// npm, Python) is admitted without a receipt, its unverified languages named, instead of every
	// push being refused (#648); a Go or Cargo root is refused as before when its stages did not run.
	prePushGateArgs = "gate run --path=. --" + gating.AdmitUnsupportedFlag
	// govulnGateArgs is the Go vulnerability gate the generated pre-push security job runs where
	// govulncheck is installed: govulncheck at symbol level, judged against the repository's
	// OpenVEX document (internal/govuln), the check the gate run's security stage makes. The job
	// ran `govulncheck ./...` before, which failed on a called symbol only (#778).
	govulnGateArgs = "security govuln"
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

// lefthookPythonCommand renders a lefthook run line that starts a Python hook script through
// the launcher adoption writes with the checkpoint scripts (checkpointLauncher). Which
// interpreter runs is the launcher's decision alone: it tries fixed candidates and proves one
// before it starts it, and no variable selects another (#339).
//
// The line holds no quote and no expansion. Lefthook's Windows executor hands a run line to sh
// without preserving its inner quotes, so a rule stated inline, with a quoted variable, split an
// interpreter path that held a space there; a path to a script and plain arguments cannot be
// split that way.
func lefthookPythonCommand(args string) string {
	return "sh " + checkpointLauncher + " " + args
}

// optionalToolCommand renders a lefthook run line for a third-party tool that is skipped
// when absent but blocks when it fails.
func optionalToolCommand(tool, args string) string {
	return optionalToolGuard(tool, tool+" "+args)
}

// optionalToolGuard renders a lefthook run line that runs command only where the third-party
// tool it needs is installed, and otherwise skips with the reason. A failing command blocks.
func optionalToolGuard(tool, command string) string {
	return "if command -v " + tool + " >/dev/null 2>&1; then " + command +
		"; else echo " + tool + " is not installed, skipping >&2; fi"
}

// rootMarkerCommand renders a lefthook run line for a language tool. It runs command only where
// the repository root holds marker (go.mod, Cargo.toml) and otherwise skips with the reason, as
// the gate's own security and test stages already do (internal/gating/pipeline.go). Without the
// guard a repository with no root module failed every push on govulncheck and every commit
// touching a .go file on go vet (#242). The reason avoids ": " so the line stays one plain YAML
// scalar.
func rootMarkerCommand(marker, skipped, command string) string {
	return "if [ -f " + marker + " ]; then " + command +
		"; else echo no " + marker + " at the repository root, skipping " + skipped + " >&2; fi"
}

// goModuleCommand is rootMarkerCommand for a Go module tool.
func goModuleCommand(skipped, command string) string {
	return rootMarkerCommand("go.mod", skipped, command)
}

// cargoCommand is rootMarkerCommand for a Cargo tool.
func cargoCommand(skipped, command string) string {
	return rootMarkerCommand("Cargo.toml", skipped, command)
}

// lefthookJobLanguages are the languages lefthook.yml carries jobs for: Go (gofmt and go vet
// before a commit, govulncheck before a push) and Rust (cargo fmt and cargo clippy before a
// commit). Every other language gets the governance jobs alone.
const lefthookJobLanguages = hisscatalog.LanguageGo | hisscatalog.LanguageRust

// lefthookLanguages returns the languages whose jobs lefthook.yml carries for a repository whose
// verification plan is plan: the languages that plan detected (planLanguages), the set the
// harness rows are rendered for too, so no second detector decides the hooks (#568). An unknown
// set, nothing detected, keeps every language's jobs, as it keeps every HISS clause and check
// (hisscatalog.Rule.AdoptedFor): each job runs only on its own staged files and root marker.
func lefthookLanguages(plan *VerificationPlan) hisscatalog.Language {
	languages := planLanguages(plan)
	if languages == 0 {
		return lefthookJobLanguages
	}
	return languages & lefthookJobLanguages
}

// lefthookLanguageNames names languages for the rendering's header, in a fixed order.
func lefthookLanguageNames(languages hisscatalog.Language) string {
	names := make([]string, 0, 2)
	if languages&hisscatalog.LanguageGo != 0 {
		names = append(names, "Go")
	}
	if languages&hisscatalog.LanguageRust != 0 {
		names = append(names, "Rust")
	}
	return strings.Join(names, ", ")
}

// lefthookPreCommitJobs renders the pre-commit jobs of languages. The Rust clippy job runs the
// gate's own clippy command (gating.CargoClippyArgs), so a commit and a push lint alike.
func lefthookPreCommitJobs(languages hisscatalog.Language) string {
	jobs := ""
	if languages&hisscatalog.LanguageGo != 0 {
		jobs += "    gofmt:\n      glob: \"*.go\"\n" + lefthookRun("gofmt -w {staged_files}") + "      stage_fixed: true\n" +
			"    govet:\n      glob: \"*.go\"\n" + lefthookRun(goModuleCommand("go vet", "go vet ./..."))
	}
	if languages&hisscatalog.LanguageRust != 0 {
		clippy := "cargo " + strings.Join(gating.CargoClippyArgs(), " ")
		jobs += "    rustfmt:\n      glob: \"*.rs\"\n" + lefthookRun(cargoCommand("cargo fmt", "cargo fmt --all --check")) +
			"    clippy:\n      glob: \"*.rs\"\n" + lefthookRun(cargoCommand("cargo clippy", clippy))
	}
	return jobs
}

// lefthookPrePushJobs renders the pre-push jobs of languages: the Go vulnerability gate for Go
// (govulnGateArgs). The gate job runs cargo audit for a Cargo.lock itself.
func lefthookPrePushJobs(languages hisscatalog.Language) string {
	if languages&hisscatalog.LanguageGo == 0 {
		return ""
	}
	return "    security:\n" + lefthookRun(goModuleCommand(govuln.DefaultScanner,
		optionalToolGuard(govuln.DefaultScanner, lefthookGovernedCommand(govulnGateArgs))))
}

// lefthookHeader is the rendering's leading comment, naming the languages it carries jobs for.
func lefthookHeader(languages hisscatalog.Language) string {
	scope := "HISS Governance"
	if names := lefthookLanguageNames(languages); names != "" {
		scope = names + " & " + scope
	}
	return "# Lefthook Configuration (" + scope + ")\n" +
		"# Go and Rust jobs follow the detected languages; both when none was detected.\n" +
		"# Governance commands fail closed: a failing or missing praetorctl blocks the\n" +
		"# commit or push. The pre-commit audit runs --offline; the pre-push audit\n" +
		"# reads the forge's live Actions state when a token and a github.com origin\n" +
		"# are present. The pre-push gate job signs a receipt only after a Go\n" +
		"# (go.mod) or Cargo (Cargo.lock) toolchain stage ran. A root with neither\n" +
		"# gets no receipt: the job names the languages it could not verify and\n" +
		"# admits the push; a root with either fails when its stages did not run.\n"
}

// buildLefthookYAMLFor renders lefthook.yml with the jobs of languages (lefthookLanguages), and
// with the checkpoint lifecycle jobs when checkpoint is set. It opens with a document start and
// folds every run line longer than yamllint's default limit (lefthookRun), so an adopter whose
// hooks lint the whole tree with yamllint's defaults accepts it (BUG-782).
func buildLefthookYAMLFor(languages hisscatalog.Language, checkpoint bool) string {
	governed := lefthookGovernedCommand
	checkpointJobs := ""
	if checkpoint {
		checkpointJobs = "agent-checkpoint-tool:\n  commands:\n    checkpoint:\n" +
			lefthookRun(lefthookPythonCommand("-B "+checkpointScript+" --event tool --json --marker")) +
			"agent-checkpoint-stop:\n  commands:\n    checkpoint:\n" +
			lefthookRun(lefthookPythonCommand("-B "+checkpointScript+" --event stop --json --marker"))
	}
	return lefthookHeader(languages) +
		"---\n" +
		checkpointJobs + "pre-commit:\n" +
		"  parallel: true\n" +
		"  commands:\n" +
		lefthookPreCommitJobs(languages) +
		"    context-check:\n" + lefthookRun(governed("compile-context --verify")) +
		"    hiss-audit:\n" + lefthookRun(governed(preCommitAuditArgs)) +
		"\n" +
		"post-commit:\n" +
		"  commands:\n" +
		"    state-sync:\n" + lefthookRun(governed("state sync .")) +
		"    dedupe-cadence:\n" + lefthookRun(governed("dedupe cadence --threshold=20 --record .")) +
		"\n" +
		"pre-push:\n" +
		"  parallel: false\n" +
		"  commands:\n" +
		lefthookPrePushJobs(languages) +
		"    flavor-audit:\n" + lefthookRun(governed("flavor audit .")) +
		"    audit:\n" + lefthookRun(governed(prePushAuditArgs)) +
		"    gate:\n" + lefthookRun(governed(prePushGateArgs))
}

// blockEvasionTemplate is the agent PreToolUse interceptor adoption writes. It is not a git
// hook: git skips hooks entirely on --no-verify, so only the agent harness can observe the
// command. buildBlockEvasionPY fills the placeholders from internal/agenthook.
//
// It used to carry its own copy of the rules, older and weaker than the engine's, and it
// allowed what it could not read: an empty or non-JSON stdin, a payload without
// tool_input.command, and it crashed with exit 1 (not blocking) on a JSON array. It now reads
// a bounded stdin and refuses, with the blocking exit 2, any input that is not one JSON object
// carrying a nonempty tool_input.command, and any command over the Python scan bounds.
// Native payloads of other clients are agenthook's dialects, reached through
// `praetorctl hook`; this script does not guess at them.
//
// The rendering passes black and flake8 at 100 columns with their defaults (BUG-782): the
// generated values go through the layouts in emitted_layout.go, and every function stays
// within the 35 lines the strictest public dogfood policy allows (checkpoint_loc_test.go).
const blockEvasionTemplate = `#!/usr/bin/env python3
"""Agent PreToolUse evasion interceptor (HISS), written by praetorctl adopt.

Wire this script as a PreToolUse hook of the agent harness's shell tool. The harness passes
the pending tool call as one JSON object on stdin with the shell command at
tool_input.command; a command may instead be passed as arguments. Exit code 2 blocks the
call and returns the reason to the agent. Input of any other shape is refused, not allowed.

The rules and their refusals are the engine's built-in command policy (internal/agenthook),
rendered at adoption; operator rules belong in hooks.command_policy.deny of .standards.yaml.

A git hook cannot observe --no-verify because git skips hooks entirely, so this script
is deliberately not part of lefthook.yml.
"""

import json
import os
import re
import sys

MAX_INPUT_BYTES = {{MAX_INPUT_BYTES}}
MAX_SCAN_CHARS = {{MAX_SCAN_CHARS}}
MAX_SCAN_LINE_CHARS = {{MAX_SCAN_LINE_CHARS}}
BLOCK_EXIT = 2

{{REFUSALS}}
RULES = [
{{RULES}}]

# The RULES judged without the words of read-only commands chained after a commit.
READ_ONLY_EXEMPT = [
{{READ_ONLY_EXEMPT}}]
{{READ_ONLY_WORDS}}{{READ_ONLY_VETO}}
LEFTHOOK_DISABLED = [
{{LEFTHOOK_DISABLED}}]
LEFTHOOK_NARROWING = {{LEFTHOOK_NARROWING}}


class Blocked(Exception):
    pass


def read_payload_command(stream):
    raw = stream.read(MAX_INPUT_BYTES + 1)
    if len(raw) > MAX_INPUT_BYTES:
        raise ValueError("hook input exceeds " + str(MAX_INPUT_BYTES) + " bytes")
    payload = json.loads(raw.decode("utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("hook input must be one JSON object")
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        raise ValueError("tool_input must be an object")
    command = tool_input.get("command")
    if not isinstance(command, str) or not command.strip():
        raise ValueError("tool_input.command must be nonempty text")
    return command


def pending_command():
    if len(sys.argv) > 1:
        return " ".join(sys.argv[1:])
    if sys.stdin.isatty():
        raise Blocked(NO_INPUT_REFUSAL)
    try:
        return read_payload_command(sys.stdin.buffer)
    except (ValueError, OSError, RecursionError) as error:
        raise Blocked(INVALID_INPUT_REFUSAL + str(error))


def check_environment(environ):
    value = environ.get("LEFTHOOK")
    for disabled, refusal in LEFTHOOK_DISABLED:
        if value == disabled:
            raise Blocked(refusal)
    for name in LEFTHOOK_NARROWING:
        if environ.get(name):
            raise Blocked(NARROWING_REFUSAL)


def without_read_only_words(command):
    # The words of git log, head, sed -n and the other read-only commands READ_ONLY_WORDS
    # names, chained after ;, &&, || or |, belong to that command and are dropped, its name
    # kept. A command holding a construct READ_ONLY_VETO names keeps every word.
    if re.search(READ_ONLY_VETO, command):
        return command
    return re.sub(READ_ONLY_WORDS, r"\1\2", command)


def check_command(command):
    # re backtracks, so a longer command or line could stall this script past the harness's
    # hook timeout. Such a command is refused, never truncated.
    if len(command) > MAX_SCAN_CHARS:
        raise Blocked(SCAN_BOUND_REFUSAL)
    if max(len(line) for line in command.split("\n")) > MAX_SCAN_LINE_CHARS:
        raise Blocked(SCAN_BOUND_REFUSAL)
    judged = without_read_only_words(command)
    for pattern, refusal in RULES:
        text = judged if pattern in READ_ONLY_EXEMPT else command
        if re.search(pattern, text):
            raise Blocked(refusal + pattern)


def main():
    try:
        check_environment(os.environ)
        check_command(pending_command())
    except Blocked as blocked:
        sys.stderr.write(str(blocked) + "\n")
        sys.exit(BLOCK_EXIT)
    sys.exit(0)


if __name__ == "__main__":
    main()
`

// noInputRefusal is the refusal of a call with neither arguments nor piped input. It is the
// interceptor's own: the Go engine always reads the payload its client pipes in.
const noInputRefusal = "[BLOCKED BY HISS] input: PreToolUse JSON on stdin or command arguments required"

// interceptorRefusal is one refusal text the interceptor prints, under its Python name.
type interceptorRefusal struct {
	name string
	text string
}

// interceptorRefusals returns the refusal texts the interceptor declares, one module
// constant each, in the order they are rendered. Every text but noInputRefusal is
// agenthook's wording, so the interceptor refuses in the engine's words (BUG-1014).
func interceptorRefusals() []interceptorRefusal {
	refusals := []interceptorRefusal{
		{"NO_INPUT_REFUSAL", noInputRefusal},
		{"INVALID_INPUT_REFUSAL", agenthook.InvalidInputRefusal},
		{"NARROWING_REFUSAL", agenthook.NarrowingRefusal},
		{"SCAN_BOUND_REFUSAL", agenthook.ScanBoundRefusal()},
	}
	seen := map[string]bool{}
	for _, rule := range agenthook.BuiltinRules() {
		if name := ruleRefusalName(rule.Invariant); !seen[name] {
			seen[name] = true
			refusals = append(refusals, interceptorRefusal{name, rule.RefusalPrefix()})
		}
	}
	return refusals
}

// ruleRefusalName is the Python name of the refusal a rule of invariant prints ahead of its
// pattern: HISS_REFUSAL, DEV_01_REFUSAL.
func ruleRefusalName(invariant string) string {
	return strings.ToUpper(strings.ReplaceAll(invariant, "-", "_")) + "_REFUSAL"
}

// buildBlockEvasionPY renders the interceptor from the engine's command policy: the built-in
// rules (agenthook.BuiltinRules), the read-only exemption of the rules that carry it
// (agenthook.ReadOnlyWords, ReadOnlyVeto), the Lefthook environment checks, the input bound,
// the scan bounds of the Python adapters (agenthook.MaxScanChars, MaxScanLineChars) and the
// refusal texts. It never includes operator rules; those are repository configuration
// (ADR-0011).
func buildBlockEvasionPY() string {
	var refusals, rules, exempt, disabled strings.Builder
	for _, refusal := range interceptorRefusals() {
		refusals.WriteString(pythonAssignment(refusal.name, pyToken{refusal.text, pyString}))
	}
	for _, rule := range agenthook.BuiltinRules() {
		rules.WriteString(pythonPairEntry(pyToken{rule.Source, pyRaw}, pyToken{ruleRefusalName(rule.Invariant), pyName}))
		if rule.ReadOnlyExempt {
			exempt.WriteString(pythonItemLines("    ", pyToken{rule.Source, pyRaw}, ","))
		}
	}
	for _, value := range agenthook.LefthookDisableValues() {
		refusal := pyToken{agenthook.LefthookDisabledRefusal(value), pyString}
		disabled.WriteString(pythonPairEntry(pyToken{value, pyString}, refusal))
	}
	return strings.NewReplacer(
		"{{MAX_INPUT_BYTES}}", strconv.Itoa(agenthook.MaxInputBytes),
		"{{MAX_SCAN_CHARS}}", strconv.Itoa(agenthook.MaxScanChars),
		"{{MAX_SCAN_LINE_CHARS}}", strconv.Itoa(agenthook.MaxScanLineChars),
		"{{REFUSALS}}", refusals.String(),
		"{{RULES}}", rules.String(),
		"{{READ_ONLY_EXEMPT}}", exempt.String(),
		"{{READ_ONLY_WORDS}}", pythonAssignment("READ_ONLY_WORDS", pyToken{agenthook.ReadOnlyWords, pyRaw}),
		"{{READ_ONLY_VETO}}", pythonAssignment("READ_ONLY_VETO", pyToken{agenthook.ReadOnlyVeto, pyRaw}),
		"{{LEFTHOOK_DISABLED}}", disabled.String(),
		"{{LEFTHOOK_NARROWING}}", pythonStringTuple(agenthook.LefthookNarrowingVariables()),
	).Replace(blockEvasionTemplate)
}

// pythonStringTuple renders plain ASCII names as a Python tuple literal of strings, laid out
// as black leaves it: no trailing comma unless the tuple holds one element.
func pythonStringTuple(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, `"`+value+`"`)
	}
	if len(quoted) == 1 {
		return "(" + quoted[0] + ",)"
	}
	return "(" + strings.Join(quoted, ", ") + ")"
}

// buildFallbackPreCommitScript renders the hook installed when lefthook is unavailable.
// It only runs binaries found on PATH and fails closed when none is installed.
func buildFallbackPreCommitScript() string {
	missing := "'[HISS] neither " + util.PraetorCLI + " nor " + util.LegacyCLI +
		" is installed; refusing to commit unverified changes'"
	return "#!/usr/bin/env bash\n" +
		fallbackPreCommitMarker + " (installed because lefthook is not available)\n" +
		"set -euo pipefail\n" +
		util.ShellCLI("compile-context --verify", missing) + "\n" +
		util.ShellCLI(preCommitAuditArgs, missing) + "\n"
}

// reconcileGitHooks classifies an existing lefthook.yml before it installs anything
// (classifyLefthookConfig). A configuration that is neither a current nor an earlier Praetor
// rendering is kept, --force included, and not activated (keepLefthookConfig). Otherwise the
// checkpoint bundle is installed, lefthook.yml is written, migrated or verified, and local git
// hooks are activated for the rendering praetor wrote.
func reconcileGitHooks(ctx context.Context, s *adoptSession) error {
	existing, exists, err := s.readExistingLefthook()
	if err != nil {
		return err
	}
	languages := s.lefthookLanguages()
	var identity lefthookIdentity
	if exists {
		identity = classifyLefthookConfig(existing, languages)
	}
	if identity.reason != "" {
		return keepLefthookConfig(ctx, s, identity)
	}
	checkpointReady, err := reconcileCheckpointLifecycle(ctx, s, false)
	if err != nil {
		return err
	}
	lefthookWritten, err := s.writeLefthookConfig(ctx, lefthookTarget{languages: languages, checkpoint: checkpointReady}, existing, identity.prior)
	if err != nil {
		return err
	}
	warnPreservedCheckpoint(s, checkpointReady, lefthookWritten)
	if err := reconcileEvasionHook(ctx, s, false); err != nil {
		return err
	}
	if s.opts.SkipHookActivation {
		return nil
	}
	return s.activateGitHooks(ctx, lefthookWritten)
}

// keepLefthookConfig records why adoption keeps lefthook.yml and installs beside it only what
// is absent: the checkpoint files, each on its own, never refreshing one that exists, since the
// kept configuration may run its own copies; and the interceptor, refreshed from an unedited
// earlier rendering only when the configuration does not extend the canonical policy, whose
// vendored bundle owns it (BUG-858).
func keepLefthookConfig(ctx context.Context, s *adoptSession, identity lefthookIdentity) error {
	if _, err := reconcileCheckpointLifecycle(ctx, s, true); err != nil {
		return err
	}
	s.report.recordSkipped(lefthookFile, identity.reason)
	return reconcileEvasionHook(ctx, s, identity.canonical)
}

// lefthookTarget is the rendering this run writes: the jobs of languages, and the checkpoint
// lifecycle jobs when checkpoint is set.
type lefthookTarget struct {
	languages  hisscatalog.Language
	checkpoint bool
}

// writeLefthookConfig writes target's rendering over an earlier Praetor rendering, and otherwise
// scaffolds it. It reports whether it wrote. classifyLefthookConfig has kept every other
// configuration, so existing is absent, prior, or a current rendering for target's languages,
// line endings aside:
//
//   - A migration writes the rendering's own LF bytes even over a CRLF checkout of an earlier
//     one: activation trusts only those exact bytes (lefthookConfigIsPraetor), and git stores the
//     working-tree LF text unchanged under core.autocrlf.
//   - The current rendering without checkpoint jobs gains them once the lifecycle is installed.
//     The one with them is kept when this run did not install the lifecycle, so a run without
//     --lock-source-root does not strip them.
//   - Under --force, a CRLF checkout of the current rendering is rewritten with its LF bytes:
//     the scaffold verifies such a copy, so it would stay bytes activation refuses.
//
// The scaffold reads lefthook.yml only as a regular file, so a symlinked one is kept and
// reported unverified, --force included.
func (s *adoptSession) writeLefthookConfig(ctx context.Context, target lefthookTarget, existing []byte, prior bool) (bool, error) {
	current := buildLefthookYAMLFor(target.languages, target.checkpoint)
	match := matchCurrentLefthook(existing, target.languages)
	switch {
	case prior:
		return true, s.migrateLefthookConfig(current, "Migrated an earlier Praetor-generated Lefthook configuration to the current template")
	case match.found && !match.checkpoint && target.checkpoint:
		return true, s.migrateLefthookConfig(current, "Added the checkpoint lifecycle jobs to the Praetor-generated Lefthook configuration")
	case match.found && match.checkpoint && !target.checkpoint:
		s.report.recordReconciled(lefthookFile, "Existing Praetor-generated Lefthook configuration kept with its checkpoint "+
			"lifecycle jobs; this run did not install the checkpoint lifecycle")
		return false, nil
	case match.found && !match.exact && s.opts.Force:
		return true, s.migrateLefthookConfig(current, "Rewrote a line-ending checkout of the current Praetor-generated "+
			"Lefthook configuration with its LF bytes, the only bytes hook activation trusts")
	}
	state, err := s.scaffoldFile(ctx, scaffold{
		rel:      lefthookFile,
		perm:     filePerm,
		content:  []byte(current),
		created:  "Scaffolded Lefthook configuration for local pre-commit and pre-push enforcement",
		verified: "Existing Lefthook configuration verified present",
	})
	return state == scaffoldWritten, err
}

// migrateLefthookConfig writes current, the rendering's own LF bytes, over a Praetor text at
// lefthook.yml and records detail. A dry run records the rewrite it would make.
func (s *adoptSession) migrateLefthookConfig(current, detail string) error {
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil {
		return err
	}
	if err := s.write(full, []byte(current), filePerm); err != nil {
		return err
	}
	s.report.recordReconciled(lefthookFile, detail)
	return nil
}

// isLineEndingCheckout reports whether existing holds current's text in another consistent
// line-ending style: equal to it once line endings are normalised (util.CanonicalTextEquivalent),
// but not byte for byte. Mixed line endings are no checkout.
func isLineEndingCheckout(existing []byte, current string) bool {
	if bytes.Equal(existing, []byte(current)) {
		return false
	}
	equivalent, err := util.CanonicalTextEquivalent(existing, []byte(current))
	return err == nil && equivalent
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

// priorEvasionHookDigests are the digests (priorRendering) of every interceptor rendering a
// Praetor release wrote at evasionHookFile, keyed to what produced them; the current rendering
// is one of them. Audit does not read the interceptor, so --force keeps an edited copy: these
// texts are what adoption refreshes to the current rendering without --force, in the file's own
// consistent line-ending style. testdata/evasion reproduces each digest, and
// TestPriorEvasionHookDigests_Boundary_CurrentRenderingRecorded fails until a changed rendering
// is recorded here, so the next release still refreshes it (evasion_prior_test.go).
var priorEvasionHookDigests = map[string]string{
	"64fa5045ca8d31b1917383b878a9549960cac8c8b684013369af171f9f2fcd3e": "HISS-16 labels, own pattern list",
	"15f46f44fcb492f59a63c186196d5be80250af02ac64b51087890094a0087cb5": "HISS labels, own pattern list",
	"3c0553691766524d3d7c22454e6acb4d6525295aa79f300a783121f329bad800": "engine rules, bounded JSON input",
	"eb5beb82cfcc85f4f398c504696750d889ffe49171e61956446adbcd0927dbde": "black-clean layout, engine refusal texts",
	"fed57187eef4b43b8d4810bcad13c1da8339d333ea74b675c0371cf6c93b309b": "abbreviated skip options, Windows hook removal",
	"93c213517c41de42c3a44b9f950b205a94d43d9c1ea7a202266912c793ae68cb": "read-only chained command exemption",
}

// reconcileEvasionHook scaffolds the interceptor. It is generated, not audit-verified, so an
// edited copy is kept, --force included, and an unedited earlier rendering is refreshed
// (priorEvasionHookDigests). With vendored set, the repository carries the canonical hook
// policy, whose interceptor belongs to that vendored bundle: an existing copy is never
// refreshed, so the policy and its interceptor stay one version.
func reconcileEvasionHook(ctx context.Context, s *adoptSession, vendored bool) error {
	sc := scaffold{
		rel:       evasionHookFile,
		perm:      execPerm,
		content:   []byte(buildBlockEvasionPY()),
		created:   "Scaffolded standalone agent PreToolUse anti-evasion interceptor; the agent-hooks step registers the engine call",
		verified:  "Existing agent anti-evasion interceptor verified present",
		refreshed: "Refreshed an unedited earlier Praetor agent anti-evasion interceptor to the current rendering",
	}
	if !vendored {
		sc.prior = priorEvasionHookDigests
	}
	_, err := s.scaffoldFile(ctx, sc)
	return err
}

// activateGitHooks installs hooks through lefthook, or the fallback hook when lefthook
// is unavailable. It never activates a lefthook.yml that praetor did not write: hook
// commands are shell executed at the adopter's next commit. A dry run installs nothing and
// records the hook the run would install (planHookActivation).
func (s *adoptSession) activateGitHooks(ctx context.Context, lefthookWritten bool) error {
	if !lefthookWritten && !s.lefthookConfigIsPraetor() {
		s.report.recordSkipped(lefthookFile, "existing lefthook.yml is not byte for byte a Praetor rendering adoption can activate; "+
			"hooks were not activated. Review its run: commands and run 'lefthook install' yourself, or "+lefthookRegenerateHint)
		return nil
	}
	hooksDir, err := s.resolveHooksDirForInstall(ctx)
	if err != nil {
		s.report.addError("git hooks: %v", err)
		return nil
	}
	hookPath := filepath.Join(hooksDir, preCommitHook)
	if s.opts.DryRun {
		return s.planHookActivation(hookPath)
	}
	installed := fileExists(hookPath)
	if _, err := util.RunCommand(ctx, s.repoPath, "lefthook", "install"); err == nil {
		if !fileExists(hookPath) {
			s.report.addError("git hooks: lefthook install completed but %s was not created", hookPath)
			return nil
		}
		s.recordLefthookInstall(hookPath, installed)
		return nil
	}
	return s.installFallbackHook(hookPath)
}

// planHookActivation records, in a dry run, the pre-commit hook activateGitHooks installs at
// hookPath, and installs nothing: lefthook install when lefthook is on PATH, else the fallback
// hook, which installFallbackHook plans without writing. Only running lefthook install shows
// whether it succeeds; one that fails installs the fallback hook at the same path, so the planned
// path and whether it is created or reconciled hold either way. The dry run used to skip hook
// activation, so the hook the real run installs under .git/hooks was never previewed (#366).
func (s *adoptSession) planHookActivation(hookPath string) error {
	if _, err := exec.LookPath("lefthook"); err != nil {
		return s.installFallbackHook(hookPath)
	}
	s.recordLefthookInstall(hookPath, fileExists(hookPath))
	return nil
}

// recordLefthookInstall records the pre-commit hook lefthook install leaves at hookPath: created,
// or reconciled over the hook installed beforehand.
func (s *adoptSession) recordLefthookInstall(hookPath string, installed bool) {
	display := displayHookPath(s.repoPath, hookPath)
	if installed {
		s.report.recordReconciled(display, "Re-activated local Git hooks via lefthook install over the existing hook")
		return
	}
	s.report.recordCreated(display, "Activated local Git hooks via lefthook install")
}

// lefthookConfigIsPraetor reports whether the existing lefthook.yml is byte-identical to a
// configuration praetor scaffolds for this repository's languages, i.e. safe to activate
// without review.
func (s *adoptSession) lefthookConfigIsPraetor() bool {
	data, exists, err := s.readExistingLefthook()
	if err != nil || !exists {
		return false
	}
	match := matchCurrentLefthook(data, s.lefthookLanguages())
	return match.exact && (!match.checkpoint || checkpointFilesPresent(s.repoPath))
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

// foreignPreCommitNote says why adoption kept a pre-commit hook it did not write and what the
// operator can do about it.
const foreignPreCommitNote = "existing pre-commit hook was not written by praetor; kept, --force included, " +
	"because it is the repository's. Audit accepts it only when lefthook or the pre-commit framework wrote it " +
	"(AuditInstalledGitHook); otherwise remove it and re-run adopt to install the praetor hook"

// installFallbackHook writes the praetor pre-commit hook. An existing hook that praetor did
// not write is kept, --force included: the hook is the repository's, and adoption neither
// replaces nor moves it; audit decides whether a known runner wrote it. An existing
// praetor hook is rewritten, which restores its mode, and listed as reconciled, never as
// created. A pre-commit.bak an earlier adoption left under --force is reported, never removed.
// A dry run records the same entry and writes nothing.
func (s *adoptSession) installFallbackHook(hookPath string) error {
	display := displayHookPath(s.repoPath, hookPath)
	warnLegacyHookBackup(s, hookPath, display)
	script := []byte(buildFallbackPreCommitScript())
	var existing []byte
	installed := fileExists(hookPath)
	if installed {
		// #nosec G304 -- hookPath is the hooks directory git reported for this repository.
		data, err := os.ReadFile(hookPath)
		if err != nil {
			return fmt.Errorf("read existing hook %s: %w", hookPath, err)
		}
		if !isFallbackPreCommitHook(data) {
			s.report.recordSkipped(display, foreignPreCommitNote)
			return nil
		}
		existing = data
	}
	if err := s.write(hookPath, script, execPerm); err != nil {
		return err
	}
	switch {
	case !installed:
		s.report.recordCreated(display, "Installed fallback pre-commit hook (lefthook is not available)")
	case bytes.Equal(existing, script):
		s.report.recordReconciled(display, "Fallback pre-commit hook already installed (lefthook is not available)")
	default:
		s.report.recordReconciled(display, "Refreshed the praetor fallback pre-commit hook (lefthook is not available)")
	}
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
