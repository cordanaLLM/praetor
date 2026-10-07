# GitHub wiki sync

`praetorctl forge sync-wiki [--output=docs/wiki]` (`internal/forge.GenerateWiki`,
invoked from `cmd/standardsctl/forge.go`) generates the pages under `docs/wiki/`
from repository content; running it and committing its output is how an operator
refreshes them. The `Wiki Sync` workflow only copies that already-generated
`docs/wiki/` into the repository wiki — it does not call `sync-wiki` itself, so a
regenerate-and-commit is still required to keep the published wiki current. The
workflow triggers on a push to `main` that touches `docs/wiki/**`,
`.github/workflows/wiki-sync.yml`, `scripts/sync_github_wiki.sh`,
`docs/assets/figures/**` or the figure engine files the sync runs
(`tools/figures/build.mjs`, `checks.mjs` and `core.mjs`), and on manual
`workflow_dispatch`; it does not run on every push. The workflow sets up Node
for that step; it needs no npm package.

The generated portal is named after the repository's identity:
`repository.owner`/`repository.name` in `.standards.yaml`, else the origin remote, with
`forge.default_owner` from the operator settings (`--fleet-config`,
`--workstation-config`, `--manifest`) as the last owner step
(`config.ResolveRepositoryIdentity`). A checkout that declares no identity and has no
origin remote fails instead of naming the portal after its directory layout
(`TestResolveWikiRepoName_Negative_NoIdentityIsAnError` in
`internal/forge/wiki_test.go`).

Every top-level entry directly under `docs/wiki/` must be a regular, non-symlinked
file whose name ends in `.md`; `scripts/sync_github_wiki.sh` rejects any other
entry (a directory, symlink, or non-`.md` file) and exits 2 before copying
anything, so one bad entry blocks the whole sync. The source directory holds at
most 256 such files (`MAX_WIKI_FILES`); more also exits 2. The workflow updates
source-owned filenames and preserves other wiki pages. The workflow and each Git
network process have explicit time limits.

## Figures

Every generated page except the moved `HISS-16-Invariants` stub draws its
diagram as a `figure` fence, and none carries Mermaid: `Home` names
`governance-lifecycle`, `HISS-Invariants` and `HISS-Matrix` name
`verification-ladder`, `Architecture-Lattice` names `lattice-join`, and
`API-Reference` names `forge-federation` (`TestGenerateWiki_Positive` and
`TestGenerateWiki_Boundary_EveryFigureHasCommittedOutputs` in
`internal/forge/forge_test.go`).

The wiki runs no JavaScript, so it cannot show the site's interactive figures.
After copying the pages into the wiki clone, `scripts/sync_github_wiki.sh` runs
`node tools/figures/build.mjs portable --wiki` on the copies. Each
`figure` fence becomes a `<figure>` holding a `<picture>` of the figure's
animated and static SVGs on the published site, each with its own recorded
width and height, with absolute URLs built from
the `site_url` in `mkdocs.yml`, then a link to the interactive figure on the
site (`<site_url>wiki/<Page>/#fig-<slug>`) and the text description. The pages
under `docs/wiki/` keep their fences; only the wiki copies change. A fence that
names a figure without `docs/assets/figures/<slug>.json` stops the sync before
anything is committed. The [figures guide](figures.md) covers the figures
themselves.

The images point at the published site, so a figure added in the same push
shows in the wiki once the Pages deployment has finished.

## Removing pages

Each sync records the names it published in `.praetor-wiki-manifest` at the root
of the wiki repository, one name per line. On the next run,
`scripts/sync_github_wiki.sh` removes only pages that the manifest lists and
`docs/wiki/` no longer contains. Pages created in the wiki itself never appear in
the manifest, so the sync keeps them. The manifest is not a page: GitHub wikis run
on [Gollum](https://github.com/gollum/gollum), which renders only files with a
markup extension.

- A wiki without a manifest removes nothing on that run and records one. A page
  deleted from `docs/wiki/` before the first recorded sync stays in the wiki
  until it is removed by hand.
- Each entry is a literal page name. An entry that is empty, contains `/`, or does
  not end in `.md`, a manifest listing more than 256 pages, or a manifest that is
  not a regular file stops the sync before any change. Correct the manifest in the
  wiki repository, or delete it to resume without removals.
- Source file names must not contain line breaks, because the manifest is
  line-based.

GitHub exposes the wiki Git repository only after an initial page has been
created on GitHub. This prerequisite comes from GitHub's
[wiki editing documentation](https://docs.github.com/en/communities/documenting-your-project-with-wikis/adding-or-editing-wiki-pages#cloning-wikis-to-your-computer).
Before cloning, the script runs `git ls-remote` on the wiki remote with the job
token. When Git answers `repository '<url>' not found`, read in the C locale, the
run mirrors nothing and still succeeds: it prints a `::notice::` and adds a line
to the step summary saying to save a first page through the Wiki tab and rerun
the workflow. GitHub gives the same answer for a repository the token cannot
read, which the repository's own job token always can.

Every other probe or clone failure remains an error: authentication, connectivity
and a timeout fail the run with Git's message, as does a local remote path that
does not exist (`WikiProbeTests` in `scripts/test_sync_github_wiki.py`, whose
stubbed Git answers for the GitHub remote). The workflow passes
`github.token` through a temporary askpass helper. The token is not placed in the
remote URL, command output, or Git configuration, and the helper is deleted when
the script exits.

Run the hermetic local regression suite with:

```bash
make wiki-sync-test
```
