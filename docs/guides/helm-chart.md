# Helm Chart

The chart in `deploy/helm/praetor` installs the Praetor container: a Deployment, a Service for
its HTTP probes, and optionally the ServiceAccount the pod runs as.

## Install

```bash
helm install praetor deploy/helm/praetor
helm install praetor-canary deploy/helm/praetor   # a second release in the same namespace
```

Both releases coexist. Every object is named `<release>-praetor`, so nothing collides; the render
tests in `internal/deploychart/chart_test.go` assert exactly that and fail if a name ever repeats.

## Names

| Value | Effect |
| :--- | :--- |
| *(none)* | Objects are named `<release>-praetor`. |
| `nameOverride` | Replaces `praetor` in the generated name and in `app.kubernetes.io/name`, giving `<release>-<nameOverride>`. |
| `fullnameOverride` | Replaces the whole generated name; the release name is not prefixed. |

`fullnameOverride` wins over `nameOverride`. The helpers that compute these names live in
`deploy/helm/praetor/templates/_helpers.tpl`.

## Service account

| `serviceAccount.create` | `serviceAccount.name` | Rendered |
| :--- | :--- | :--- |
| `true` (default) | empty (default) | The chart creates `<release>-praetor` and the pod runs as it. |
| `true` | set | The chart creates an account under that name and the pod runs as it. |
| `false` | empty | No account is created; the pod runs as the namespace's `default` account. |
| `false` | set | No account is created; the pod runs as the named account, which you must create yourself. |

Both the pod and the created ServiceAccount set `automountServiceAccountToken: false`. The binary
has no Kubernetes client — `go.mod` requires only `gopkg.in/yaml.v3` — so an API token would be a
credential it cannot use.

### Upgrading a release installed before the rename

Two renames can reach an existing release. Check both before you run `helm upgrade`.

**The ServiceAccount moves on every release.** Earlier versions named the account `praetor-sa`
outright, which is why two releases collided. The release-scoped default renames it, and a
`ServiceAccount` rename means the old object is deleted and a new one created. To keep the
existing account across the upgrade, name it:

```bash
helm upgrade praetor deploy/helm/praetor --set serviceAccount.name=praetor-sa
```

`TestServiceAccountCreateTrueHonoursExplicitName` in `internal/deploychart/chart_test.go` pins that
path.

**The Deployment and Service move where an override is set.** `nameOverride` and
`fullnameOverride` shipped in `values.yaml` but no template read them, so a release that set one
rendered as though it had not: every object stayed `<release>-praetor`. They now steer every
generated name, including the two objects that carry traffic. Render the values you install with
and compare the result against the live release before upgrading:

```bash
helm template alpha deploy/helm/praetor --set nameOverride=alt   # alpha-alt, not alpha-praetor
```

Helm sees a differently named object, so it deletes the old Deployment and Service and creates new
ones: the pods restart, and the in-cluster DNS name of the Service moves with it. `nameOverride`
also changes `app.kubernetes.io/name`, which the Deployment selector matches on, so the new
Deployment selects a different label set than the old one did.

Clearing both values reproduces the previous names exactly, so a release that never set them is
untouched by this half; `TestDefaultValuesRenderServiceAccountTheDeploymentUses` and
`TestTwoReleasesShareNoResourceName` pin the default render, and
`TestNameOverrideKeepsReleaseScope` pins the overridden one. A release that did set an override
either accepts the rename or clears the value before upgrading.

## Private registries

`imagePullSecrets` is rendered on the pod spec when it is non-empty:

```yaml
imagePullSecrets:
  - name: ghcr-credentials
```

## Probes and metrics

The container serves `/healthz`, `/livez` and `/readyz` (`internal/container/health.go`), which the
liveness and readiness probes in `values.yaml` use. There is no metrics endpoint, so the chart
ships no ServiceMonitor; a scrape target would have nothing to read.

## Verifying a change

```bash
helm lint deploy/helm/praetor
helm template praetor deploy/helm/praetor
go test ./internal/deploychart/
```

The Go tests drive the real `helm` binary and skip with a stated reason where it is absent, so the
same command is meaningful on Linux, macOS and Windows.
