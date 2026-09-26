# Build, release and deployment

These guides cover how a commit becomes an installable version, how the container is
deployed, and how dependency upgrades are tested before they land.

Read [Releasing and release flavors](../releasing.md) first.

- [Releasing and release flavors](../releasing.md): what a `v*` tag sets off and how the
  `bleeding`, `edge`, `latest` and `lts` tags follow it.
- [Helm chart](../helm-chart.md): installing the Praetor container from
  `deploy/helm/praetor`.
- [Canary execution and evidence](../canary-evidence.md): what `praetorctl bump canary`
  plans and the evidence a canary run retains.
- [Universal builder execution status](../universal-builder.md): `praetorctl build` has no
  executable backends yet and refuses every request before writing output.
