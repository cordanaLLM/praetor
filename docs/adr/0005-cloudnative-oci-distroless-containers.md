# ADR-0005: Cloud-Native OCI Distroless Containers & Kubernetes Deployment

## Status
Accepted

## Context
Deploying Praetor as a fleet governance sentinel and background service across Kubernetes clusters requires lightweight, zero-CVE, secure container images. Running containers as `root` or bundling interactive shells and package managers expands attack surfaces and violates enterprise compliance policies.

## Decision
We mandate cloud-native OCI packaging standards for all Praetor container artifacts:
1. **Multi-Stage Hermetic Build**: Build stage using `golang:1.24-alpine` compiling statically linked binaries (`CGO_ENABLED=0`, `-ldflags="-s -w"`).
2. **Minimal Distroless Runtime**: Production stage based on `gcr.io/distroless/static-debian12:nonroot`, resulting in an immutable image size $< 30$ MB with zero package managers, shells, or CVEs.
3. **Non-Root & Read-Only**: Enforces unprivileged user `65532:65532` (`nonroot:nonroot`) and `readOnlyRootFilesystem: true`, with ephemeral scratch storage mounted on `/tmp` via memory `emptyDir` (tmpfs).
4. **POSIX Signal Draining & Health Probes**: Integrates HTTP probes (`/healthz`, `/livez`, `/readyz`) on port 8080 and traps `SIGTERM`/`SIGINT` for graceful draining.
5. **Declarative GitOps Delivery**: Packaged via Helm chart (`deploy/helm/praetor`) and managed through ArgoCD (`deploy/k8s/application.yaml`).

## Consequences
- **Positive**: Hardened production security posture satisfying CIS Kubernetes benchmarks and SOC2/ISO27001 requirements.
- **Positive**: Fast startup times and minimal cluster resource utilization.
- **Negative**: Debugging containers in production requires ephemeral debug pods or telemetry, as interactive shells are absent.
