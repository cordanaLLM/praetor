# Repository-aware editor generation

`praetorctl editors generate --path PATH` and `praetorctl editors verify --path PATH`
resolve the same repository capabilities before generating or checking editor
files. A repository without Go sources or a selected Go profile no longer receives
unconditional Go settings, Go inspections or a Go problem matcher.

## Selecting editors

Both subcommands take `--editors=id[,id...]`, a comma-separated list of editor ids
or aliases (`cmd/standardsctl/editors.go`). Omitting the flag, or passing an empty
or all-separator value, targets every editor in `editor.DefaultOptions()`. Any id
that does not resolve through `editor.Options.Editors` (`internal/editor/editor.go`)
fails the whole run with `unknown editor id(s): <ids>; supported: <canonical ids>`
and writes nothing: a partially-matched `--editors` list used to synthesize only
the recognized subset and silently drop the rest.

## Antigravity IDE

`antigravity` (aliases `agy`, `antigravity-ide`) joins the VS Code-compatible
family alongside `vscode`, `cursor` and `windsurf`, and shares their single
`.vscode/settings.json`. Requesting it adds two keys to that file, confirmed
against the installed IDE rather than assumed from its docs:

- `antigravity.searchMaxWorkspaceFileCount: 50000` — raises Jetski's per-workspace
  embedding scan bound above its shipped default of 5,000 (confirmed:
  `contributes.configuration` in the IDE's `extensions/antigravity/package.json`).
- `files.watcherExclude` — a core VS Code setting (confirmed: the workbench's own
  configuration schema) extended with `.workingdir*`, build output (`bin`, `dist`)
  and the isolated gate/dogfood run worktrees under `.standards/worktrees`, to
  keep inotify-heavy scratch out of the file watcher.

These two keys are only added when `antigravity` is part of the requested editor
set; `vscode`/`cursor`/`windsurf`-only runs do not receive them. Run-count
retention for `~/.local/state/praetor/dogfood-local` is a separate, still-open
fix tracked on the issue that requested this key.

The shared resolver combines supported project markers and source formats with
explicit caller options. It excludes private state, dependency/build directories
and nested agent worktrees. An incomplete scan is an error rather than a claim
that the unexamined part of the repository has no relevant languages. The initial
scan limit is 4096 files.

Default commands include `make verify-all` only when that literal target exists.
The generator does not add `make build` merely because a Makefile exists. Target
presence is structural evidence; generation does not run or certify the command.
Callers using the Go API can provide explicit commands and languages through
`editor.Options`.

Praetor LSP settings require an explicitly selected, existing regular executable
and a supported language. Extension recommendations require caller-supplied
registry evidence through `editor.Options`; the generator does not contact an
extension marketplace or prove publication from an extension's source directory.
These settings do not prove installation, startup or native client activation.

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
