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
    
    FORK["Managing Fork (lusoris/praetor)"]
    GIT_PROVIDER["Git Provider (GitHub Enterprise / Forge)"]
    K8S_ARC["Kubernetes Cluster (Actions Runner Controller)"]
    GITOPS["GitOps Controller (ArgoCD / Helm)"]

    DEVELOPER -->|"invokes commands / verifies PRs"| PRAETOR
    AGENT -->|"reads AGENTS.md / runs checks"| PRAETOR
    FORK -->|"harvests requirements & dogfoods"| PRAETOR
    PRAETOR -->|"enforces branch protection & checks"| GIT_PROVIDER
    PRAETOR -->|"dispatches ephemeral job scale sets"| K8S_ARC
    GITOPS -->|"reconciles distroless OCI deployments"| PRAETOR
```

---

## 2. Level 2: Container Diagram

The Container diagram illustrates the runtime modules within the Praetor binary, daemon services, and configuration inputs.

```mermaid
flowchart TD
    subgraph REPO_SPACE["Repository Space"]
        STANDARDS_YAML[".standards.yaml (Manifest)"]
        FLEET_YAML[".config/fleet.yaml (Fleet Policy)"]
        AGENTS_MD["AGENTS.md (Canonical Instructions)"]
        SUBAGENTS[".agents/agents/*.md (Subagent Personas)"]
    end

    subgraph PRAETOR_CLI["Praetor Core Engine (Go Binary / Container)"]
        GATING_SVC["Gating Engine (Anti-Direct-Merge)"]
        COMPILER_SVC["Compiler & Transpiler Engine"]
        HISS_SVC["HISS Invariant & AST Scanner"]
        RUNNER_SVC["Hierarchical Runner Matrix Router"]
        HEALTH_SVC["Container HTTP Health Probe (:8080)"]
    end

    subgraph ARTIFACT_OUTPUTS["Generated Projections"]
        VENDOR_AGENTS[".claude/, .gemini/, .codex/, .github/"]
        SARIF_OUTPUT["SARIF Diagnostic Distillation"]
        OCI_IMAGE["ghcr.io/cordanallm/praetor (Distroless)"]
    end

    STANDARDS_YAML --> GATING_SVC
    FLEET_YAML --> RUNNER_SVC
    AGENTS_MD --> COMPILER_SVC
    SUBAGENTS --> COMPILER_SVC

    GATING_SVC --> HISS_SVC
    COMPILER_SVC --> VENDOR_AGENTS
    HISS_SVC --> SARIF_OUTPUT
    HEALTH_SVC --> OCI_IMAGE
```

---

## 3. Level 3: Component Diagram (Gating Engine)

The Component diagram details the internal workflow of the Anti-Direct-Merge Gating Pipeline (`internal/gating/pipeline.go`).

```mermaid
flowchart LR
    INPUT["Merge / Commit Candidate"] --> PREFETCH["1. Prefetch & Lockfiles\n(go mod verify / download)"]
    PREFETCH -->|"Valid"| AST_SCAN["2. HISS-16 Invariant Scan\n(AST, NASA Rule 4 <= 60 LOC)"]
    AST_SCAN -->|"0 Infractions"| RACE_TEST["3. Race-Detector Tests\n(go test -race in worktree)"]
    RACE_TEST -->|"Pass"| RECEIPT["4. Exit-0 Receipt & Gate Admit\n(Eligible for Fast-Forward Merge)"]
    
    PREFETCH -->|"Checksum Mismatch"| REJECT["Rejected Disposition (Exit 1)"]
    AST_SCAN -->|"Violations Detected"| REJECT
    RACE_TEST -->|"Data Race / Test Failure"| REJECT
```
