# Local context review candidates

`standardsctl context-optimize` reduces redundant bytes in an explicitly selected
set of local rules, skills, or memory files. It produces a review candidate; it
does not edit sources, activate instructions, or change machine-global settings.

Analyze selected files without writing contents anywhere:

```sh
standardsctl context-optimize --root "$PWD" AGENTS.md CLAUDE.md .codex/rules.md
```

Write a candidate to a **new** directory under an existing parent:

```sh
standardsctl context-optimize --root "$PWD" \
  --output-dir ../praetor-context-review \
  AGENTS.md CLAUDE.md .codex/rules.md
```

For files under a home directory, an explicit new destination such as
`~/.local/state/praetor/context-packs/review-1` is allowed when its parent already
exists. Source names remain relative to `--root`. No directories are scanned and
no additional files are discovered. Put flags before the source arguments.

The private directory contains `context.md` and `manifest.json`. The manifest
records every source path, SHA256, byte count, retained source and reason. It also
records exact byte ranges of retained documents in the pack, with all source
aliases. Keep the manifest with the candidate when reviewing its loading scopes.
The directory is created with mode 0700 and files with mode 0600, subject to a
more restrictive process umask. Existing output is always rejected.

Two reductions are supported:

- Exact duplicate file contents share one retained document. Filenames alone
  never establish duplication; different `SKILL.md` contents remain separate.
- A selected root `AGENTS.md` can replace selected vendor projections only when
  the current `compiler.Transpiler.CompileContent` reproduces each projection
  exactly at its declared target path. Drift, whitespace differences, nested
  unrelated files, and unverifiable projections are retained. Vendor outputs
  continue to belong to `compile-context`.

Retained document bytes are copied without prose rewriting or whitespace changes.
Pack separators add bytes. `input_bytes` sums all selected file sizes;
`payload_bytes` sums retained document sizes; `pack_bytes` measures the actual
`context.md`, including separators. `saved_bytes = input_bytes - pack_bytes` may
be negative. Manifest storage is excluded. These are byte measurements, without
token estimates or claims of per-turn savings. Selecting several alternative
vendor contexts does not establish that an agent normally loads them together.

Combining documents can change instruction scope, precedence, and repetition.
Review is required before using a pack. There is no semantic-equivalence claim or
automatic apply command; canonical policy changes still belong in `AGENTS.md`
and vendor outputs must be generated with `compile-context`.

Analysis accepts 1–64 unique, clean relative paths, at most 1 MiB per file and
8 MiB total, with 4096-byte paths and 128 components. It rejects symlinks,
non-regular files, non-UTF-8/NUL contents, unstable reads, cancellation, and every
limit overflow without returning a partial plan.

"Rejects symlinks" is bounded by the confinement root, and the boundary matters. The root the
caller names must itself be a real directory -- handing in a symlink is naming one directory and
being given another. Every component *below* that root is walked strictly, so a symlink
introduced inside a repository under audit is refused rather than followed. The path *to* the
root is resolved instead of rejected: macOS ships `/var` and `/tmp` as symlinks, so refusing a
symlinked ancestor refused every path under the platform's own temporary directory and left 30
of 56 packages unable to run there at all (#109). An attacker holding `/var` does not need a
symlink.

Each operation carries a
30-second context budget, checked between local filesystem operations. Unix
source opens are nonblocking to reject FIFO replacement; a stalled kernel or
filesystem operation cannot be forcibly interrupted by a Go context.

Before writing, every selected source hash is rechecked. Invalid inputs, stale
snapshots, output/source overlap, and existing output fail before creation.
Write failures trigger removal of files created for the incomplete candidate;
cleanup errors are returned. Source contents never appear in normal reports.
Pack contents remain sensitive and should stay local unless explicitly reviewed.

The MCP tool `standards_context_analyze` accepts `sources` (a required array of
relative paths) and optional `root` (defaults to the server root). It uses the same
analyzer and returns metadata only. It rejects write arguments; repository-root
confinement follows the server's existing `--allow-outside-root` policy.

```json
{"sources":["AGENTS.md","CLAUDE.md",".codex/rules.md"]}
```

`python3 scripts/dev_mcp.py probe` exercises the real tool over stdio, including
compiler projections, changed policy, invalid paths, and exact/overflow limits.
