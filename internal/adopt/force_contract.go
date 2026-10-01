package adopt

// ForceContract states what --force adds to a plain adoption run, for the CLI flag help
// (cmd/standardsctl/adopt.go). The standards_adopt MCP schema (cmd/standards-mcp/tools_adoption.go)
// restates the same clauses in the agent register, as a string literal the register-source
// extraction reads and caveman-lints, and TestCreateAdoptTool_ForceStatesTheContract holds it to
// every clause. docs/adoption.md ("What a forced re-adoption changes") states the contract file by
// file.
//
// Each clause is a gate of this package, so a change to one of them changes this text too:
// reconcileLockfile and prepareCatalogWrites rebuild the lock and its catalog; scaffold.auditLocked
// marks the scaffolds --force may overwrite (the documentation gate's managed files, the branch
// ruleset while rulesetRequired holds); reconcileDevContainer, reconcileDocumentationMakefile and
// publishGitAttributes admit their overwrites only under --force; mergeEditorFile merges editor
// JSON; refreshAgentHarness regenerates the harness and keeps the repository's additions;
// planOwnedHarness sets only the platform of an operator-owned Paperclip harness; and
// replaceExisting records every overwrite as replace, or a merge as merge, with its line delta and
// backup. Every other scaffold is kept when it differs (scaffoldDriftNote).
const ForceContract = "Also rebuild .standards.lock and its pinned catalog from the lock source; rewrite each " +
	"drifted file audit compares byte for byte (documentation gate files, the branch ruleset while the policy " +
	"requires one, the DevContainer, an edited Makefile or .gitattributes managed block); merge managed values " +
	"into editor JSON; regenerate the AGENTS.md harness, keeping the repository's additions; and set a Paperclip " +
	"platform that names another repository. Each such file is reported as replace, or merge for editor JSON, with " +
	"its line delta, and backed up under .workingdir/adopt-backups when git ignores that path. --force overwrites " +
	"no other file audit does not verify: delete such a file and re-run adopt to regenerate it"
