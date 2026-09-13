# Infrastructure development environments

Status: researched strategy and implementation plan. Backend selection, VM
provisioning, and cluster admission described here are not implemented. The
[sandbox research](../research/infrastructure-sandboxes.md) records the upstream
contracts and limitations checked on 13 September 2026.

## One matrix, separate tooling and execution requirements

Extend the existing selected profile/facet matrix with task-level execution
requirements. Keep the editor/tooling devcontainer small. A test can request a
disposable guest or cluster without installing its server stack in every editor
image. A Kubernetes repository does not imply that every lint job needs a cluster.

| Task | Tooling selected by the matrix | Required test environment |
| --- | --- | --- |
| Kubernetes YAML, Helm and Kustomize validation | Only the selected renderers, schema validators and policy checks | Container or equivalent admitted user-space runner |
| Kubernetes API/controller integration | Selected cluster client and test tools | Disposable kind/k3d cluster, with actual API readiness and declared CNI/storage scope |
| k3s installation, upgrade or OS configuration | Ansible/k3s test tooling as required | Isolated guest with the required init system, kernel features, disk and network behavior |
| Ansible role tests | Pinned controller dependencies and Molecule scenario | Container for user-space roles; guest for host/boot/module tests; explicit reboot capability when needed |
| Terraform/OpenTofu validation and mocks | Explicit engine and provider versions | Container; preserve the distinction between mocked and real provider calls |
| Terraform/OpenTofu integration | Same selected engine plus admitted provider access | Disposable test infrastructure and its separately tracked cleanup |
| GPU or special device tests | Selected device userspace/toolchain | Independently qualified device-capable runner |

These are proposed task classes, not newly accepted manifest keys or commands.
Do not add ignored configuration and call it support. Every class needs a strict
parser, planner behavior, execution adapter and evidence before promotion.

## Reuse and gaps in current Praetor

| Existing seam | Current behavior | Extension needed |
| --- | --- | --- |
| `internal/config/devcontainer_features.go` | Bounded, deterministic feature union from selected pinned catalog artifacts; conflicting options fail | Resolve separate environment requirements with the same source provenance |
| `internal/devcontainer` and `internal/adopt/devcontainer.go` | Prepare and verify portable CLI/tooling bundles | Preserve workspace generation; report external test-environment requirements separately |
| `.config/archetypes/gitops-infra.yaml` | Kubernetes/Helm/minikube and Terraform feature references plus declared linters | Split optional task/tool requirements; add tested k3s, Ansible and OpenTofu coverage |
| `internal/flavor/definitions.go` | Infrastructure detection from selected Helm/Kustomize files; kubectl/Helm toolchain checks | Evidence-based detection and engine-specific checks, including Terraform-only projects |
| `internal/needs` | Dependency demand and framework capability evidence | Aggregate missing environment support without treating a framework package as a qualified runtime |
| `internal/runner/matrix.go` | Configured OS/architecture/GPU runner labels, including a default fallback | Reuse routing identity, but require capability admission instead of treating fallback labels as execution proof |
| `.config/lefthook/scripts/sandbox.py` | Exact-commit disposable Docker gate with named-container cleanup | Adapt to a shared execution contract; it currently has no VM selection, general resource admission, or explicit network policy |
| `internal/repairrun`, `internal/dogfood`, `internal/util` | Bounded execution, retained attempts, fixtures and process helpers | Reuse task identities, evidence, bounded commands and replay instead of creating another agent loop |
| `internal/gc`, `internal/worktree` | Explicit released-path collection and protected worktree removal | Link external resources to the planned lease/fencing lifecycle before automatic reclamation |

The `agent:sandboxed` facet and installed command hooks do not establish a
hypervisor, cgroup or network isolation boundary. Generated tool configuration,
a successful image build, guest readiness and a passing test are distinct facts.

## Backend admission and configuration

One shared planner should consume the exact task/source identity, effective
policy, typed requirements and fresh backend observations. CLI, MCP, IDE, bot,
container and private configuration forks consume that same result.

Eligibility precedes price: discard candidates that lack required capabilities,
isolation, resource capacity, provenance or implementation. Rank the remaining
candidates using configurable measured cost, queue time, startup time and task
duration. Record the chosen candidate and rejection reasons. A missing probe is
`unknown`; a missing adapter is `unsupported`. Neither can become a successful
fallback to a weaker environment.

Configurations must cover backend enablement and preference; OS/architecture and
device requirements; CPU, memory, disk, I/O, PID and concurrency budgets; timeouts;
network/credential scope; image/kernel/rootfs references; retention; and locality.
Extend the [management policy design](../research/compact-management-data.md)
and [lifecycle policy](lifecycle-management.md), retaining source precedence and
stricter safety caps. Repository preferences cannot enlarge an inherited limit.
Keep backend compatibility data versioned separately from executables, with the
existing proposed signed-data distribution and rollback contract.

Firecracker is a candidate for short, disposable CPU-only Linux tests whose
requirements fit its supported guest lifecycle and devices. Qualify QEMU or
Cloud Hypervisor when the task needs capabilities the Firecracker adapter cannot
provide. A pod-sandbox runtime such as Kata is a deployment option where a
configured Kubernetes platform already owns that runtime. None is mandatory.

Minimizing footprint means measuring the complete attempt: provisioned/peak
guest RAM, VMM and network overhead, image/cache storage, cold and warm startup,
test runtime, retained artifacts and cleanup cost. Guest kernel and application
memory are part of guest RAM, not free space supplied by a small VMM. Reuse
immutable images and explicitly scoped caches; use fresh writable state and
credentials for every attempt. Snapshot reuse requires compatible assets and
evidence that task data and identities are reset.

## Ownership and lifecycle

Praetor should declare requirements, submit admitted work, verify observations and
retain task evidence. The configured workspace service owns editor workspaces;
the scheduler/runtime owns placement, VM/network resources and release. For
GitOps deployments, host preparation, worker labels, RuntimeClasses and controller
configuration stay in the infrastructure repository. Do not introduce a second
workspace control plane or have an IDE provision arbitrary cluster resources.

Every attempt needs a stable owner/run ID, exact source and policy digests,
backend/version, guest assets, admission/lease identity, bounded command and
artifact manifest. Track execution and cleanup outcomes independently:

```text
requirements -> plan -> admission -> provision -> readiness -> execute
                                                       -> verify -> release -> cleanup readback
```

Cancellation, timeout, failed boot and an agent crash all enter reconciliation.
An expired heartbeat or missing PID is insufficient authority to destroy an
environment. Reuse the [lifecycle management stages](lifecycle-management.md):
fence stale owners, confirm release, preserve unknown resources and retained
evidence, and read back deletion through the owning provider. Never report an
attempt fully reclaimed because the client process exited.

Lefthook and task verification remain required inside admitted source workspaces.
The outer executor verifies results against the exact source and settings;
native agent Stop events request reconciliation rather than owning VM deletion.

## Delivery and dogfood acceptance

1. **Requirements and planning:** add strict catalog decoding, capability union,
   conflict handling and a read-only planner with shared CLI/MCP output. Test
   mixed Kubernetes/Ansible/Terraform repositories, explicit engine selection,
   unrelated profile exclusion, unknown backends, stale observations and all
   resource bounds. A default runner label must not satisfy an unknown platform
   or a missing guest/device capability. Do not report a planned environment as runnable.
2. **Existing execution:** adapt the current Docker/repair execution paths to
   the shared attempt record and explicit isolation/resource policy. Prove
   cancellation, evidence readback and complete owned-resource cleanup.
3. **First VM canary:** select reviewed release-pinned VMM, kernel and rootfs
   artifacts. On an admitted KVM worker, run a small Linux fixture, then an
   Ansible convergence/idempotence case. Exercise denied admission, altered
   assets, boot failure, timeout, controller restart and cleanup readback.
   Qualify reboot separately on a backend that implements it.
4. **Infrastructure suites:** add k3s bootstrap/upgrade, controller/API tests,
   Ansible host roles, and mocked Terraform/OpenTofu modules. Add real provider
   tests only with explicit disposable target and cleanup authority. Evidence
   must record the tested OS/kernel/CNI/storage/provider scope.
5. **Deployment and efficiency:** replay the same cases locally and through the
   configured GitOps scheduler, then compare complete attempt costs. Promote
   only proven backend/host/version combinations; keep missing capabilities in
   dogfood discovery and the shared state ledger.

No host virtualization configuration, cluster runtime or VM was installed by
this strategy change. First runtime implementation remains open and requires
the capability and ownership contract above.
