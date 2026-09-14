# GitHub wiki sync

The `Wiki Sync` workflow copies the regular Markdown files in `docs/wiki/` to
the repository wiki. It updates source-owned filenames and preserves other wiki
pages. The workflow and each Git network process have explicit time limits.

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
