# GitHub wiki sync

The `Wiki Sync` workflow copies the regular Markdown files in `docs/wiki/` to
the repository wiki. It updates source-owned filenames and preserves other wiki
pages. The workflow and each Git network process have explicit time limits.

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
created on GitHub. If a repository has its wiki feature enabled but the workflow
reports `Repository not found`, create and save the first page through the Wiki
tab, then rerun the workflow. This prerequisite comes from GitHub's
[wiki editing documentation](https://docs.github.com/en/communities/documenting-your-project-with-wikis/adding-or-editing-wiki-pages#cloning-wikis-to-your-computer).

Other clone failures remain errors. Check the Git message for authentication or
connectivity problems instead of assuming the wiki is empty. The workflow passes
`github.token` through a temporary askpass helper. The token is not placed in the
remote URL, command output, or Git configuration, and the helper is deleted when
the script exits.

Run the hermetic local regression suite with:

```bash
make wiki-sync-test
```
