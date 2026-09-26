// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// One immutable catalog owns Praetor's machine-readable documentation surfaces:
// docs/llms.txt, docs/llms-full.txt, and the landing-page lines that describe
// them. The verifier renders both files from this data and binds every entry to
// a repository source; no network result is part of the pull-request gate.
const POLICY_SOURCE = ".standards.yaml";
const RULESET_SOURCE = ".github/rulesets/main.json";
const MACHINE_REFERENCE_SOURCE = "docs/llms-full.txt";

export const catalog = Object.freeze({
  version: 1,
  pagesBase: "https://cordanallm.github.io/praetor/",
  machineDocsClaims: Object.freeze([
    Object.freeze({
      path: "README.md",
      source: MACHINE_REFERENCE_SOURCE,
      text: "| **Dual-Surface Documentation** | Material-for-MkDocs human UI paired with a concise `/llms.txt` index and source-bound `/llms-full.txt` compatibility map. | `make docs-lint` catalog parity |",
    }),
    Object.freeze({
      path: "docs/index.md",
      source: MACHINE_REFERENCE_SOURCE,
      text: "- [`/llms-full.txt`](llms-full.txt): Source-bound compatibility map for current repository authorities; policy values remain in those sources instead of being copied into a stale second specification.",
    }),
  ]),
  llmsSections: Object.freeze([
    Object.freeze({
      title: "Core Governance & Invariants",
      links: Object.freeze([
        Object.freeze({ label: "HISS Formal Specification", path: "standards/hiss-spec/", source: "docs/standards/hiss-spec.md", description: "Complete specification of HISS-01 through HISS-21 invariants." }),
        Object.freeze({ label: "Model Routing & Capacity Arbiter", path: "standards/model-routing-and-fanout/", source: "docs/standards/model-routing-and-fanout.md", description: "Orchestration and fallback cascade for frontier and open models." }),
        Object.freeze({ label: "Agent Hooks & Operating Harness", path: "guides/agent-hooks/", source: "docs/guides/agent-hooks.md", description: "Canonical operating instructions and native client hook coverage." }),
        Object.freeze({ label: "Archetype Authoring", path: "guides/archetype-authoring/", source: "docs/guides/archetype-authoring.md", description: "Archetype authoring, lattice joins, and profile manifests." }),
        Object.freeze({ label: "Developer Onboarding", path: "guides/onboarding/", source: "docs/guides/onboarding.md", description: "Developer setup, Git hooks, and environment bootstrapping." }),
        Object.freeze({ label: "Architectural Decision Records", path: "adr/", source: "docs/adr/README.md", description: "ADR directory and architectural decision history." }),
      ]),
    }),
    Object.freeze({
      title: "CLI Reference & Tooling",
      links: Object.freeze([
        Object.freeze({ label: "Fast Repository Adoption", path: "adoption/", source: "docs/adoption.md", description: "One-step repository onboarding through the CLI, MCP, and hosted automation." }),
        Object.freeze({ label: "Universal Dogfooding & Public Benchmarks", path: "dogfooding/", source: "docs/dogfooding.md", description: "Self-governance auditing and dry-run benchmarking against non-owned public repositories." }),
        Object.freeze({ label: "API Reference", path: "wiki/API-Reference/", source: "docs/wiki/API-Reference.md", description: "Existing command and service reference for praetorctl and standardsctl." }),
        Object.freeze({ label: "Operational Sync & Branch Protection", path: "guides/operational-sync/", source: "docs/guides/operational-sync.md", description: "Declarative reconciliation of hosted rules, labels, and branch protection." }),
      ]),
    }),
    Object.freeze({
      title: "Community & Funding",
      links: Object.freeze([
        Object.freeze({ label: "Polar.sh Issue Bounties", path: "sponsoring/", source: "docs/sponsoring.md", description: "Feature bounties and contributor rewards split via Polar.sh MoR." }),
        Object.freeze({ label: "Monetization Strategy", path: "monetization/", source: "docs/monetization.md", description: "In-IDE passive monetization (Idlen) and enterprise licensing framework." }),
      ]),
    }),
  ]),
  llmsFull: Object.freeze({
    title: "Praetor Source-Bound Machine Reference",
    notice: "Compatibility endpoint: current policy values stay in their authoritative repository sources and are not copied here. Checked-in desired state does not certify live hosted state.",
    authorities: Object.freeze([
      Object.freeze({
        label: "Effective governance manifest",
        source: POLICY_SOURCE,
        description: "Repository identity, enabled facets, policy inputs, and local governance configuration.",
      }),
      Object.freeze({
        label: "Checked-in branch ruleset",
        source: RULESET_SOURCE,
        description: "Declarative desired branch rules and required status contexts; query the forge for live enforcement.",
      }),
      Object.freeze({
        label: "Go toolchain directive",
        source: "go.mod",
        description: "Go language version authority for the module and its build.",
      }),
      Object.freeze({
        label: "Documentation workflow",
        source: ".github/workflows/praetor-docs.yml",
        description: "Hosted job that runs the documentation gate under the required status context.",
      }),
      Object.freeze({
        label: "Canonical agent harness",
        source: "AGENTS.md",
        description: "Agent operating rules and text-register routing compiled into vendor projections.",
      }),
      Object.freeze({
        label: "HISS specification",
        source: "docs/standards/hiss-spec.md",
        description: "Reviewed invariant definitions and enforcement expectations.",
      }),
      Object.freeze({
        label: "Operational sync contract",
        source: "docs/guides/operational-sync.md",
        description: "Boundary between checked-in desired state and live forge reconciliation.",
      }),
      Object.freeze({
        label: "Documentation governance contract",
        source: "docs/guides/documentation-governance.md",
        description: "Machine-documentation catalog ownership, deterministic parity, source binding, and local verification commands.",
      }),
    ]),
  }),
});
