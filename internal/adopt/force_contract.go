package adopt

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ForceContract states what --force adds to a plain adoption run, for the CLI flag help
// (cmd/standardsctl/adopt.go). The standards_adopt MCP schema (cmd/standards-mcp/tools_adoption.go)
// restates the same clauses in the agent register, as a string literal the register-source
// extraction reads and caveman-lints, and TestCreateAdoptTool_ForceStatesTheContract pairs every
// clause of this text with its restatement there. docs/adoption.md ("What a forced re-adoption
// changes") states the contract file by file.
//
// Each clause is a gate of this package, so a change to one of them changes this text too:
// reconcileLockfile and prepareCatalogWrites rebuild the lock and its catalog; scaffold.auditLocked
// marks the scaffolds --force may overwrite (the documentation gate's managed files, the branch
// ruleset while rulesetRequired holds); reconcileDevContainer, reconcileDocumentationMakefile and
// publishGitAttributes admit their overwrites only under --force; mergeEditorFile merges editor
// JSON; refreshAgentHarness regenerates the harness and keeps the repository's additions;
// planOwnedHarness resets only the platform of an operator-owned Paperclip harness that names
// another repository to this one; and replaceExisting records every overwrite as replace, or a
// merge as merge, with its line delta and backup. Every other scaffold is kept when it differs
// (scaffoldDriftNote).
const ForceContract = "Also rebuild .standards.lock and its pinned catalog from the lock source; rewrite each " +
	"drifted file audit compares byte for byte (documentation gate files, the branch ruleset while the policy " +
	"requires one, the DevContainer, an edited Makefile or .gitattributes managed block); merge managed values " +
	"into editor JSON; regenerate the AGENTS.md harness, keeping the repository's additions; and reset a Paperclip " +
	"platform that names another repository to this repository. Each such file is reported as replace, or merge " +
	"for editor JSON, with its line delta, and backed up under .workingdir/adopt-backups when git ignores that " +
	"path. Every other file audit leaves unverified stays as it is: delete one and re-run adopt to regenerate it"

// LockSourcePlaceholder stands in a printed command for the praetor checkout the operator passes
// as --lock-source-root, when the code printing it was given none.
const LockSourcePlaceholder = "<praetor checkout>"

// ForceCommand returns the forced re-adoption a remedy tells the operator to run. --force rebuilds
// .standards.lock, which needs a lock source (ErrLockSourceRequired, a dry run included), so the
// command always names --lock-source-root: lockSource when the caller has one, else
// LockSourcePlaceholder. flags, such as --dry-run, go between --force and --lock-source-root.
//
// Every message that tells the operator to re-run adopt with --force builds the command here, so
// none of them names a forced run that stops at the lock step (TestForceRemedies in
// force_contract_test.go).
func ForceCommand(lockSource string, flags ...string) string {
	if lockSource == "" {
		lockSource = LockSourcePlaceholder
	}
	command := make([]string, 0, len(flags)+4)
	command = append(command, util.PraetorCLI, "adopt", "--force")
	command = append(command, flags...)
	return strings.Join(append(command, "--lock-source-root="+lockSource), " ")
}

// forceCommand is the ForceCommand a remedy of this session names: it carries the lock source
// this run was given, or the placeholder when it was given none.
func (s *adoptSession) forceCommand() string {
	return ForceCommand(s.opts.LockSourceRoot)
}
