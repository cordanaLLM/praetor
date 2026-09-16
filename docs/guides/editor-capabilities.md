# Repository-aware editor generation

`praetorctl editors generate --path PATH` and `praetorctl editors verify --path PATH`
resolve the same repository capabilities before generating or checking editor
files. A repository without Go sources or a selected Go profile no longer receives
unconditional Go settings, Go inspections or a Go problem matcher.

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
