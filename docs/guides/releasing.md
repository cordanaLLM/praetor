# Releasing and Release Flavors

A commit on `main` becomes an installable version only when a `v*` tag points at it. This
page covers what that tag sets off, how to cut one, and how the moving flavor tags
(`bleeding`, `edge`, `latest`, `lts`) follow it.

## What a `v*` tag produces

| Consumer | Trigger | Result |
| :--- | :--- | :--- |
| `.github/workflows/release-binaries.yml` | `push` of a tag matching `v*` | GoReleaser (`.goreleaser.yaml`) builds `praetorctl`, `standardsctl`, `standards-mcp` and `standards-lsp` for Linux, macOS and Windows on amd64 and arm64, attaches Syft SBOMs, signs `checksums.txt` keyless with cosign, and publishes a GitHub release |
| `go install github.com/cordanaLLM/praetor/cmd/standardsctl@latest` | any release version on the module proxy | `@latest` selects the highest release version; with no tag at all it falls back to a pseudo-version of `main` ([Go modules reference, version queries](https://go.dev/ref/mod#version-queries)). `.github/actions/praetor-adopt/action.yml` installs this way |
| `.github/workflows/sync-flavors.yml` | next run after the tag exists | moves `latest` to the highest `v*` tag |

Once the first release exists, `@latest` stops following `main`. An adopter that wants
unreleased commits has to ask for them, for example `@main`.

## Cutting a release

The pull-request flow squash-merges into `main`, so the commit prepared on a branch is not
the commit that lands. The tag is therefore made after the merge, on `main`, and
`praetorctl release` deliberately does not tag.

1. Prepare the changelog on a branch cut from `origin/main`:

   ```bash
   git switch -c release/v0.1.0 origin/main
   praetorctl release --version=v0.1.0 --date=2026-09-18
   ```

   `praetorctl release` (`internal/release/release.go`) checks the version is SemVer and
   the tree is clean, runs `make verify-all`, then renders `changelog.d/` into a new
   `CHANGELOG.md` section and removes the rendered fragments. Commit the result and merge
   it through a pull request.

2. Tag the merged commit and push only the tag:

   ```bash
   git fetch origin
   git tag -a v0.1.0 -m "v0.1.0" origin/main
   git push origin v0.1.0
   ```

   Use `git tag -s` instead of `-a` to sign the tag.

3. Check the result:

   ```bash
   gh run list --workflow release-binaries.yml --limit 1
   gh release view v0.1.0
   praetorctl flavors plan   # latest now resolves to v0.1.0
   ```

   The flavor sync runs on the next push to `main`, every six hours, or on
   `gh workflow run sync-flavors.yml`.

`internal/changelog/tracked_fragments_test.go` renders the tracked fragments into a
temporary copy on every `go test`, so a fragment that would stop step 1 fails the change
that adds it.

## Release flavors

`.config/flavors.yaml` declares each flavor and the ref it follows:

| Flavor | Source ref | Stability |
| :--- | :--- | :--- |
| `bleeding` | `refs/heads/main` | experimental |
| `edge` | `refs/heads/main` | pre-release |
| `latest` | `refs/tags/v*`, highest SemVer tag with no prerelease component | stable |
| `lts` | `refs/heads/lts-*`, highest by the same sort | enterprise-stable |

```bash
praetorctl flavors plan                 # print each flavor's current and target commit
praetorctl flavors sync                 # move the local tags
praetorctl flavors sync --push          # ...and publish them
praetorctl flavors sync --strict        # fail if any source ref resolves to nothing
```

| Flag | Default | Effect |
| :--- | :--- | :--- |
| `--push` | off | Publishes every tag the sync moved in one `git push --atomic --force`, so the remote takes all of them or none |
| `--remote` | `origin` | Remote that `--push` publishes to |
| `--strict` | off | Fails before any tag moves when a declared source ref resolves to no commit |
| `--config` | `.config/flavors.yaml` | Flavor declarations |
| `--dir` | `.` | Repository root |

A flavor whose source ref resolves to no commit is **pending**. That is the normal state for
`latest` before the first `v*` tag, for `latest` while only prerelease tags exist (a
`v0.2.0-rc.1` alone never stands in for `latest`, see #245), and for `lts` before the first
`lts-*` branch. `sync` prints it as `Pending <flavor>`, leaves its tag exactly where it is,
and still moves every other flavor. It never falls back to `HEAD`, so a stable channel is
never aliased to `main`. The workflow runs `flavors sync --push`, so the tags published are
the flavors declared in the config, not a list of names kept in YAML.

Moving tags are lightweight tags created with `git tag --no-sign`. A workstation with
`tag.gpgSign=true` would otherwise turn them into signed annotated tags that need a
message, and the sync would fail.

`cmd/standardsctl/flavors_cli_test.go` covers pending flavors, `--strict`, the atomic push
against a bare repository, and the signing configuration.

## Known limits

- `latest` ignores tags that do not parse as SemVer even though they match the `v*` glob
  (for example a stray `v-nightly`); those are treated as absent, not as an error.
- `lts` matches local branches only. A CI checkout creates a local branch for `main` alone,
  so an `lts-*` branch that exists only on the remote leaves `lts` pending.
