# Planning-artifact onboarding

Use the `planning-artifacts` archetype for a repository whose current product is
research, a concept, or a preparation bundle. It supplies bounded Praetor
preparation governance and the repository's strict HISS complexity limits. It
does not choose a language runtime, execute a build, boot an image, or establish
release evidence.

## Safe preparation

Keep the source notes, exports, and working material in the repository's private
workspace policy. Preserve source bytes and provenance; do not copy private
material into public generated context or a candidate scaffold. Review the
planned adoption report before applying it. Use a dry run first, for example:

```text
praetorctl adopt --profile planning-artifacts --facets agent:sandboxed --dry-run \
  --path /path/to/repository --lock-source-root /path/to/praetor
```

The profile's development container requests only baseline utilities and selects
no project language runtime or GPU feature. Adoption may still use its existing
bootstrap builder to prepare the governance harness; that is separate from a
project toolchain. Add a language toolchain, GPU feature, or executable scaffold
only after a repository manifest, pinned inputs, and an explicit contract select
it. A candidate scaffold is inactive until those inputs exist and its commands
have been run.
Missing tools and malformed or oversized inputs remain preparation failures;
there is no blanket adoption bypass.

The explicit source bundle also supplies the shared checkpoint evaluator and
local-only policy. Adoption installs Lefthook tool/stop jobs without enabling
remote publication. Existing custom hook configuration and checkpoint settings
remain authoritative; inspect reported warnings and run the actual jobs to prove
wiring. Client-native lifecycle activation is a separate workstation step.

Use `scripts/planning_import.py` to organize a flat local export without
executing its setup scripts. Supply an explicit version-1 JSON manifest with
`entries` containing `source`, `destination`, and the original `sha256` for
every file. Outputs must be a new directory inside `.workingdir`; imported
content remains untrusted data with private permissions. Review the dry run
first, then apply the same command without `--dry-run`:

```sh
python3 -B scripts/planning_import.py --manifest /repo/.workingdir/import.json \
  --source /repo/.workingdir/export --output /repo/.workingdir/prepared --dry-run
```

Name imported governance files and scripts as inert proposals, such as
`proposals/AGENTS.md.txt` or `proposals/setup.sh.txt`. Keep the raw export and
manifest unchanged. The importer refuses missing, extra, conflicting, oversized,
or digest-mismatched inputs. Its preparation report does not certify the
imported code, research, or workflow claims. Prepare supported text sources
separately with `praetorctl notebook prepare`; record excluded binary or
oversized sources and their hashes explicitly instead of silently truncating.

## Readiness boundaries

Record these as separate decisions and evidence sets:

| Stage | What must be demonstrated |
| --- | --- |
| Planning | Sources, assumptions, ownership, scope, and bounded preparation are reviewed. |
| Build | Pinned manifests and dependencies produce the intended artifact. |
| Boot | The artifact starts in its declared target environment and its checks pass. |
| Release | Review, provenance, security checks, publication, and rollback evidence exist. |

A planning report can satisfy the first row while the other rows remain
`unverified` or `blocked`. Generated instructions describe intended work and
cannot become unrestricted upstream authority. Native runtime, deployment, and
release claims require their own executable evidence and explicit contracts.
