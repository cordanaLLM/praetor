# Operational fork synchronization

`praetorctl operational sync` supports three local stages: `init`, `plan` and
`prepare`. It consumes existing Git remotes, reviewed commits, and
`.standards.yaml` version 1.
It does not add a rollout configuration, install a bot, or contact GitHub.
This initial workstation implementation is verified on Linux with Git 2.55.0.

The initial contract supports a private operational repository with the same name
as its public source and a different owner. The owner's `origin` must match its
manifest; its `upstream` must match the public manifest and source checkout's
`origin`. Credential-free GitHub HTTPS and `git@github.com:` remotes are accepted.
These are local identity checks, not verification of live GitHub privacy settings.

## Apply the first overlay

A new operational fork starts as a checkout of the public source. `init` writes
the owner overlay into that checkout, so the first fork commit is engine output
that `plan` later accepts, not a hand edit it has to match byte for byte.

```bash
praetorctl operational sync init \
  --owner-path /path/to/private/praetor \
  --owner "$operational_owner"
```

`init` requires all of the following and writes nothing until they hold:

- `--owner-path` names a checkout root, with no Git filter drivers, grafts or
  submodule entries: the same input checks as `plan`.
- The checkout is clean: no modified, staged or untracked files.
- `.standards.yaml` at HEAD still carries the public source identity:
  `repository.visibility` is `public` and `repository.owner` differs from
  `--owner`.
- The `origin` remote is `<owner>/<name>` and the `upstream` remote is the
  manifest's `<source owner>/<name>`.
- `--owner` is a valid GitHub owner name.

`init` accepts no other flag: `--source-path`, the three SHA flags and
`--destination` are refused, and `plan` and `prepare` refuse `--owner`, which
they read from the reviewed manifest instead.

`init` writes the four overlay files into the working tree with the same
transformation `plan` and `prepare` verify against, with owner visibility
`private`. It stages nothing, commits nothing, creates no clone and contacts
nothing. After writing it verifies that HEAD did not move and that the working
tree differs from HEAD in exactly the four overlay paths, unstaged. Otherwise
the report status is `failed` with the error and the files stay in place for
inspection; `git checkout -- <the four paths>` restores them.

The report records `stage` `init` and `status` `initialized`.
`options.owner_sha` records the HEAD the overlay was derived from,
`changed_paths` lists the four files, and `owner_only_paths` is `[]`.

Review the diff and commit it signed and signed-off, for example as
`chore(operational): apply owner overlay`. `plan` then accepts that commit: with
`--base-sha` and `--source-sha` both set to the public tip the fork was cloned
from it reports `planned`, because a commit is its own ancestor. A second `init`
is refused: before the commit because the checkout is dirty, after it because
the manifest no longer carries the public identity.

## Plan from reviewed commits

Fetch and review source changes separately. Supply full lowercase 40-character
commit SHAs already present in the local repositories. The owner checkout must be
clean and its HEAD must equal `--owner-sha`. The previous public commit must be an
ancestor of both the owner and the new source. Missing objects, unrelated history,
and a changed reviewed owner HEAD are errors.
Both input paths must name checkout roots. Effective repository configuration,
including included files and worktree configuration, must contain no Git filter
drivers. Input submodule worktrees and local `info/grafts` metadata are rejected
before status inspection. Replacement objects and automatic fetching of missing
objects are disabled so local metadata cannot substitute reviewed commit ancestry.
See Git's [replacement and lazy-fetch controls](https://git-scm.com/docs/git).

```bash
praetorctl operational sync plan \
  --owner-path /path/to/private/praetor \
  --source-path /path/to/public/praetor \
  --owner-sha "$reviewed_owner_sha" \
  --base-sha "$incorporated_public_sha" \
  --source-sha "$reviewed_public_sha"
```

The existing owner tree must equal the incorporated source except for the
[owner-only operator paths](#owner-only-operator-paths) and for these four
files and these specific identity fields:

| File | Allowed owner difference |
| --- | --- |
| `.standards.yaml` | `repository.owner` and `repository.visibility` |
| `.devcontainer/devcontainer.json` | root `name` |
| `.paperclip/harness.json` | root `platform` |
| `.paperclip/rules.md` | repository identity in the generated first heading |

Unexpected engine differences, extra policy overrides, derived configuration drift,
or missing/symlinked configuration files stop the operation. Unknown upstream YAML
fields and comments are retained through `yaml.Node`; the owner manifest is never
round-tripped through the narrower Go `config.Manifest` struct. Identity anchors
and duplicate JSON keys are rejected. Derived JSON must retain the existing
generator's scalar formatting; ambiguous replacements are rejected.

## Owner-only operator paths

An operational fork also carries operator files the public source never has:
fleet topology, runner routing, cluster manifests, workstation settings. `plan`
accepts them in the owner tree and `prepare` in the candidate tree, under a
fixed list of owner-only prefixes:

| Prefix | Kind | Matches | Does not match |
| --- | --- | --- | --- |
| `.config/fleet.yaml` | exact file | `.config/fleet.yaml` | `.config/fleet.yaml.bak` |
| `.config/fleet-topology.yaml` | exact file | `.config/fleet-topology.yaml` | |
| `.config/orgs/` | directory | `.config/orgs/example.yaml`, `.config/orgs/deep/a.yaml` | `.config/orgsx/a.yaml`, `.config/orgs.yaml` |
| `.config/operator/` | directory | `.config/operator/fleet.yaml` | |
| `deploy/arc/` | directory | `deploy/arc/scale-set.yaml` | |
| `deploy/k8s/` | directory | `deploy/k8s/app/kustomization.yaml` | `deploy/k8sx/a.yaml`, `deploy/helm/values.yaml` |

Matching is on whole path segments. A path under a prefix is accepted only when
all of these hold:

- the public source has no such path at `--base-sha` and none at `--source-sha`;
- the entry is a regular non-executable file, Git mode `100644`: symlinks,
  submodule links and executable files are refused;
- it is at most 1 MiB;
- at most 256 such paths exist in total.

Exceeding a bound is an error. `prepare` additionally requires the candidate to
carry exactly the owner-only paths the reviewed owner tree carried. The report
lists them as `owner_only_paths`, a sorted array that is `[]` when there are
none.

The list is engine schema, not operator data, and is not configurable. The
engine's own `.gitignore` ignores every prefix, and an engine test replays
`git check-ignore` for each one, so a prefix cannot be added that the public
source could also track. Because the paths are ignored, the
fork tracks them with `git add -f`.

Everything else outside the four overlay files is still refused with
`unexpected owner tree difference`; a fork-only GitHub workflow file is
therefore not a legal owner path. The tree comparison runs with rename detection
disabled, so moving an engine file under an owner-only prefix is reported as the
deletion of that engine file and refused. A merge conflict in an owner-only path
is not resolved by the engine; only the four overlay files are.

## Prepare an ordinary merge

Use the same arguments with `prepare` and a new destination outside both input
repositories. The destination's parent must exist and have no symlink ancestors.

```bash
praetorctl operational sync prepare \
  --owner-path /path/to/private/praetor \
  --source-path /path/to/public/praetor \
  --owner-sha "$reviewed_owner_sha" \
  --base-sha "$incorporated_public_sha" \
  --source-sha "$reviewed_public_sha" \
  --destination /path/to/worktrees/reviewed-operational-candidate
```

Preparation creates a private clone and an `operational-sync-candidate` branch.
It runs an ordinary `git merge --no-ff --no-commit`, resolves only the four owner
configuration paths, and stages those exact paths. The candidate HEAD remains the
reviewed owner commit; `MERGE_HEAD` identifies the reviewed new source. A later
normal merge commit therefore retains published owner ancestry. An already
incorporated source produces a clean `up-to-date` candidate.

The final index tree must equal the reviewed source outside the owner overlay
and the owner-only operator paths.
Unresolved conflicts, unstaged modifications and untracked files are errors. No
source code, build scripts, hooks, filters, submodules or GitHub workflows are
executed by preparation. New clones use inert Git templates and an isolated Git
environment; neither input repository's configuration or hooks are modified.
Only the trusted running Praetor implementation transforms configuration data.

JSON output distinguishes `initialized`, `planned`, `preparing`, `prepared`,
`up-to-date`, and `failed` states. Failure after clone creation retains the
candidate path and error for inspection; it is not automatically deleted or
overwritten on retry. Runtime is capped at two minutes, Git output at 4 MiB per
stream, each configuration blob and each owner-only file at 1 MiB, and the
owner-only paths at 256. Exceeding a bound is an error, never successful partial
coverage.

## Which workflows run where

An operational fork carries the engine's workflow files unchanged, because a fork-only
workflow file would be an unexpected owner tree difference. What differs is which jobs run.
Engine-only jobs carry one condition:

```yaml
if: github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')
```

Repository variables are not inherited by a fork, so the literal decides there. It is the
`repository.owner` and `repository.name` of `.standards.yaml`; a test in `internal/forge`
parses every workflow and fails when the literal differs from the manifest, or when a
scheduled or publishing job has no guard.

| Workflow | Canonical repository | Any other copy |
| :-- | :-- | :-- |
| `ci.yml`, `compliance.yml`, `security.yml` on push and pull request | runs | runs: verification follows the code, and it gates sync candidates |
| `security.yml` schedule leg | runs | skipped |
| `sync-flavors.yml`, `sync-models.yml`, `pages.yml`, `wiki-sync.yml`, `release-binaries.yml`, `sbom.yml` | runs | skipped at job level; the job name says `canonical repository only` |
| `portability.yml` | runs | skipped with a stated reason unless the repository variable `PRAETOR_FORK_PORTABILITY` is `enabled` |
| `adopt.yml` | on dispatch or comment | on dispatch or comment: a person asked for it in that repository |

Set `PRAETOR_CANONICAL_REPOSITORY` only when the canonical repository itself moves; the
manifest identity and the literal then change in the same commit. Guards limit what a copy
runs on its own. They do not replace disabling Actions on a fork before its first push.

## Review and later rollout

A prepared candidate is structural evidence, not a test result or an Exit-0
receipt. Review its diff and exact parent commits, explicitly activate the normal
project hooks when it becomes a trusted working checkout, and run the applicable
verification gates before committing. Publication is a separate normal
fast-forward push; this command never pushes, rebases published history, merges a
default branch, changes privacy/protection, opens a PR, or activates an agent.

There is currently no scheduler, GitHub App runtime, webhook consumer, automatic
promotion stage, or workstation rollout consumer behind this interface. Those
require separately reviewed implementations and activation. Current repository
gate failures remain failures; a successful structural preparation does not
waive them. The operation is currently CLI-only and has no MCP mutation tool.
