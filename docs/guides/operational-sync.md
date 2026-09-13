# Operational fork synchronization

`praetorctl operational sync` supports two local stages: `plan` and `prepare`.
It consumes existing Git remotes, reviewed commits, and `.standards.yaml` version 1.
It does not add a rollout configuration, install a bot, or contact GitHub.
This initial workstation implementation is verified on Linux with Git 2.55.0.

The initial contract supports a private operational repository with the same name
as its public source and a different owner. The owner's `origin` must match its
manifest; its `upstream` must match the public manifest and source checkout's
`origin`. Credential-free GitHub HTTPS and `git@github.com:` remotes are accepted.
These are local identity checks, not verification of live GitHub privacy settings.

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

The existing owner tree must equal the incorporated source except for these four
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

The final index tree must equal the reviewed source outside the owner overlay.
Unresolved conflicts, unstaged modifications and untracked files are errors. No
source code, build scripts, hooks, filters, submodules or GitHub workflows are
executed by preparation. New clones use inert Git templates and an isolated Git
environment; neither input repository's configuration or hooks are modified.
Only the trusted running Praetor implementation transforms configuration data.

JSON output distinguishes `planned`, `preparing`, `prepared`, `up-to-date`, and
`failed` states. Failure after clone creation retains the candidate path and error
for inspection; it is not automatically deleted or overwritten on retry. Runtime
is capped at two minutes, Git output at 4 MiB per stream, and each configuration
blob at 1 MiB. Exceeding a bound is an error, never successful partial coverage.

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
