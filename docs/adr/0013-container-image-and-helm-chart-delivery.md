# ADR-0013: Container Image and Helm Chart Delivery

## Status

Accepted — 2026-09-26. Supersedes decision 4 of ADR-0012 (container delivery); ADR-0012's other
decisions stand.

## Context

ADR-0012 decision 4 records the production image as `build/package/Dockerfile` and states that no
workflow builds or publishes it. The chart's default image reference was
`ghcr.io/cordanallm/praetor:v1.0.0`, a tag nothing had pushed, so every `helm install` of
`deploy/helm/praetor` ended in `ImagePullBackOff`.

Two Dockerfiles described one image. `build/package/Dockerfile` compiled `praetorctl` from source in
a builder stage. The root `Dockerfile`, which the `go-service` flavor requires
(`internal/flavor/definitions.go`, `GoServiceFlavor.RequiredTemplates`), copied a `praetor` binary no
build produces. Meanwhile the release job already compiles `praetorctl` for `linux/amd64` and
`linux/arm64` (`.goreleaser.yaml`, `builds`), signs and attests what it releases, and publishes the
GitHub release last so an immutable release accepts every asset (#43,
[releasing guide](../guides/releasing.md)). HISS-19 counts the second Dockerfile as a second
implementation of one behaviour.

## Decision

1. **One image path.** The root `Dockerfile` defines the image and `build/package/Dockerfile` is
   removed. GoReleaser's `dockers_v2` block builds the image in the release job from the
   `praetorctl` binaries the same run compiled, so the image carries the binary the release
   archives carry. The runtime stays the digest-pinned `gcr.io/distroless/static-debian13:nonroot`,
   running as `65532:65532` with `praetorctl serve -addr=:8080`. The OCI metadata (title, source,
   licenses, version, revision) is written once in the `dockers_v2` block, which applies it as
   image labels and as manifest-list annotations; the `Dockerfile` carries no second copy.
2. **Image publication.** buildx pushes a `linux/amd64` and `linux/arm64` manifest list to
   `ghcr.io/cordanallm/praetor:<version>`, where `<version>` is the release tag without its `v`,
   with an SBOM attestation and buildx's provenance attestation. The `docker_signs` block signs the
   pushed digest keyless with cosign. The release job reads that digest from
   `dist/artifacts.json`, verifies the signature on it against the workflow identity, and fails
   when a platform the digest holds lacks the SBOM or the provenance attestation.
3. **Chart publication.** The release job runs `helm lint` before anything is pushed, packages the
   chart with `--version` and `--app-version` set to the release version, pushes it to
   `oci://ghcr.io/cordanallm/charts`, signs the digest `helm push` reports and verifies that
   signature. Every image and chart step runs before the release is published, so a failure in one
   leaves the release a draft.
4. **Default image tag.** An empty `image.tag` means the chart's `appVersion`
   (`praetor.image` in `deploy/helm/praetor/templates/_helpers.tpl`), so a published chart pulls
   the image released with it. A set `image.tag` wins. An empty `image.repository` fails the
   render rather than producing an image reference the cluster rejects at pull time.
5. **Names.** GHCR accepts lowercase names only, so the image and chart repositories are the
   lowercased `.standards.yaml` identity. `.goreleaser.yaml`, `deploy/helm/praetor/values.yaml` and
   the release job's `IMAGE` and `CHART_REPOSITORY` each write it down.

The chart settings ADR-0012 decision 4 describes (read-only root filesystem, memory-backed scratch
volume, probes on `/livez` and `/readyz`) are unchanged, and GitOps wiring stays operator data in
the operational fork.

## Consequences

### Positive

- `helm install praetor oci://ghcr.io/cordanallm/charts/praetor --version <version>` pulls an image
  that exists and was built from the same commit.
- The image, the chart and the release archives come from one build, and each published artifact is
  signed and its signature verified before the release is published.

### Negative / Trade-offs

- GHCR creates a package private on its first push. The operator makes `praetor` and
  `charts/praetor` public once, in the organisation's package settings, before anonymous pulls work.
- The build context holds only prebuilt binaries, so `docker build .` in a checkout has nothing to
  copy; `goreleaser release --snapshot --clean` builds the image locally.
- The image and chart are pushed before the release is published. When a later step fails, their
  tags stay on GHCR next to a draft release, and a rerun of the job pushes over them.
- The in-tree chart keeps placeholder versions (`deploy/helm/praetor/Chart.yaml`), so an install from
  the directory needs `--set image.tag=<released version>` until a release of its `appVersion`
  exists.
- Keyless signing and verification of the image and chart, and the attestation read, run against
  GHCR for the first time on a real tag push; no local run reaches GHCR with the workflow's
  identity.

### Neutral

- SLSA Build Level 3 is still not claimed (#330). buildx's provenance attestation describes the image
  build; the signed in-toto statement over `checksums.txt` still covers the archives.

## Verification & Compliance

- `internal/deploychart/chart_test.go`: `TestChartPassesHelmLint`,
  `TestDefaultImageTagIsTheChartAppVersion`, `TestExplicitImageTagWinsOverTheAppVersion`,
  `TestEmptyImageRepositoryFailsTheRender` and `TestPackagedAppVersionDrivesTheImageTag`, which
  packages the chart the way the release job does.
- `internal/forge/workflow_guard_test.go`: `TestContainerReferencesFollowTheManifestIdentity`,
  `TestContainerReferencesFailForAnotherIdentity` and `TestContainerReferenceViolationsBoundaries`
  hold decision 5.
- `internal/forge/release_flow_test.go`: `TestEngineReleaseFlowVerifiesImageAndChartBeforePublishing`
  and `TestContainerFlowViolationsSyntheticShapes` hold the order in decisions 2 and 3, the
  attestation check included.
- The release job runs `goreleaser check` before it builds, so an invalid `dockers_v2` or
  `docker_signs` block fails before anything is pushed.

## References

- ADR-0012 decision 4 (superseded by this record), ADR-0005.
- Issue #43 (immutable releases), #330 (SLSA level measurement).
