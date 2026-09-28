# Repository-aware editor generation

`praetorctl editors generate --path PATH` and `praetorctl editors verify --path PATH`
resolve the same repository capabilities before generating or checking editor
files. A repository without Go sources or a selected Go profile receives no Go
settings, Go inspections or Go problem matcher.

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
explicit caller options. It excludes private state, dependency/build directories,
nested agent worktrees, and the trees Zig writes inside a checkout: `zig-pkg`
(fetched packages), `zig-out` and `.zig-cache` (`util.IsToolchainTreeDir`). A
native build marker adds `c` and `cpp` for `meson.build` or `CMakeLists.txt` and
`zig` for `build.zig` or `build.zig.zon` (`needs.NativeLanguages`), so a pure-Zig
repository gets no C/C++ tooling (`internal/editor/native_languages_test.go`).
`cuda` comes only from `.cu` and `.cuh` sources. An incomplete scan is an error rather than a claim
that the unexamined part of the repository has no relevant languages. The scan
walks at most 4096 files by default (`Options.MaxWorkspaceFiles`, zero selects the
default). `praetorctl adopt` passes the entry bound its verification walk resolved,
so `--verification-max-entries` raises this scan too, up to the same 200000 ceiling.
A bound past the ceiling is refused, and adoption names the flag when the scan
stops at the bound ([large repositories](../adoption.md#large-repositories);
`internal/editor/workspace_bound_test.go`,
`internal/adopt/large_repo_bounds_test.go`). `praetorctl editors` keeps the default.

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
- Language server. The Praetor LSP is written only for a Go workspace where the
  host can start the workspace-relative command `editor.Options.LSPPath`, or,
  when that is unset, `<BinaryDir>/standards-lsp`. Otherwise VS Code settings
  carry no `standards.lsp.*` key and Neovim registers no server. The settings
  name the command, never the file it resolves to, so one tracked
  `.vscode/settings.json` verifies on every host. On Linux and macOS the command
  is an executable regular file. On Windows the VS Code client spawns it without
  a shell through libuv, whose `search_path` (`src/win/process.c`) appends `.com`
  and then `.exe` to a name without an extension: a built
  `bin/standards-lsp.exe` starts as `bin/standards-lsp`. Node rejects `.bat`
  and `.cmd` files in that spawn, so they never count as a language server.
  Tests: `internal/editor/lsp_launch_test.go` and
  `cmd/standardsctl/editors_self_gate_test.go`.
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
`praetorctl audit` enforces there. The JetBrains profile carries the cyclomatic
limit (`GoCyclomaticComplexity`'s `m_limit`) and otherwise names only inspections
the IDE provides; it no longer lists `HISS01DAGControlFlow`, `HISS02BoundedLoops`,
`HISS04ComplexityLOC` or `HISS07ZeroUnwrap`, which no plugin implements, so their
function-length and statement options were never applied. Those limits reach an
editor through `standards-lsp`. `praetorctl adopt` uses the policy its
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

## Reference integrations under `editors/`

`editors/neovim/lua/standards.lua` and
`editors/jetbrains/inspectionProfiles/standards.xml` are the Neovim module and
JetBrains inspection profile for editors without a Praetor extension. They are
generator output: `editor.ReferenceSet` (`internal/editor/reference.go`) renders
them with the same renderers as `editors generate`, from a fixed plan (a Go
workspace, `praetorctl` on `PATH`, `bin/standards-lsp`, the HISS-04 ceiling), so
they do not depend on what the host has built. The Neovim module defines
`:StandardsAudit`, `:StandardsCompileContext`, `:StandardsVerifyAll` and
`:StandardsRatchetSweep`; the ratchet sweep runs `praetorctl audit`, which
evaluates the baseline ratchet against the working tree
(`cmd/standardsctl/audit_ratchet.go`).

Never edit them by hand. After changing a renderer, regenerate and check:

```bash
make editors-reference          # praetorctl editors reference
make editors-reference-verify   # praetorctl editors reference --verify
```

`make verify-all` runs `editors-reference-verify`, which fails with the drifted
file's path. Tests: `internal/editor/reference_test.go`,
`cmd/standardsctl/editors_reference_test.go`.

## The engine's own editor files

The engine checks its own editor files the way an adopter's are checked. Its
`.standards.yaml` selects `editors: [universal, vscode]`, and `make verify-all`
runs `make editors-verify` (`praetorctl editors verify`) against that selection:

```bash
make editors-verify   # praetorctl editors verify
```

The gate fails when a selected file is missing or has lost a managed value. The
tracked `.editorconfig` adds two-space sections for the TypeScript, Lua, shell and
web sources, so the gate reports it as preserved but unverified. Neovim is not
selected: its module is compared byte for byte, and it holds a language-server
block only when `bin/standards-lsp` is built, so a fresh checkout and a built one
would disagree. Tests: `cmd/standardsctl/editors_self_gate_test.go`, which the
platform matrix runs on Linux, macOS and Windows.

## VS Code extension trace level

The extension starts its language client with the id `standards.lsp`
(`LSP_CLIENT_ID` in `editors/vscode/src/setup.ts`). vscode-languageclient reads
the trace level from `<client id>.trace.server` and re-reads it on every
configuration change, so the contributed `standards.lsp.trace.server` setting
(`off`, `messages`, `verbose`) now takes effect. The former id `standardsLSP`
read a key nothing contributes. Test: `editors/vscode/src/setup.test.ts`.

## VS Code extension MCP server

Generated `.vscode/settings.json` files carry no `standards.mcp.*` key and no
MCP server entry. The extension registers `standards-mcp` itself through the MCP
server definition provider API, defaulting to `${workspaceFolder}/bin/standards-mcp`
in a trusted workspace; its engine floor is VS Code 1.107. The generated
`standards.sentinel.headroomMB` value is the headroom the extension's
**Check Sentinel Host Headroom** command passes to
`praetorctl sentinel --min-free-mb`. Details and tests:
`editors/vscode/README.md`, sections "MCP server" and "Settings".

## Existing configuration

Generation preflights configuration conflicts before writing editor files. JSON
configuration retains unrelated user keys and list entries while adding missing
managed requirements. A conflicting managed value is reported for review.
Verification checks those requirements rather than requiring every JSON byte to
match a generated template.

Known legacy Go, unavailable LSP, unpublished extension and invented build-task
settings are reported as conflicts when they are no longer supported by the
resolved plan. They are retained for review; automatic migration of legacy
generated files is not supported. Resolve each reported conflict using the actual
repository's languages and commands, then generate and verify again.

Non-JSON formats do not yet have semantic merge adapters. Once they exist, the
developer-owned files are never replaced: `.editorconfig` and `.clang-tidy` carry
hand-tuned project policy, and `.idea/workspace.xml`, `.nvim.lua` and
`.dir-locals.el` hold IDE session state or a developer's own editor setup.
Generation reports each of them as `PRESERVED`, and verification lists them as
preserved but unverified, because their existence is not semantic verification of
XML, Lua or editor Lisp. Any other existing non-JSON file that differs from its
template, such as `lua/standards.lua` or the JetBrains inspection profile, belongs
to Praetor and `editors generate` rewrites it. A file whose only difference is one
consistent CRLF line-ending style, as a Windows checkout with `core.autocrlf` leaves
it, is the template: generation reports it `PRESENT` and verification passes it,
while mixed line endings still count as a difference. Missing files are still
generated from the resolved plan. The list lives in one place,
[`internal/editor/editor.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/editor/editor.go)
(`IsPreservedEditorFile`).

A successful workspace configuration check does not imply that an IDE extension,
coding-agent wrapper, hook or MCP connection is installed and active. Those require
the separate [client bootstrap](client-bootstrap.md) and
[agent lifecycle](agent-lifecycle.md) checks.

### Adoption and onboarding

`praetorctl adopt` resolves an existing editor file with the rule `editors generate`
uses, `editor.ResolveExisting` in `internal/editor/editor.go`, with one difference: it
never overwrites an editor file, because no audit gate verifies one (#502). The
adoption side is `internal/adopt/editor_files.go`. The editors step runs after the
Makefile step (`adoptSteps` in `internal/adopt/adopt.go`), so a first adoption's
editor files already offer the `verify-all` target it scaffolds, and a re-run finds
them unchanged:

| Existing file | Plain run | `--force` |
| :--- | :--- | :--- |
| identical (CRLF line endings included), or JSON holding every managed value | verified | verified |
| developer-owned and different | preserved | preserved |
| JSON missing managed values | kept; the warning names each one as a JSON Pointer, such as `"/standards.sentinel.headroomMB"` | merged: every adopter key and list entry stays, the managed values are added, and the report lists a `merge` |
| JSON adoption cannot merge: JSONC comments or a trailing comma, a duplicate key, a conflicting managed value | kept, with a warning naming the reason and the fix | kept, with a warning; adoption continues |
| any other file that differs, such as `lua/standards.lua` | kept, with a warning and the line delta regenerating would apply | kept, with a warning |

A merge re-indents the file with two spaces and sorts its keys. Its report entry
carries the line delta and the backup location that every adoption overwrite records
([replaced files](../adoption.md)); a dry run plans the merge and writes nothing.

How a kept file stops warning depends on what it is:

- **Non-JSON file that differs.** Delete it and re-run adoption, which regenerates it
  from the template for the repository's adopted profile. `praetorctl editors generate`
  is no substitute here: it renders the framework profile whatever profile adoption
  selected, so for another profile it rewrites files adoption had verified.
- **JSONC comments, a trailing comma or a duplicate key.** Adoption and
  `praetorctl editors verify` read strict JSON only, so such a file never verifies,
  even when it holds every managed value. `praetorctl editors generate` refuses it
  too: it stops with `cannot safely merge existing <path>` before it writes any file.
  Remove the comments, trailing commas and duplicate keys, then re-run adoption with
  `--force` to merge the managed values; or delete the file and re-run adoption to
  regenerate it.
- **A conflicting managed value.** `praetorctl editors generate` refuses it the same
  way. Set the value the warning names to the managed one, then re-run adoption with
  `--force`; or delete the file and re-run adoption.

Onboarding
(`internal/harvester/onboard.go`) skips every existing developer-owned file through
the same `IsPreservedEditorFile`.

Tests: `internal/adopt/editor_files_test.go`,
`internal/editor/resolve_existing_test.go` and
`TestOnboardRepository_Boundary_KeepsDeveloperOwnedNvimLua` in
`internal/harvester/onboard_selection_test.go`.
