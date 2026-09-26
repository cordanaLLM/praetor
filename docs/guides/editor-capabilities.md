# Repository-aware editor generation

`praetorctl editors generate --path PATH` and `praetorctl editors verify --path PATH`
resolve the same repository capabilities before generating or checking editor
files. A repository without Go sources or a selected Go profile no longer receives
unconditional Go settings, Go inspections or a Go problem matcher.

## Selecting editors

A repository declares the editors it uses in `.standards.yaml`:

```yaml
editors: [vscode, neovim]
```

`praetorctl adopt`, onboarding and both `editors` subcommands resolve that list
through `editor.SelectEditors` (`internal/editor/selection.go`):

| Declaration | Result |
| :--- | :--- |
| key absent (or `editors:` with no value) | every supported editor, as before the key existed |
| a list of ids or aliases | exactly those editors |
| `editors: []` | no editor; nothing is generated or verified |

Every supported editor outside the selection is reported as not applicable:
`[NOT_APPLICABLE] Editors not selected: ...` from the CLI, and a `skip` action on
`editors` in the adoption report. Its files are neither generated nor verified, so a
file the repository deleted is not recreated by the next run. Existing files of an
unselected editor are left in place; delete them yourself.

Both subcommands also take `--editors=id[,id...]`, a comma-separated list of editor
ids or aliases (`cmd/standardsctl/editors.go`). A non-empty flag overrides the
manifest for that run. Omitting the flag, or passing an empty or all-separator value,
falls back to the manifest. Any id that does not resolve, from the flag or the
manifest, fails the whole run with `unknown editor id(s): <ids>; supported:
<canonical ids>` and writes nothing: a partially-matched list used to synthesize only
the recognized subset and silently drop the rest. Without the flag, a manifest that
cannot be read fails the same way instead of falling back to every editor.

Tests: `internal/editor/selection_test.go`,
`cmd/standardsctl/editors_selection_test.go`,
`internal/adopt/client_selection_test.go`,
`internal/harvester/onboard_selection_test.go`.

## Selecting agent clients

`agent_clients` selects the vendor context files `praetorctl compile-context` compiles
from `AGENTS.md` and the persona directories it copies `.agents/agents/*.md` into, with
the same absent, list and empty rules as `editors`:

```yaml
agent_clients: [claude, codex]
```

| Id | Context file | Persona directory |
| :--- | :--- | :--- |
| `claude` | `CLAUDE.md` | `.claude/agents` |
| `cursor` | `.cursor/rules/hiss-invariants.mdc` | none |
| `copilot` | `.github/copilot-instructions.md` | `.github/agents` |
| `windsurf` | `.windsurfrules` | none |
| `gemini` | `.gemini/GEMINI.md` | `.gemini/agents` |
| `codex` | `.codex/rules.md` | `.codex/agents` |

The registry is `vendorTargets` in `internal/agentcontext/render.go`. The compiler
reads the key from the manifest beside `AGENTS.md`, the one that already governs the
[text register](text-register.md) block, so `compile-context`, `compile-context
--verify`, `praetorctl audit`, adoption, onboarding and the MCP
`standards_compile_context` tool agree on which projections exist. Persona copies are
resolved by `compiler.SelectPersonaDirs` from the manifest at the root the personas are
projected into, which is the same file when `AGENTS.md` sits at that root. Unselected
context files and persona directories print as `[NOT_APPLICABLE]` and are neither
written, verified nor removed, so a directory the repository deletes stays deleted and
one it keeps for its own use is left alone. An unknown id fails with
`unknown agent client id(s): <ids>; supported: <ids>`.

The plugin copy under `.agents/plugins/praetor/agents` belongs to no client and is kept
whenever `.agents/plugins/praetor/plugin.json` exists. Tests:
`internal/agentcontext/render_selection_test.go`,
`internal/compiler/transpiler_selection_test.go`,
`internal/compiler/agents_selection_test.go`,
`cmd/standardsctl/compile_context_clients_test.go`,
`internal/adopt/client_selection_test.go`.

## Antigravity IDE

`antigravity` (aliases `agy`, `antigravity-ide`) joins the VS Code-compatible
family alongside `vscode`, `cursor` and `windsurf`, and shares their single
`.vscode/settings.json`. Requesting it adds two keys to that file, confirmed
against the installed IDE rather than assumed from its docs:

- `antigravity.searchMaxWorkspaceFileCount: 50000` — raises Jetski's per-workspace
  embedding scan bound above its shipped default of 5,000 (confirmed:
  `contributes.configuration` in the IDE's `extensions/antigravity/package.json`).
- `files.watcherExclude` — a core VS Code setting (confirmed: the workbench's own
  configuration schema) extended with every resolved private directory
  (`editor.Options.PrivateDirs`, default `.workingdir` and `.workingdir2`), build
  output (`bin`, `dist`) and the isolated gate/dogfood run worktrees under
  `.standards/worktrees`, to keep inotify-heavy scratch out of the file watcher.

These two keys are only added when `antigravity` is part of the requested editor
set; `vscode`/`cursor`/`windsurf`-only runs do not receive them. Run-count
retention for `~/.local/state/praetor/dogfood-local` is a separate, still-open
fix tracked on the issue that requested this key.

The shared resolver combines supported project markers and source formats with
explicit caller options. It excludes private state, dependency/build directories
and nested agent worktrees. An incomplete scan is an error rather than a claim
that the unexamined part of the repository has no relevant languages. The initial
scan limit is 4096 files.

Every renderer reads the resolved plan and nothing else (`Plan` in
`internal/editor/capabilities.go`); a capability the resolver rejected is omitted,
not asserted anyway:

- Commands. Default commands include `make verify-all` only when that literal
  target exists. The generator does not add `make build` merely because a Makefile
  exists. VS Code tasks, JetBrains external tools, Neovim user commands, Zed tasks,
  the Emacs `compile-command`, Fleet run configurations and Sublime build systems
  list exactly the resolved commands; an empty plan binds none. Target presence is
  structural evidence; generation does not run or certify the command. Callers
  using the Go API can provide explicit commands and languages through
  `editor.Options`.
- Language server. The Praetor LSP is written only for a Go workspace that holds
  a workspace-relative executable regular file at `editor.Options.LSPPath`, or,
  when that is unset, at `<BinaryDir>/standards-lsp` (`standards-lsp.exe` on
  Windows, where executability is the file extension). Otherwise VS Code settings
  carry no `standards.lsp.*` key and Neovim registers no server.
- Extensions. Recommendations are exactly the caller-supplied IDs verified in
  `editor.Options.ExtensionRegistry`; the generator does not contact an extension
  marketplace or prove publication from an extension's source directory. Without
  that evidence `.vscode/extensions.json` recommends nothing, `golang.go` included.

These settings do not prove installation, startup or native client activation.
`internal/editor/plan_render_test.go` covers each rule with evidence present,
absent and at its boundary.

## Complexity ceilings

The JetBrains inspection profile and the Neovim server settings state the
complexity ceilings the repository's policy resolves to, which are the ceilings
`praetorctl audit` enforces there. `praetorctl adopt` uses the policy its
session already resolved. `praetorctl editors`, `standards-lsp` (for the
workspace named in its `initialize` request) and the MCP
`standards_inspect_symbols` tool (for its server root) use
`config.ResolveRepositoryComplexity` (`internal/config/repository_policy.go`).
A locked repository resolves exactly as `praetorctl plan` resolves it: pinned
profiles, repository overrides and the audit function-length cap.

Every other state resolves to `config.HISSComplexityCeiling` (cyclomatic 10,
cognitive 15, statements 50 and the audit's function length,
`config.AuditMaxFuncLOC` = 60), tightened by any complexity override the
manifest declares. Overrides only tighten, so these states never state a limit
looser than the ceiling:

| Workspace state | Result | Stated |
| :--- | :--- | :--- |
| no `.standards.yaml` | ceiling | nothing |
| manifest, no `.standards.lock` | ceiling tightened by overrides | nothing |
| manifest or lock that does not resolve, including the lock `praetorctl init` writes | ceiling tightened by any readable override | `[WARN] repository policy unresolved ...` (CLI output, MCP report, LSP `window/logMessage`) |

The lock-less row differs from `praetorctl plan` on purpose. The plan preview
shows built-in defaults plus overrides (cyclomatic 15, cognitive 20, 60 lines,
75 statements before overrides); an editor told the looser cyclomatic, cognitive
and statement limits would accept what the HISS-04 ceiling rejects. Function
length agrees in both: it is `hiss.DefaultMaxFuncLOC` (`internal/hiss/hiss.go`),
the one constant every function-length default derives from. An unresolvable
policy warns rather than failing, because these commands generated editor files
before they read policy at all; `praetorctl audit` still fails on that state.
Only a nil or cancelled context fails the resolution: `editors` and the MCP
inspection return the error, and `standards-lsp` keeps its current ceiling and
logs the reason. Tests:
`internal/config/repository_policy_test.go`,
`cmd/standardsctl/editors_test.go`, `cmd/standards-lsp/policy_test.go`,
`cmd/standards-mcp/server_test.go`.

## Existing configuration

Generation preflights configuration conflicts before writing editor files. JSON
configuration retains unrelated user keys and list entries while adding missing
managed requirements. A conflicting managed value is reported for review.
Verification checks those requirements rather than requiring every JSON byte to
match a generated template.

Known legacy Go, unavailable LSP, unpublished extension and invented build-task
settings are reported as conflicts when they are no longer supported by the
resolved plan. They are retained for review; this release does not automatically
migrate legacy generated files. Resolve each reported conflict using the actual
repository's languages and commands, then generate and verify again.

Non-JSON formats do not yet have semantic merge adapters. Existing files are
preserved and checked for known unsupported legacy settings; their existence is
not full semantic verification of XML, TOML, Lua or editor Lisp. Missing files can
still be generated from the resolved plan.

Adoption retains its existing explicit `--force` contract. A successful workspace
configuration check does not imply that an IDE extension, coding-agent wrapper,
hook or MCP connection is installed and active. Those require the separate
[client bootstrap](client-bootstrap.md) and
[agent lifecycle](agent-lifecycle.md) checks.
