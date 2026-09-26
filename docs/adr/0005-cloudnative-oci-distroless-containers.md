# ADR-0005: Cloud-Native OCI Distroless Containers & Kubernetes Deployment

## Status

Superseded by [ADR-0012](0012-current-delivery-runner-and-pipeline-state.md) — 2026-09-26 (previously Accepted).

## Context

Deploying Praetor as a fleet governance sentinel and background service across Kubernetes clusters requires lightweight, zero-CVE, secure container images. Running containers as `root` or bundling interactive shells and package managers expands attack surfaces and violates enterprise compliance policies.

## Decision

We mandate cloud-native OCI packaging standards for all Praetor container artifacts:

1. **Multi-Stage Hermetic Build**: Build stage on the `golang:` image whose version equals the `go` directive in the module's `go.mod` — today `golang:1.27-alpine` for Praetor's own image (`docker/dev/Dockerfile`) and the same version in the distroless template shipped to adopters (`templates/go/Dockerfile.distroless.tmpl`) — compiling statically linked binaries (`CGO_ENABLED=0`, `-ldflags="-s -w"`). `go.mod` is the single source of the toolchain version: this clause states the version it currently resolves to rather than mandating one of its own, and `forge.AuditGoToolchain` reports every workflow key or shipped template that falls behind the directive.
2. **Minimal Distroless Runtime**: Production stage based on `gcr.io/distroless/static-debian12:nonroot`, resulting in an immutable image size $< 30$ MB with zero package managers, shells, or CVEs.
3. **Non-Root & Read-Only**: Enforces unprivileged user `65532:65532` (`nonroot:nonroot`) and `readOnlyRootFilesystem: true`, with ephemeral scratch storage mounted on `/tmp` via memory `emptyDir` (tmpfs).
4. **POSIX Signal Draining & Health Probes**: Integrates HTTP probes (`/healthz`, `/livez`, `/readyz`) on port 8080 and traps `SIGTERM`/`SIGINT` for graceful draining.
5. **Declarative GitOps Delivery**: Packaged via Helm chart (`deploy/helm/praetor`) and managed through ArgoCD (`deploy/k8s/application.yaml`).

## Consequences

- **Positive**: Hardened production security posture satisfying CIS Kubernetes benchmarks and SOC2/ISO27001 requirements.
- **Positive**: Fast startup times and minimal cluster resource utilization.
- **Negative**: Debugging containers in production requires ephemeral debug pods or telemetry, as interactive shells are absent.
