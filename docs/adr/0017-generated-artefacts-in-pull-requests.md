# ADR-0017: Generated Artefacts in Pull Requests

## Status

Accepted — 2026-10-01. The owner decided the questions of #696 on 2026-10-01; "Decision" records
them. Phase 1 (the declaration, the commands and this record) lands with it. Phase 2 wires the
hooks, CI and the release gate to the pull-request mode, folds the scheduled bundle regeneration
of #338 into the declared list, and replaces the line-keyed baseline fingerprint (#29).

## Context

A governed repository commits files that other files determine: the compiled context projections
`praetorctl compile-context` writes from `AGENTS.md`, the debt baseline `.standards-baseline.json`,
the README governance block, `.needs.yaml`, the documentation figures, the shipped-text ledger and,
in Praetor itself, the DevContainer source bundle. The gates required every pull request to carry
these files fresh, so any two open pull requests changed the same generated lines and conflicted
with each other even when their sources did not.

Issue #696 measured the cost in an adopter repository: 78 pull requests landed in about 26 hours through a
local merge train, each landing a restack, a build and test run and a pre-push hook run of about five
minutes, a ceiling near seven per hour. A train that stacked several pull requests could push them
together only when they shared no generated file, and nearly every one did. The baseline added a
case of its own: its fingerprints are keyed by line (#29), so a change that inserts lines above a
baselined function has to re-record the baseline, and two such changes conflict in it.

`merge=union` in `.gitattributes` does not help: the forge applies no merge drivers, so a branch
`git merge-tree` merges cleanly is still conflicting there (#696).

Before this record nothing declared which files are generated. Each tool that touched a branch (a
merge train, a conflict resolver, an agent) had to carry its own list.

## Decision

### 1. One declared list

The `generated` section of `.standards.yaml` declares the generated artefacts. The manifest loader
reads and validates it (`config.LoadManifest`, `internal/config/generated.go`); there is no second
configuration file. Each entry names:

- `paths`: the globs of its files, or, with `block`, the region between two marker lines inside
  each of them, so a hand-edited file that carries a generated block stays editable outside it;
- `command`: the argument vector that renders it from the repository root, with optional `env`
  and `timeout`; a first word `praetorctl` runs the running binary itself;
- `sources`: the globs it is rendered from.

Praetor's own artefacts are built in (`internal/generated/builtin.go`) and apply wherever their
files exist; `generated.decline` leaves one out by name, and `generated.artefacts` adds the
repository's own. `praetorctl ci generated list` prints the resolved list, and `--json` gives merge
trains, conflict resolvers and agents the same list in machine-readable form.

A generated value that lives inside a hand-edited file without markers is not declared, because
declaring the whole file would forbid every edit of it. The one such value today is the
`register.sources` census pin in `.standards.yaml` (`expected`, `not_applicable`, `sha256`). The
proposed fix moves the pin to a file of its own beside the manifest, written by a write mode of
`praetorctl caveman check --configured-sources` and declared as a built-in artefact; until then
the pin stays a hand-maintained value.

### 2. A pull request must not edit a declared artefact

`praetorctl ci generated check --base=<ref>` judges the change from the merge base to the head. A
change that edits a declared artefact (a file of a whole-file artefact, or the text inside a
block) fails. The guarded set is the union of the base's and the head's declarations, so a change
cannot stop declaring an artefact and edit it at once.

### 3. In a pull request the gate proves only that each artefact still renders

The same check renders every artefact that applies at the head in a temporary worktree of the
head. A generator that fails, or a rendering that selects no file, fails the check: the gate fails
closed in the pull request, not at regeneration time. Whether the committed file equals its
rendering is reported but not enforced in a pull request.

### 4. Freshness is enforced on the default branch only

`praetorctl ci generated render --check` renders every artefact at `HEAD` and exits non-zero when a
committed file differs from its rendering, for a scheduled job and for the release gate.
`praetorctl ci generated render` writes the changed artefacts instead, for the regeneration change.

### 5. One regeneration change per batch, recognised by a declared marker

The regeneration change is the only change allowed to touch declared artefacts. The gate recognises
it by a marker the base declares, never the change itself: its branch starts with
`generated.regeneration.branch_prefix` and its title carries the conventional type
`generated.regeneration.title_type`; both must match. The defaults are `regen/` and
`chore(generated)`. A regeneration change may edit nothing but declared artefacts, and each
artefact it commits must equal its rendering.

### 6. The bound on lag

Between a merge and the regeneration change of its batch, the generated artefacts of the default
branch, the compiled context projections included, can lag their sources. The bound is one batch:
the regeneration change of a batch lands before the next batch starts landing, and no release is
cut from a default branch on which `render --check` fails. HISS-16's guarantee, every projection
compiled from the canonical `AGENTS.md`, holds on the default branch after each regeneration change.
A session that needs current projections before then runs `praetorctl compile-context` locally.

### 7. Generators are idempotent

Rendering an unchanged source leaves its artefact byte for byte as it is; otherwise the artefact is
stale forever. `praetorctl baseline --record` therefore keeps a baseline whose debt did not change,
and `praetorctl needs scan --write` keeps a manifest that differs only in `updated_at`. An adopter's
own generator has to meet the same rule.

## Consequences

### Positive

- Pull requests stop conflicting on generated files, so a merge train can gate a stack of them once
  and land them together; the batch's single regeneration change carries every rendering.
- An adopter declares its own generated files in the manifest it already has, and every tool reads
  one list.
- A pull request whose generator breaks still fails in the pull request.

### Negative / Trade-offs

- The generated artefacts of the default branch, the compiled projections included, lag their
  sources until the regeneration change lands (decision 6).
- The pull-request check runs every generator. Rendering all of Praetor's built-in artefacts took
  under five seconds on a warm build cache (`ci generated render --check`, 2026-10-01), but a cold
  runner pays for each toolchain.
- Every regeneration change is one more pull request to review, and a generator that is not
  idempotent cannot be declared until it is.

### Neutral

- Phase 1 changes no hook, workflow or gate: the existing freshness gates keep running until phase 2
  wires them to the pull-request mode.

## Alternatives considered

- **Keep requiring fresh artefacts in every pull request.** Rejected: that is the conflict #696
  measured.
- **Merge drivers such as `merge=union`.** Rejected: the forge applies none.
- **Enforce freshness in the pull request as well.** Rejected: it brings the conflicts back.
- **Recognise the regeneration change by its branch alone, or by its title alone.** Rejected: the
  owner chose both, declared, with documented defaults.
- **Stop committing generated files.** Rejected for these artefacts: agent clients read the
  projections from the checkout, the forge renders the README, and a DevContainer build reads the
  bundle, all without running a generator.

## References

- #696: the issue this record decides.
- #338: the scheduled DevContainer bundle regeneration, which phase 2 turns into one entry of the
  declared list.
- #29: baseline fingerprints keyed by line number.
- Owner decisions of 2026-10-01 on #696, paraphrased under Decision 2 to 6.
- `internal/config/generated.go`, `internal/generated/`, `cmd/standardsctl/ci_generated.go`,
  `docs/guides/generated-artefacts.md`.
