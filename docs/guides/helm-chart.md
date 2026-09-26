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

Two name changes can reach an existing release. Check both before you run `helm upgrade`.

**The ServiceAccount moves wherever `serviceAccount.name` was left at its default.** Earlier
versions defaulted the name to `praetor-sa` outright, which is why two releases collided. That
default is now empty, and what an empty name renders depends on `serviceAccount.create`:

- `create: true`: the account is renamed to `<release>-praetor`. Helm deletes the old
  `praetor-sa` object and creates the new one.
- `create: false`: the pod moves to the namespace's `default` account, and nothing warns you. The
  previous chart pointed such a pod at `praetor-sa`. The ServiceAccount admission controller turns
  away a pod whose account does not exist, so any install of this kind that ran had `praetor-sa`
  created outside the chart. After the upgrade the pod no longer uses it. The account itself is
  left in place, but the pod loses everything it got through that account: RBAC bindings,
  SecurityContextConstraints and any other per-account grant. It also loses the account's image
  pull secrets. The admission controller copies an account's pull secrets onto a pod that declares
  none of its own, so unless `imagePullSecrets` is set in the chart values, a pull from a private
  registry now fails.

The remedy is the same for both: name the account.

```bash
helm upgrade praetor deploy/helm/praetor --set serviceAccount.name=praetor-sa
```

`TestServiceAccountCreateTrueHonoursExplicitName` and
`TestExplicitServiceAccountNameWinsWithoutCreation` in `internal/deploychart/chart_test.go` pin
this upgrade for `create: true` and `create: false` respectively. A release that already set its
own `serviceAccount.name` is not affected. The admission controller's behaviour is documented in
the Kubernetes reference, [ServiceAccount admission
controller](https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/#serviceaccount-admission-controller).

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
