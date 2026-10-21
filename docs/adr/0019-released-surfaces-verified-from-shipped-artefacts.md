# ADR-0019: Released Surfaces Verified from Shipped Artefacts

## Status

Accepted — 2026-10-21.

## Context

A successful publishing workflow does not prove that a consumer can use what
shipped. A tag pattern can miss a release tag entirely, leaving a published release
with no assets and no failed build. Source-tree tests cannot detect that outcome.

## Decision

1. **Exercise the shipped surface.** A release or other published surface is
   verified by fetching and checking its published artefacts, after publication.
   Workflow status and source-tree tests alone are insufficient evidence.
2. **Prove the check refuses defects.** Every check has a planted negative case
   and a clean twin. Its failure names the release, artefact or surface that failed.
3. **Declare asset-less releases.** Zero assets fail unless the caller explicitly
   declares that release asset-less. The result records that declaration.
4. **State verification limits.** Downloads have byte, count and time bounds.
   Checksums verify integrity; signatures require a configured verifier. An
   available signature without one is reported as “not verified”, never as passed.
5. **Read the fleet's manifest names.** One checksum manifest per release, named `SHA256SUMS`,
   `<name>-SHA256SUMS`, `checksums.txt` or `<name>_checksums.txt`; every other asset except
   signature and attestation files (`.bundle`, `.sig`, `.asc`, `.pem`, `.sigstore`,
   `.sigstore.json`) must be listed in it. Several manifests fail by name.

## Consequences

### Positive

- A missing publication trigger, missing asset or corrupted download fails a
  consumer-facing check with a named cause (HISS-15 and HISS-20).
- Adopters can reuse the same bounded check from their own publishing workflows.

### Negative / Trade-offs

- Verification downloads the assets again and consumes network time and bandwidth.
- The release smoke command does not establish signature authenticity or execute
  downloaded programs; published surfaces need checks appropriate to their contract.

### Neutral

- Existing release preparation remains `standardsctl release --version=vX.Y.Z`.

## Verification & Compliance

`standardsctl release smoke --repo=OWNER/NAME --tag=TAG` checks release assets.
`.github/workflows/release-smoke.yml` builds the command from a pinned engine
revision and checks the caller's release. `internal/releasesmoke/smoke_test.go`
contains defective releases and clean twins; transport bounds are exercised by
`internal/forge/release_assets_test.go`.

See [the releasing guide](../guides/releasing.md#verify-shipped-release-assets).
