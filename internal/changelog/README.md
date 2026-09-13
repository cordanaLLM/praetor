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
an error and retains recovery evidence. After complete cleanup the journal is
removed and the directory synced. An error does not prove publication failed.

Inputs, output and the journal use the shared 1 MiB snapshot limit. The operation
has a ten-second deadline. Cooperating renderers serialize; external writers can
still race the final content check and filesystem operation. Inspect the journal
and current files before reconciling edits or recovering a failed filesystem sync.
