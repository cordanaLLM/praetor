# Documentation drift

A change that alters a user-discoverable surface ships the documentation for it in the same change.
`praetorctl docs references --base=<rev>` enforces that on every pull request, in Praetor and in
any repository that declares its surfaces in `.standards.yaml`.

The opposite direction has its own gate: a guide that names a command, flag or file that no longer
exists fails `make docs-references`, whether or not the change touched the guide. See
[References that stop resolving](#references-that-stop-resolving).

## Why this exists

Praetor already keeps two kinds of text in sync and neither one is this.

`compile-context --verify` proves the *agent* instructions match the canonical `AGENTS.md` across
thirty projections. `praetorctl docs sync` harvests *dependency* documentation from upstream
packages. Nothing checked whether a repository's own `docs/` still describes what its code does,
so documentation drifted and no mechanism reported it. An adopter could only state the rule in its
`AGENTS.md` and have reviewers enforce it from memory (#608).

The design is backported from a downstream adopter's blocking gate.

## Declaring surfaces

A repository lists its surfaces under `docs_surfaces` in `.standards.yaml`, one entry per surface:

```yaml
docs_surfaces:
  - name: "build script"
    paths:
      - "scripts/build.sh"
    docs:
      - "docs/onboarding.md"
  - name: "release workflow"
    paths:
      - ".github/workflows/release.yml"
```

| Key | Meaning |
| --- | --- |
| `name` | Printed in every finding and waived line: required, unique, one line, at most 128 bytes. |
| `paths` | Globs that select the surface's files. Required. |
| `exclude` | Globs removed from `paths`, such as the surface's own tests. |
| `docs` | Globs of the documents that describe the surface. Without it the surface is *unmapped*. |

A glob is repository-relative: `*` matches inside one directory and a `**` segment spans any
number of them. The manifest loader refuses an absolute, negated or wildcard-only glob, an empty,
`.` or `..` segment, more than 128 surfaces and more than 32 globs in one list
(`internal/config/docs_surfaces.go`).

Praetor's own list is the `docs_surfaces` block of this repository's `.standards.yaml`. It holds
only what an adopter reads about before using it: the archetype and facet catalogs, the policy
resolvers, the git and agent hooks, the development MCP server, and the gates and ledgers each
guide describes. Everything else is internal. A refactor that changes no listed surface is never
accused, and that is what keeps the gate worth reading: one that fires on every change is one
people learn to ignore, and then it protects nothing.

## What a change must carry

For every declared surface a changed path belongs to, the check looks for a changed path that
matches one of the surface's `docs` globs:

- **Found**: the surface passes and is printed as `documented <name>: <document>`.
- **Not found**: a finding names the surface, the changed paths and the `docs` globs mapped to it.
- **Unmapped**: a surface without `docs` never passes silently; a change to it is reported as
  unmapped until its entry names its documents or the change carries a waiver.

A change that touches no surface passes, a documentation-only change included. A file of the
surface itself never counts as its documentation.

## When documentation genuinely is not needed

State the reason in one of two places:

- a `Docs-Waiver: <reason>` trailer line in any commit of the checked range;
- a `no docs needed: <reason>` line in the pull request body, which CI passes with
  `--pr-body-file`.

A waiver without a reason waives nothing. A waiver admits every finding of the change, and the
output prints each waiver with its source and each finding it admitted, so the reviewer sees what
was waived and why.

Use it for a pure internal refactor, a bug fix with no user-visible delta, or a test-only change
that happens to touch a mapped file.

## An ADR does not satisfy the gate

Edits under `docs/adr/` are explicitly excluded, and a `docs` glob that selects only decision
records is refused as selecting nothing. An ADR records a *decision*; a guide describes a
*surface*. Accepting an ADR as documentation coverage would let "I wrote it down somewhere" stand
in for "the guide still matches the behaviour", which is the drift this exists to catch.

## Running it

```bash
go run ./cmd/standardsctl docs references --path=. --base=origin/main
go test ./internal/docsref/ ./internal/config/ ./cmd/standardsctl/ -run 'Drift|Surfaces|Waivers|DocsReferences'
```

Without `--base` the command checks the declaration only: every `paths` glob must select a file of
the repository and every `docs` glob a document, because a surface mapped to a missing document
could never be satisfied and would fail every change to it with no way to clear it. That part runs
in `make docs-references`, inside `make verify-all`, and on every leg of
`.github/workflows/portability.yml`. The change check runs as the pull-request step "Documentation
Drift" in `.github/workflows/ci.yml`, with the pull request's base and head commits and its body.

The changed paths come from `git diff --name-only <base>...<head>` through
`cifilter.GetChangedFiles` (the diff against the merge base; when that fails or lists nothing,
the diff between the two commits), capped at 5,000 paths; the waiver trailers come from the commits of
`<base>..<head>`, capped at 1,000. A failed git command stops the check with an error; it never
becomes an empty, passing diff. The tests replay each case:

- `internal/docsref/drift_test.go`: documented, undocumented, unmapped, waived, excluded and
  decision-record changes, the path cap and one path over it, and the declaration check.
- `internal/docsref/drift_praetor_test.go`: this repository's own surfaces.
- `cmd/standardsctl/docs_references_test.go`: an adopter checkout end to end, waivers included.
- `internal/config/docs_surfaces_test.go`: the manifest schema and its bounds.

## In an adopted repository

Declare the surfaces as above, then run the check in the pull-request workflow:

```bash
praetorctl docs references --path=. --base="$BASE_SHA" --head="$HEAD_SHA" --pr-body-file=body.txt
```

In a checkout without Praetor's CLI source (`cmd/standardsctl`) the command skips the command,
flag and path reference check described below and says so. A checkout that is neither Praetor's
nor declares a surface is refused: a gate with nothing to check is not a passing gate.

Adoption does not yet write this step into a workflow or hook; add it to the pull-request workflow
by hand.

## Extending the map

Add an entry to `docs_surfaces` in `.standards.yaml`. Keep additions narrow. The cost of a wrong
entry is not a missed document — it is an accusation nobody can act on, which teaches people to
reach for the waiver.

### Known limits of the change check

- A renamed file is listed under its new path only, so renaming a file out of a surface is not a
  change to that surface.
- Under Git's default `core.quotePath`, `git diff` quotes a path with bytes outside ASCII, and a
  quoted path matches no glob.
- A waiver admits the whole change, not one surface of it.

## References that stop resolving

The change check only examines a change that touches a declared surface. A guide can go stale
without any such change: a command is renamed, a flag is removed, a file moves, and every guide
that named it still reads as correct in review because none of them is in the diff (BUG-992).

`praetorctl docs references` reads `README.md` and every Markdown file under `docs/` and checks
each reference a reader would copy:

- **Commands.** Every call of `praetorctl` or `standardsctl` (bare, by path such as
  `./bin/praetorctl`, or as `go run ./cmd/standardsctl`) in an inline code span or a shell fence.
  In a script fence (`bash`, `sh`, `shell`, `zsh`, `ksh`, `csh`, `tcsh`, `fish`, `powershell`,
  `pwsh`, `ps1` and the other names of the table) every line that is not a `#` comment is a
  command, and in a Windows batch fence (`cmd`, `bat`, `batch`) every line that is not a `REM`
  or `::` comment is. In a terminal transcript (`console`, `shell-session`, `terminal`) only a
  line that starts with the `$` prompt and a space is; the lines between prompts are output,
  such as `praetorctl version dev`, and are not read. A fence without a language is not read.
  The first word must be a command of the binary's own dispatch table, and each subcommand
  word and flag after it must be one its code defines. The fence table, the per-line command
  rule and the CommonMark code span reader live in `internal/util/markdown_syntax.go`
  (`util.MarkdownShellFence`, `util.MarkdownShellCommand`, `util.MarkdownCodeSpans`); the
  caveman clarity floor reads commands and code spans with the same functions. The two
  differ on one point: the floor holds the lines of a fence without a language as commands
  (`F2`), since nothing says they are not, while this check reads none of them.
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
| Fences in other languages (`yaml`, `json`, `text`, `mermaid`) and fences without a language | they quote data or program output, where a word after `praetorctl` is not a call |
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

`docs references` takes no positional argument. It parses argv like every other command, so a
stray argument is refused by name wherever it stands among the flags, and a `--` ends the flags.

It runs inside `make verify-all`, as the light documentation step of `.github/workflows/ci.yml`,
and on every leg of `.github/workflows/portability.yml`. It lists the tree through git, so a file
that exists only on one machine never satisfies it there and fails it in CI. The fixture corpus
replays both directions: `internal/docsref/testdata/repo/docs/pass.md` must produce no finding,
and every defect planted in `internal/docsref/testdata/repo/docs/fail.md` must produce exactly
the finding listed in `internal/docsref/testdata/repo/expected-findings.txt`.
