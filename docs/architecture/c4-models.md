# Praetor Architecture: C4 Models & System Lattice

This document specifies the architectural topology of the Praetor Governance Engine using C4 modeling and Mermaid diagrams.

---

## 1. Level 1: System Context Diagram

The System Context diagram details Praetor's boundaries, autonomous agent interactions, git providers, and downstream cluster infrastructure.

```mermaid
flowchart TD
    DEVELOPER["Software Engineer / Operator"]
    AGENT["Autonomous AI Coding Agent"]
    
    subgraph SYSTEM_BOUNDARY["Praetor Governance Boundary"]
        PRAETOR["Praetor Engine (praetorctl)"]
    end
    
    FORK["Operational Fork (lusoris/praetor)"]
    GIT_PROVIDER["Git Provider (GitHub Enterprise / Forge)"]
    K8S_ARC["ARC Runner Scale Sets\n(operator-owned: fork deploy/arc/)"]
    GITOPS["GitOps Controller\n(operator-owned: fork deploy/k8s/)"]

    DEVELOPER -->|"invokes commands / verifies PRs"| PRAETOR
    AGENT -->|"reads AGENTS.md / runs checks"| PRAETOR
    FORK -->|"harvests requirements & dogfoods"| PRAETOR
    PRAETOR -->|"enforces branch protection & checks"| GIT_PROVIDER
    PRAETOR -.->|"resolves runner routing to scale-set names"| K8S_ARC
    GITOPS -.->|"syncs deploy/helm/praetor"| PRAETOR
```

Dashed edges cross into infrastructure this repository does not ship. The ARC scale sets and the
ArgoCD Application are operator data: `deploy/arc/` and `deploy/k8s/` are owner-only paths
(`ownerOnlyPrefixes` in `internal/operationalsync/overlay.go`) that exist only in the operational
fork, and the engine's `.gitignore` ignores them (`TestOwnerOnlyPrefixesAreIgnoredByTheEngine` in
`internal/operationalsync/owner_only_test.go`). Praetor does not dispatch jobs: `praetorctl audit`
checks that the runner policy resolves (`auditRunnerMatrix` in `cmd/standardsctl/audit.go`), and
this repository's workflows run on GitHub-hosted runners.

---

## 2. Level 2: Container Diagram

The Container diagram illustrates the runtime modules within the Praetor binary, daemon services, and configuration inputs.

```mermaid
flowchart TD
    subgraph REPO_SPACE["Repository Space"]
        STANDARDS_YAML[".standards.yaml (Manifest)"]
        FLEET_YAML[".config/fleet.yaml, .config/orgs/*.yaml\n(optional, operator-owned)"]
        AGENTS_MD["AGENTS.md (Canonical Instructions)"]
        SUBAGENTS[".agents/ (Personas, Skills, Plugin)"]
    end

    subgraph PRAETOR_CLI["Praetor Core Engine (Go Binary / Container)"]
        GATING_SVC["Gating Engine (internal/gating)"]
        COMPILER_SVC["Compiler & Transpiler Engine"]
        HISS_SVC["HISS Invariant & AST Scanner (internal/hiss)"]
        RUNNER_SVC["Hierarchical Runner Matrix Router"]
        CANARY_SVC["Bump Canary (internal/bump)"]
        DISTILL_SVC["SARIF Distiller (internal/lockdown)"]
        HEALTH_SVC["HTTP Health Probes (praetorctl serve, :8080)"]
    end

    subgraph ARTIFACT_OUTPUTS["Generated Projections"]
        VENDOR_AGENTS["CLAUDE.md, .cursor/rules/, .windsurfrules,\n.github/, .gemini/, .codex/, .claude/agents/"]
        SARIF_OUTPUT["Distilled diagnostic summary\n+ full report (.workingdir/evidence/)"]
        OCI_IMAGE["Distroless image (build/package/Dockerfile)\nbuilt locally; no workflow publishes it"]
    end

    STANDARDS_YAML -->|"lockfile check, complexity policy"| GATING_SVC
    STANDARDS_YAML -->|"runners:"| RUNNER_SVC
    FLEET_YAML --> RUNNER_SVC
    AGENTS_MD --> COMPILER_SVC
    SUBAGENTS --> COMPILER_SVC

    GATING_SVC --> HISS_SVC
    COMPILER_SVC --> VENDOR_AGENTS
    CANARY_SVC -->|"wraps failure output as SARIF"| DISTILL_SVC
    DISTILL_SVC --> SARIF_OUTPUT
    HEALTH_SVC -->|"packaged in"| OCI_IMAGE
```

The HISS scanner returns a `ScanReport` to the gate and to `praetorctl audit`; it emits no SARIF.
SARIF enters through `lockdown.DistillSARIF`, which condenses a SARIF log (today the bump canary's
wrapped test failure, `internal/bump/canary.go`) into a bounded summary and writes the full report
under `.workingdir/evidence/`. The gate reads `.standards.yaml` twice: the prefetch stage requires it
and `.standards.lock` to exist, and the HISS stage scans with the function-length limit its policy
resolves to. The Helm chart's default image reference (`deploy/helm/praetor/values.yaml`) names a
`ghcr.io` tag that no workflow publishes (ADR-0012, decision 4).

---

## 3. Level 3: Component Diagram (Gating Engine)

The Component diagram details the internal workflow of the Anti-Direct-Merge Gating Pipeline (`internal/gating/pipeline.go`).

```mermaid
flowchart LR
    INPUT["Merge / Commit Candidate"] --> PREFETCH["1. Prefetch & Lockfiles\n(.standards.yaml + .standards.lock,\ngo mod verify / download)"]
    PREFETCH -->|"Pass"| AST_SCAN["2. HISS Invariant Scan\n(HISS-01/02/04/07/08/09, resolved\nfunction-length limit, baseline ratchet)"]
    AST_SCAN -->|"No new infractions"| SECURITY["3. Security & SCA Scan\n(govulncheck, gosec)"]
    SECURITY -->|"Pass"| FLAVOR["4. Flavor Conformance\n(flavor audit)"]
    FLAVOR -->|"Pass / not applicable"| RACE_TEST["5. Race-Detector Tests\n(go test -race in worktree)"]
    RACE_TEST -->|"Pass / skipped with reason"| RECEIPT["6. Ed25519 Exit-0 Receipt\n(signed stage output)"]

    PREFETCH -->|"Missing lockfile / checksum mismatch"| REJECT["Rejected Disposition (Exit 1)"]
    AST_SCAN -->|"New violations / incomplete scan"| REJECT
    SECURITY -->|"Findings / missing scanner"| REJECT
    FLAVOR -->|"Score below 80% / missing template"| REJECT
    RACE_TEST -->|"Data race / test failure"| REJECT
    RECEIPT -->|"No signing key"| REJECT
```

Stage order and names come from `executeStages` in `internal/gating/pipeline.go`; a dry run skips
stage 5 and mints no receipt. ADR-0012 (decision 3) records the pipeline; ADR-0004's four-stage
description is superseded.
