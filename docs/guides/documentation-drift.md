# Documentation drift

A change that alters a user-discoverable surface ships the documentation for it in the same change.
`scripts/docs_drift.py` enforces that on every pull request.

The opposite direction has its own gate: a guide that names a command, flag or file that no longer
exists fails `make docs-references`, whether or not the change touched the guide. See
[References that stop resolving](#references-that-stop-resolving).

## Why this exists

Praetor already keeps two kinds of text in sync and neither one is this.

`compile-context --verify` proves the *agent* instructions match the canonical `AGENTS.md` across
thirty projections. `praetorctl docs sync` harvests *dependency* documentation from upstream
packages. Nothing checked whether this repository's own `docs/` still describes what its code does,
so documentation drifted and no mechanism reported it.

The design is backported from a downstream adopter's blocking gate.

## What counts as a surface

Only what an adopter reads about before using it. The map in `SURFACE_MAP` is deliberately narrow:

| surface | documentation |
| --- | --- |
| `.config/archetypes/*.yaml`, `facets/*.yaml` | `docs/guides/archetype-authoring.md` |
| `internal/flavor/definitions.go` | `docs/guides/archetype-authoring.md` or `onboarding.md` |
| `internal/config/effective.go`, `effective_load.go` | `docs/guides/effective-policy.md` |
| `internal/config/register.go`, `register_render.go` | `docs/guides/text-register.md` |
| `internal/hiss/rules.go`, `internal/hiss/go_callgraph.go` | `docs/standards/` |
| `internal/hisscoverage/` | `docs/guides/` or `docs/standards/` |
| `.config/lefthook/scripts/*.py`, `lefthook.yml` | `docs/guides/git-hooks.md` |
| `cmd/standards-mcp/` | `docs/guides/development-mcp.md` |
| `internal/gating/pipeline.go` | `docs/guides/adoption-verification.md` |
| `internal/readmegovernance/`, the adoption README renderer, and its audit gate | `docs/guides/adoption-verification.md` |
| `internal/wishes/`, `internal/state/` | their respective guides |
| `internal/agenthook/*.go` (tests excluded), `cmd/standardsctl/hook.go` | `docs/guides/agent-hooks.md` |
| `.github/workflows/portability.yml`, `scripts/portability_selftest.py` | `docs/standards/hiss-21-platform-neutrality.md` |
| `internal/workstation/`, `cmd/standardsctl/workstation.go`, `scripts/dev_install.py` | `docs/guides/workstation-update.md` |
| `tools/markdownlint/`, `tools/docsurface/`, the adoption emitter, CI selector, and dedicated workflow | `docs/guides/documentation-governance.md` |
| `internal/docsref/`, `cmd/standardsctl/docs_references.go` | this document |
| `scripts/docs_drift.py` | this document |

Everything else is internal. A refactor that changes no listed surface is never accused, and that
is what keeps the gate worth reading: one that fires on every change is one people learn to ignore,
and then it protects nothing.

## When documentation genuinely is not needed

Write `no docs needed: <reason>` in the pull request body. The reason is for the reviewer, not the
script — the script only looks for the phrase.

Use it for a pure internal refactor, a bug fix with no user-visible delta, or a test-only change
that happens to touch a mapped file.

## An ADR does not satisfy the gate

Edits under `docs/adr/` are explicitly excluded. An ADR records a *decision*; a guide describes a
*surface*. Accepting an ADR as documentation coverage would let "I wrote it down somewhere" stand in
for "the guide still matches the behaviour", which is the drift this exists to catch.

## Running it

```bash
make docs-drift-test          # the check's own tests
BASE_SHA=origin/main HEAD_SHA=HEAD python3 scripts/docs_drift.py
```

It runs inside `make verify-all` and as a pull-request step in `.github/workflows/ci.yml`.
The changed-path query uses the shared bounded hook runner (`run_bounded` in
`.config/lefthook/scripts/common.py`): `git diff` has a 10-second deadline, combined standard
output and error are capped at 1 MiB, and the complete NUL-delimited inventory is capped at 5,000
paths. Timeout, process-start, non-zero-exit, malformed-output, byte-limit, and path-limit failures
stop the gate with exit status 2 as infrastructure errors; none can become an empty passing diff.
`scripts/test_docs_drift.py` replays each failure, the exact path cap, and one path over it.

## Extending the map

Add a row to `SURFACE_MAP` in `scripts/docs_drift.py`. A test asserts that every mapped document
exists: a surface pointing at a missing file could never be satisfied, so it would fail every change
to that surface with no way to clear it.

Keep additions narrow. The cost of a wrong row is not a missed document — it is an accusation
nobody can act on, which teaches people to reach for the opt-out.

## References that stop resolving

`scripts/docs_drift.py` only examines a change that touches a mapped surface. A guide can go stale
without any such change: a command is renamed, a flag is removed, a file moves, and every guide
that named it still reads as correct in review because none of them is in the diff (BUG-992).

`praetorctl docs references` reads `README.md` and every Markdown file under `docs/` and checks
each reference a reader would copy:

- **Commands.** Every call of `praetorctl` or `standardsctl` (bare, by path such as
  `./bin/praetorctl`, or as `go run ./cmd/standardsctl`) in an inline code span or a shell fence.
  In a script fence (`bash`, `sh`, `shell`, `zsh`, `fish`, `powershell`) every line that is not
  a comment is a command. In a terminal transcript (`console`, `shell-session`, `terminal`) only
  a line that starts with the `$` prompt and a space is; the lines between prompts are output, such as
  `praetorctl version dev`, and are not read. The first word must be a command of the binary's
  own dispatch table, and each subcommand word and flag after it must be one its code defines.
- **Repository paths.** Every word with a slash whose first element is a top-level entry of the
  repository, such as `internal/forge/pr.go:37`, `deploy/helm/` or `internal/state.VerifyStateSync`.
  It must exist in `git ls-files`; a package path followed by an identifier, exported or not,
  must declare it. A trailing `# comment` on a shell line is not read.

### Where the command vocabulary comes from

Nothing is listed by hand. The top-level commands come from `commandTable()` in
`cmd/standardsctl/main.go`, the table the binary dispatches on. Below that, `internal/docsref`
reads each handler's source for how it dispatches on the head of its argument list: a `switch`
on `args[0]` or on a variable taken from it, an `if args[0] == "run"` chain, a lookup in a map
such as `stateCommands`, or a handler that only forwards the list. Each subcommand leads to the
function that handles it, and the walk goes on from there, so `state task add` is three levels
deep while `hook` has none.

- **Subcommand words** are checked only as deep as that tree goes. A `state` call with the
  word `purge` fails, because `state` dispatches and has no `purge`. Below a leaf every word is an operand
  and passes: `praetorctl docs lookup cobra`, `praetorctl state task add fix-build`,
  `praetorctl agent run my-agent`, `praetorctl hook claude pre-tool`.
- **Flags** come only from real definitions: a registration through Go's `flag` package
  (`fs.Bool("verify", …)`, `fs.StringVar(…)`) or a flag-shaped literal the code compares an
  argument against by hand (`arg == "--json"`). They are collected per path: the flags of each
  command on the path, without the code of subcommands the call did not choose or of any other
  top-level command. A git argument such as `--porcelain` is not a definition, so `--porcelain`
  on `state sync` fails, and `--path=.` on `docs lookup` fails because `--path` belongs to its
  sibling `docs references`.
- **Operands** are never checked: a placeholder (`<file>`, `[path]`, `PATH`), a path, a quoted
  value, the value after a flag that takes one, and every word below a leaf. Alternatives are
  checked one by one: `praetorctl state task <add|list>` passes and `<add|rm>` fails.

### What is not checked, and why

| Skipped | Reason |
| --- | --- |
| `docs/project-records/` | records past events as they were written; never updated to match later code |
| Accepted, Superseded and Deprecated decision records | the body is immutable ([ADR lifecycle](../adr/README.md)); a changed decision gets a new record |
| Proposed and Draft decision records | they name surfaces that do not exist until they are implemented |
| Fences in other languages (`yaml`, `json`, `text`, `mermaid`) | they quote data or program output, where a word after `praetorctl` is not a call |
| A mention outside command position | `this praetorctl serves no row` is a sentence, not a call |

A record with no readable `## Status` is checked, so an unrecognised status cannot switch the
check off. Every skipped document is printed with its reason.

### Paths that exist outside the public tree

Three kinds of absent path are legitimate, and the code states why for each:

- **Operator-owned paths.** The owner-only prefixes of `internal/operationalsync`
  (`deploy/arc/`, `deploy/k8s/`, `.config/operator/` and the rest listed in
  [operational configuration](operational-configuration.md#what-the-operator-owns)) hold operator
  data an operational fork supplies. A guide describing them names real operator data.
- **Ignored paths.** Anything the repository's own `.gitignore` hides, such as `bin/praetorctl`
  or `.workingdir/OPEN.md`, is a local artefact a checkout creates. The operator's global
  excludes are not consulted, so the answer is the same on every machine.
- **Paths the engine names.** A path spelled as a string literal in the module's non-test Go
  source is part of what the engine does: a file adoption writes into an adopted repository
  (`docs/adr/0000-template.md`) or an MCP method shaped like a path (`tools/list`).

Anything else absent is a finding: another repository's file, an adopter's file the engine only
assembles at run time, or an illustrative example. Link the external file by URL, write the
example with a placeholder (`<chart>/templates/service.yaml`), or, when neither fits, fence the
block with a reasoned suppression:

```markdown
<!-- praetor:docs-references:off the Does not match column lists illustrative paths -->

| Prefix | Does not match |
| --- | --- |
| `deploy/k8s/` | `deploy/k8sx/a.yaml` |

<!-- praetor:docs-references:on -->
```

The reason is mandatory and must be at least three words, so `off x` does not pass. A marker
without one, an `on` with no open `off`, a repeated `off` and an `off` never closed are findings
themselves. Every accepted block is counted and printed with its reason, next to the skipped
documents, so a reviewer sees what the run did not read.

### Known limits

- A path whose first element is no longer a top-level entry of the repository (a whole
  top-level directory renamed or removed) is not recognised as a repository path, so it is not
  checked.
- Below a leaf the check cannot tell an operand from a stale subcommand. A command that checks a
  word inline after the fact (`operational sync <stage>`) is a leaf here.
- A path the engine assembles at run time instead of spelling it out needs a reference that
  exists, or a reasoned off block.
- The command list comes from the binary that runs the check and the subcommands and flags from
  the source at `--path`. `make docs-references` builds both from the same checkout; running an
  installed binary against another checkout can mix them.
- On a fork where `PRAETOR_FORK_PORTABILITY` is not enabled, a light pull request that changes
  neither documentation nor code runs no leg of the check; heavy runs and pushes reach it
  through `make verify-all`.

### Running it

```bash
make docs-references                              # the gate
go run ./cmd/standardsctl docs references --path=.
go test ./internal/docsref/                       # its tests and fixture corpus
```

It runs inside `make verify-all`, as the light documentation step of `.github/workflows/ci.yml`,
and on every leg of `.github/workflows/portability.yml`. It lists the tree through git, so a file
that exists only on one machine never satisfies it there and fails it in CI. The fixture corpus
replays both directions: `internal/docsref/testdata/repo/docs/pass.md` must produce no finding,
and every defect planted in `internal/docsref/testdata/repo/docs/fail.md` must produce exactly
the finding listed in `internal/docsref/testdata/repo/expected-findings.txt`.
