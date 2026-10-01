# Generated artefacts

A generated artefact is a file the repository commits although other files determine it: the
compiled context projections, the debt baseline, the README governance block, a changelog rendered
from fragments. When every pull request has to carry such files fresh, any two pull requests
conflict in them, even when their sources do not. Praetor therefore declares the generated
artefacts, keeps them out of ordinary pull requests and regenerates them in one change per batch.
[ADR-0017](../adr/0017-generated-artefacts-in-pull-requests.md) records the decision.

## Quick start

```bash
# what is declared, as text or as JSON for a merge train or an agent
praetorctl ci generated list
praetorctl ci generated list --json

# in a pull request: no edit of a declared artefact, and every artefact still renders
praetorctl ci generated check --base=origin/main --branch=feat/my-change --title="feat: my change"

# on the default branch: fail when an artefact is stale, or write the fresh renderings
praetorctl ci generated render --check
praetorctl ci generated render
```

Each command takes `--dir` (default `.`), the checkout to work on, and `--json`. Phase 1 adds the commands only: no hook or workflow calls them yet, and the
existing freshness gates keep running until phase 2 wires them to the pull-request mode.

## The three rules

1. **A pull request does not edit a declared artefact.** `check` fails a change that edits a file of
   a declared artefact, or the text inside a declared block, from the merge base to the head.
2. **A pull request proves that every artefact still renders.** `check` runs each artefact's
   command in a temporary worktree of the head. A command that fails, or a rendering that selects no
   file, fails the pull request. Whether a committed file equals its rendering is reported (as
   "differs from its rendering") but not enforced here.
3. **The default branch is kept fresh by one regeneration change per batch.** `render --check`
   fails while an artefact differs from its rendering; `render` writes the renderings for the
   regeneration change, the only change allowed to touch declared artefacts.

## Built-in artefacts

Praetor declares its own artefacts in `internal/generated/builtin.go`. Each applies only where its
files exist, so a repository without a needs manifest has no "needs manifest" artefact.
`ci generated list` shows every built-in artefact and, for one that does not apply, why.

| Name | Paths | Render command | Applies when |
| --- | --- | --- | --- |
| compiled context projections | the vendor files and persona copies `agent_clients` selects, and the plugin copies | `praetorctl compile-context` | `AGENTS.md` exists and a projection does |
| agent register block | the register block of `AGENTS.md` | `praetorctl compile-context` | `AGENTS.md` carries the block |
| debt baseline | `.standards-baseline.json` | `praetorctl baseline --record` | the baseline exists |
| README governance block | the governance block of `README.md` | `praetorctl docs readme` | `README.md` carries the block |
| needs manifest | `.needs.yaml` | `praetorctl needs scan --write --manifest=`, with no operator settings | the manifest exists |
| documentation figures | the `.json` and `.svg` renderings in `docs/assets/figures/` | `node tools/figures/build.mjs build` | `tools/figures/build.mjs` exists |
| shipped-text ledger | the ledgers in `internal/managedasset/testdata/shipped/` | `go test` of `TestShippedTextLedger` with `PRAETOR_UPDATE_SHIPPED_TEXTS=1` | a Praetor source checkout |
| devcontainer bundle | the files `devcontainer generate` writes to `.devcontainer/` | `praetorctl devcontainer generate --source-root=. --force` | a Praetor source checkout with `.devcontainer/Dockerfile.praetor` |

They render in this order, so the README block reads the baseline the row above it has just
recorded. A
command whose first word is `praetorctl` runs the running binary itself (`SelfCommand`), so a
built-in artefact renders through the generator's own command and with the same build that checks
it. `praetorctl docs readme [path] [--check]` exists for this list: it renders the README block from
the state `praetorctl audit` verifies it against (`readmeGovernanceState` in
`cmd/standardsctl/audit_readme.go`), where only a whole `praetorctl adopt` run refreshed it before.

## Declaring your own artefacts

Add entries under `generated` in `.standards.yaml`; the manifest loader reads and validates them
(`internal/config/generated.go`):

<!-- praetor:docs-references:off an adopter's own files and scripts, not paths of this repository -->

```yaml
generated:
  regeneration:
    branch_prefix: "regen/"
    title_type: "chore(generated)"
  decline:
    - "documentation figures"
  artefacts:
    - name: "changelog"
      paths:
        - "CHANGELOG.md"
      command: ["python3", "scripts/render_changelog.py"]
      sources:
        - "changelog.d/*.yaml"
    - name: "ADR index"
      paths:
        - "docs/adr/README.md"
      block:
        start: "<!-- adr-index:start -->"
        end: "<!-- adr-index:end -->"
      command: ["make", "adr-index"]
      sources:
        - "docs/adr/[0-9]*.md"
      env:
        TZ: "UTC"
      timeout: "2m"
```

<!-- praetor:docs-references:on -->

| Key | Meaning |
| --- | --- |
| `regeneration.branch_prefix` | Branch prefix of the regeneration change. Default `regen/`. |
| `regeneration.title_type` | Conventional type, with an optional scope, of its title. Default `chore(generated)`. A type without a scope admits any scope. |
| `decline` | Built-in artefacts to leave out, by name. A name that is no built-in artefact is refused. |
| `artefacts[].name` | Printed in every result line: required, unique, one line, at most 128 bytes, never a built-in name. |
| `artefacts[].paths` | Globs of the artefact's files. Required. |
| `artefacts[].block` | `start` and `end` marker lines. With it the artefact is only the text from the start line through the end line in each file `paths` selects. |
| `artefacts[].command` | The argument vector that renders the artefact, run from the repository root. Required. |
| `artefacts[].sources` | Globs of the files the artefact is rendered from. Required. |
| `artefacts[].env` | Variables added to the command's environment; an empty value sets the variable empty. |
| `artefacts[].timeout` | Bound of one rendering, a Go duration up to `30m`. Default `10m`. |

Globs follow the `docs_surfaces` rules: repository-relative, `*` inside one directory, a `**`
segment across any number. Markers are whole lines and are ignored inside fenced code
(`util.FindMarkedBlock`). The loader refuses more than 64 artefacts, 32 declined names, 32 globs in
one list, 32 command words, a word or value above 1024 bytes and more than 16 variables; a glob that
selects more than 4096 files fails at run time (`internal/generated/artefact.go`).

A command is an argument vector, not a shell line, so it runs the same on Linux, macOS and Windows
when its program does. A relative program path resolves against the repository root.

## Writing a generator

A generator must be idempotent: rendering unchanged sources leaves its files byte for byte as they
are. One that stamps the current time on every run is never fresh, and `render --check` fails on it
forever. `praetorctl baseline --record` keeps a baseline whose debt did not change and
`praetorctl needs scan --write` keeps a manifest that differs only in `updated_at` for this reason
(`cmd/standardsctl/generator_idempotence_test.go`).

It runs in a temporary worktree of the commit being judged, checked out below
`.standards/worktrees/` and removed afterwards, so it sees committed files only. It exits non-zero
when it cannot render; its standard error is quoted in the failure.

## Pull requests: `ci generated check`

`check` compares the merge base of `--base` and `--head` (default `HEAD`) with the head:

- **Edits.** A changed path that a whole-file artefact selects edits it, and so does a rename or a
  removal, under the old path. A changed file that only block artefacts select edits each block whose
  text differs; a damaged marker counts as an edit.
- **Guarded set.** The artefacts that apply at the base or at the head. An artefact both declare
  guards the paths and blocks of both declarations. A change cannot stop declaring an artefact,
  decline it, narrow its `paths`, move its block markers or drop an `agent_clients` entry, and edit
  what the base declares, in the same change (`internal/generated/check_test.go`).
- **Marker.** Read from the base's manifest, never from the change, and checked against `--branch`
  and `--title`. Without both flags the change carries no marker.
- **Rendering.** Every artefact that applies at the head is rendered; artefacts that share a command
  and environment render once.

Each result line names what the change edits, which sources of the artefact it changes (the
artefact needs the regeneration change once the change lands) and which files differ from their
rendering. The check fails, listing each problem on standard error, when the change edits an
artefact without the marker, or when an artefact does not render.

## The regeneration change

On the default branch, after a batch has landed:

```bash
git switch -c regen/batch-42 origin/main
praetorctl ci generated render
git commit -s -am "chore(generated): render batch 42"
```

`render` writes only the artefacts that changed, and only into files that match `HEAD`: a file
the checkout changed since `HEAD` is refused, because the rendering read `HEAD`, not those changes.
In the pull request, `check` with the marker passes only when the change edits nothing but declared
artefacts and every artefact it commits equals its rendering.

## The default branch: `ci generated render --check`

`render --check` renders every artefact at `HEAD` and exits non-zero when one differs from its
committed file or does not render. A scheduled job runs it to find a lag that outlived its batch,
and the release gate requires it to pass (both wired in phase 2).

### How long artefacts may lag

Between a merge and the regeneration change of its batch the generated artefacts of the default
branch can lag their sources. The bound is one batch: the regeneration change of a batch lands
before the next batch starts landing, and no release is cut while `render --check` fails. For the
compiled context projections this means HISS-16 holds on the default branch after each regeneration
change; a session that needs current projections sooner runs `praetorctl compile-context` locally.

## Known gaps

- The `register.sources` census pin (`expected`, `not_applicable`, `sha256`) is a generated value
  inside the hand-edited `.standards.yaml`, without markers, so it is not declared. The proposed fix
  moves it to a file of its own beside the manifest, written by a write mode of
  `praetorctl caveman check --configured-sources` ([text register](text-register.md)).
- Phase 2 wires the hooks, CI and the release gate to `check` and `render --check`, folds the
  scheduled DevContainer bundle regeneration (#338) into the declared list, and replaces the
  line-keyed baseline fingerprint (#29), which today changes the baseline whenever lines move above
  a baselined function.

## Output reference

`list --json` prints the resolved set: `regeneration` (the marker), `declined`, and `artefacts`, each
with `name`, `origin` (`builtin` or `manifest`), `active`, `reason`, `paths`, `block`, `command`,
`env`, `sources`, `timeout` and `files`. `check --json` and `render --json` print a report with
`mode` (`pull-request` or `render`), `head`, `base` and `merge_base`, `regeneration_marker`,
`regeneration`, `changed_paths`, `outside` (what a regeneration change edits beyond the artefacts),
`written`, `problems` and `passed`, and per artefact `edited`, `sources_changed`, `rendered` (`ok`,
`failed` or `skipped`), `failure` and `changed`. The commands exit non-zero exactly when `passed` is
false. The types are `Set`, `Report` and `Result` in `internal/generated`.
