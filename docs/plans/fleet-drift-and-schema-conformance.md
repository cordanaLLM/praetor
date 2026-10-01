# Fleet drift and cross-repository schema conformance

Status: design proposal, 2026-09-29, baseline `8d0344e9`. Covers #605 (copied governance and
privacy scripts drift between adopted repositories) and #607 (a consumer's decoder of a
cross-repository payload schema is never checked against the owner's). This pass ships one
read-only survey command and this note; declarations, `audit` checks and gates are later
units, listed under [Staged delivery](#staged-delivery).

## What ships now: `harvest drift`

`praetorctl harvest drift` compares the files that several local repositories carry under
the same path, and changes nothing:

```bash
praetorctl harvest drift --dir /path/to/dev
praetorctl harvest drift --dir /path/to/dev --path scripts/ --path .githooks/ --json
```

- **Repositories.** The survey reads the repositories `praetorctl harvest workstation` finds
  (`harvester.ScanLocalWorkstation`), one checkout per Git identity (`git_common_dir`): a main
  checkout before a linked worktree, a linked worktree before a bare repository.
- **Content.** Each repository is read at its HEAD commit: `git ls-tree` lists the files and
  `git cat-file` reads them, both through the isolated probe environment of
  `util.RunGitProbe`. Working-tree edits, clean or smudge filters and hooks take no part.
  Symbolic links and submodules are not copies and are skipped.
- **Scope.** `.githooks/` and `scripts/`, the directories that conventionally hold
  hand-copied governance and privacy scripts, unless `--path` names other
  repository-relative prefixes (at most 32), such as a directory of agent hook scripts.
- **Findings.** A path two or more repositories carry is *drifted* when the copies differ and
  *identical* when they agree. Each variant carries its sha256 digest, line count, the
  repositories that hold it, and the lines it removes and adds relative to the most common
  variant (`util.LineDeltaOf`, the summary adoption uses for kept drift). A path that one
  repository carries alone is no finding.
- **Exit status.** Drift never fails the command. A repository whose identity, HEAD or
  listing cannot be read, a copy above 1 MiB, more than 5,000 files under the selected
  paths in one repository, more than 500 shared paths, or an incomplete workstation
  inventory make the report incomplete and the command exit nonzero, with the report still
  printed.

Evidence: `internal/harvester/drift.go`; `internal/harvester/drift_test.go`
(`TestSurveyDrift_*`, `TestNormalizeDriftPaths_*`); `cmd/standardsctl/harvest_drift_test.go`.
The survey answers #605's second expectation, finding copies nobody declared. It does not
declare, pin or enforce anything.

## Declared copies (#605, later unit)

An adopter declares that a file is a copy of another repository's file, and `audit` fails
when the copy no longer matches its pinned source. The declaration belongs in
`.standards.yaml`, decoded by `internal/config`'s `Manifest`, not in a second configuration
file:

```yaml
copies:
  - path: scripts/guard.py
    source:
      repository: <owner>/<repository>
      path: scripts/guard.py
      revision: <full commit id>
    digest: sha256:<hex of the source file at that revision>
```

The check has two steps:

1. **Copy against the pin.** The committed copy's digest is compared with `digest`, offline.
   A mismatch fails `audit` and names the path, the source repository and revision, and both
   digests. Recording the digest beside the revision keeps this step independent of the
   network and catches a rewritten revision.
2. **Pin against the source.** The source file is read at `revision` from a local checkout of
   `source.repository` under the dev root, matched by its origin remote
   (`util.ReadOriginRemote`) and read with `git cat-file` as the survey does. `internal/forge`
   has no file-content read today; a forge fallback would be a new method on the existing
   driver, not a second client. A source that cannot be read is reported **unverified**, never
   passed.

Digests use the `sha256:<hex>` form `.standards.lock` already uses (`internal/config/lockdigest.go`).
The survey's `--json` output lists every copy's repositories and digest, so it can seed the
first declarations. A copy that must differ from its source is not a copy; the design has no
allowed-delta field.

Related scope: #66 covers files Praetor itself vendors, which `.standards.lock` and the
managed asset families pin; declared copies cover files Praetor never published. #161 is
clone detection inside one repository, Go only; the survey compares whole files across
repositories in any language. #203 (matching records of work across sources) is separate.

## Cross-repository payload schema conformance (#607, later unit)

One repository owns a payload schema and its decoder; other repositories decode the same
payload with their own implementations, and nothing checks that they agree. The
`api:public-contract` facet (`.config/archetypes/facets/api-public.yaml`) supplies branch
protection, linter names and a devcontainer feature; in code it is a default facet name
(`internal/config/facets.go`) and a devcontainer case (`internal/devcontainer/devcontainer.go`).
Issues #357 (an API compatibility gate inside one repository) and #353 (facet linters that
never run) stay within one repository. `praetorctl needs contract export` snapshots framework
capabilities and is unrelated.

**Owner.** The owning repository declares the schema id and a vector manifest, data only:

```yaml
# .standards.yaml
schemas:
  owns:
    - id: <schema id>
      vectors: conformance/<schema id>/vectors.yaml
```

```yaml
# conformance/<schema id>/vectors.yaml
schema: <schema id>
vectors:
  - file: shipped.json
    verdict: accepted
  - file: unknown-field.json
    verdict: refused
```

The owner's own gate replays its decoder over the vectors, so a recorded verdict cannot go
stale without failing the owner first.

**Consumer.** A consuming repository declares the schema id, the owner, a pinned revision and
its own decode command as an argument vector:

```yaml
schemas:
  consumes:
    - id: <schema id>
      owner: <owner>/<repository>
      revision: <full commit id>
      decode: ["<decoder>", "<subcommand>", "{file}"]
```

**Gate.** The consumer's gate reads the owner's vector manifest and payloads at `revision`,
through the same source read as declared copies. It runs `decode` once per vector, without a
shell and under a time and output bound (`util.RunCommandBytes`), and reads exit status 0 as
accepted and any other as refused. Any verdict that differs from the owner's fails the gate
and names the vector with both verdicts. Vectors that cannot be read make the result
unverified, never passed. A consumer is checked against its pinned revision's vectors until
the pin moves. A schema id that two repositories claim to own is refused, which needs a fleet
view: the dev-root survey `harvest drift` already walks, or the operator's
`.config/fleet-topology.yaml` (`harvester.LoadFleetTopology`).

**Trust.** The gate executes only the consumer's own declared command inside the consumer's
checkout. Everything read from the owner is data; the owner's decoder never runs in the
consumer's gate. Vector count and payload size are bounded like the survey's reads.

No command ships for #607 in this pass: until a repository declares `schemas`, there is
nothing to survey.

## Staged delivery

Each later unit reports before it enforces, and each enforcement unit gets its own issue.

1. This pass: `praetorctl harvest drift` and this note.
2. `copies:` in `Manifest`, with decoding and validation tests, and an `audit` check that
   compares copies with their pins and reads sources from local checkouts only; unreachable
   sources are unverified.
3. A file-content read on the forge driver, used as the source fallback.
4. `schemas.owns`, the vector manifest schema and the owner-side replay.
5. `schemas.consumes` and the consumer gate, with schema-id uniqueness across the fleet view.

## Known gaps of this pass

- No copy declarations and no `audit` failure for a drifted declared copy (#605).
- No schema declarations, vector format or conformance gate (#607).
- The survey reads repositories under one local dev root only, matches copies by identical
  path (a renamed copy is not paired), and has no MCP tool; `standards_harvest_workstation`
  covers the inventory alone.
