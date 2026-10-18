# ADR-0021: Figure Player Bundle as an Engine Release Asset

## Status

Accepted — 2026-10-18. Supersedes [ADR-0016](0016-figures-for-adopters.md) section 2.

## Context

ADR-0016 section 2 placed the pre-built interactive figure player bundle
(`player/loader.js`, `player/player.js`, and `player/THIRD-PARTY-LICENSES.txt`)
into the managed `figure-engine` family inventory so `praetorctl adopt` copied
those three files into every adopter repository with `docs:seo-portal`.

Emitting a minified third-party JavaScript bundle (`player.js` carries bundled
React, `react-dom`, `scheduler`, and `interfig`) into adopter trees conflicted
with the repository governance invariant `exceptions-never-tiers` (`AGENTS.md`
operational rule 14):

- Every adopter repository needed two dated `codespell` exceptions for
  `tools/figures/player/player.js` and
  `tools/figures/player/THIRD-PARTY-LICENSES.txt`, plus formatter and ESLint
  exclusions for a minified bundle that adopters neither author nor edit.
- Every bump to React, `interfig`, or `esbuild` changed the binary-like player
  bytes across every adopted repository even when no figure source or check
  changed.

## Decision

1. **Retire the player bundle from adopter trees.** `player/loader.js`,
   `player/player.js`, and `player/THIRD-PARTY-LICENSES.txt` are removed from
    the managed `figure-engine` asset inventory (`tools/figures/assets.go`,
   `MaxAssets = 16`). Their shipped digests are registered in the family's
   `Moved` map (`internal/managedasset/family.go`), so `praetorctl adopt`
   removes unedited copies automatically and refuses edited copies unless
   `--force` is supplied.
2. **Keep the committed bundle in the engine repository.** The engine keeps
   building and committing `tools/figures/player/` for its own documentation
   site and embeds those three files separately in `tools/figures` so the
   deterministic archive packer has the exact bytes.
3. **One deterministic archive packer (`HISS-19`).** `figures.PlayerArchive()`
   (`tools/figures/player_pack.go`) builds `figures-player.tar.gz`
   deterministically from the three embedded files in fixed order
   (`loader.js`, `player.js`, `THIRD-PARTY-LICENSES.txt`), with mode `0644`,
   `uid` and `gid` `0`, empty `uname` and `gname`, Unix epoch zero `ModTime`,
   and an unnamed zero-`ModTime` gzip header. `figures.PlayerArchiveSHA256()`
   returns the hex SHA-256 digest of those bytes, and
   `standardsctl figures pack --out <file>` writes the archive and prints
   `<sha256>  figures-player.tar.gz`.
4. **Release asset publication.** `.github/workflows/release-binaries.yml` runs
   `standardsctl figures pack --out dist/figures-player.tar.gz` before
   publishing a release, includes `figures-player.tar.gz` in `SHA256SUMS`,
   verifies its checksum, signs and attests it alongside the binary archives,
   and uploads it to the GitHub release.
5. **Adopter lock file (`tools/figures/player.lock.json`).** In adopter
   repositories (every repository where the engine-only
   `tools/figures/bundle.mjs` is absent), `praetorctl adopt` writes
   `tools/figures/player.lock.json`:

   ```json
   {
     "asset": "figures-player.tar.gz",
     "sha256": "<PlayerArchiveSHA256>",
     "release": "<engine release tag or empty>"
   }
   ```

   The `release` field records the engine release tag the adopter pins (from
   the running engine binary's release identity or `tools/go.mod`), or `""` when
   adopted from an unreleased engine revision. An optional `"repository"` field
   (defaulting to `cordanaLLM/praetor`) lets an adopter on its own fork fetch
   the release asset from that fork.
6. **MkDocs hook fetch and verified extraction.** `tools/figures/mkdocs_hook.py`
   publishes `tools/figures/player/` directly when that directory exists on
   disk (the engine repository). Otherwise it reads
   `tools/figures/player.lock.json`:
   - When `release` is empty, it logs one warning line
     (`figures: engine pin <...> has no release; figures render as SVG (no interactive player)`)
     and publishes no player files.
   - When `release` is set, it downloads
     `https://github.com/<repository>/releases/download/<release>/figures-player.tar.gz`
     with a 30-second timeout and `MAX_FILE_BYTES` ceiling, verifies that its
     SHA-256 matches `sha256` in `player.lock.json` (raising an error naming
     both digests on mismatch), extracts only `loader.js`, `player.js`, and
     `THIRD-PARTY-LICENSES.txt` (refusing any other member, absolute path, `..`
     segment, or non-regular entry), and publishes them under
     `assets/figures/player/`.
   - When the download fails (such as HTTP 404 or network unavailability), it
     logs the same single SVG fallback warning rather than failing the site
     build.
7. **Audit refusal and `codespell` cleanup.** `standardsctl audit` fails the
   documentation gate in any non-engine repository that still carries
   `tools/figures/player/player.js`, `loader.js`, or
   `THIRD-PARTY-LICENSES.txt`, naming the file and the remedy
   (`praetorctl adopt` removes it; the docs build fetches the release asset).
   `internal/adopt/codespell.go` no longer proposes `codespell` exceptions for
   the figure player bundle.

## Consequences

### Positive

- Adopters need zero `codespell` or ESLint exceptions for the interactive
  figure player bundle, upholding `exceptions-never-tiers`.
- Adopter repositories stop carrying ~245 kB of minified third-party JavaScript
  while pinning the exact player bytes via SHA-256 in
  `tools/figures/player.lock.json`.

### Negative / Trade-offs

- Adopter documentation builds render figures as static SVGs (with one stated
  warning line) until the first engine release that carries
  `figures-player.tar.gz`, when pinned to an unreleased engine commit, or when
  building without network access to GitHub releases.

### Neutral

- ADR-0016 sections 1 and 3–6 remain in force unchanged.
- The canonical engine repository continues to build, commit, and publish
  `tools/figures/player/` directly without `tools/figures/player.lock.json`.

## Verification & Compliance

- `go test ./tools/figures/... ./internal/managedasset ./internal/adopt ./cmd/standardsctl`
- `python3 -m pytest -q tools/figures/test_mkdocs_hook.py`
- `actionlint .github/workflows/release-binaries.yml`
- `standardsctl audit`
