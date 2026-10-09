# Changelog recovery

`RenderReleaseContext` validates one complete YAML document per fragment and
publishes a release under a cooperative directory lock. Before publication it
retains `.praetor-changelog-render.json` in the repository root, mode 0600. This
bounded journal records the release identity, rendered section, changelog hashes
and hashes of the exact fragments included in the release.

If cleanup fails after publication, rerun with the same version and date. The
renderer verifies the output hash and removes only unchanged recorded fragments;
new fragments remain for a later release. A missing recorded fragment is an
already-completed cleanup step. Changed output, changed fragments, another
release identity or a missing fragment directory with a pending journal returns
an error and retains recovery evidence. Once the recorded fragments are gone the
renderer leaves an empty `.gitkeep` (`FragmentPlaceholder`) in `changelog.d`, keeping an
existing one unchanged, so the directory survives in version control and
`FragmentDirPresent` answers the same in a fresh clone. A failure to write it also
retains the journal. After complete cleanup the journal is removed and the directory
synced. An error does not prove publication failed (`placeholder_test.go`).

With no pending journal, a render needs at least one fragment. A missing `changelog.d`
or one that holds no fragment returns `ErrNoFragments`, naming which of the two it was,
and writes nothing (`TestPrepareRelease_Negative_NoFragments` in
`internal/release/release_test.go`).

Inputs, output and the journal use the shared 1 MiB snapshot limit. The operation
has a ten-second deadline. Cooperating renderers serialize; external writers can
still race the final content check and filesystem operation. Inspect the journal
and current files before reconciling edits or recovering a failed filesystem sync.

## Fragment schema

Changelog fragments stored in `changelog.d/` are decoded and validated by `decodeFragment`
in `fragments.go`. Each fragment specifies:

- `type`: one of `added`, `changed`, `deprecated`, `removed`, `fixed`, or `security`.
- `title`: one-sentence imperative description of the change.
- `issue`: optional issue reference (digits or `#<digits>`, e.g. `502` or `#502`, or an `owner/repo#n` cross reference). Leading `#` characters are normalised by `NormaliseIssue` in `changelog.go` to prevent duplicate hash prefixes during release rendering.
- `breaking`: optional boolean indicating whether the change introduces breaking changes.
