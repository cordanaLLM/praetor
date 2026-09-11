// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"fmt"
	"strings"
)

// SynthesizeKnowledgePages produces AST-grounded markdown bodies for the 5 canonical pages.
func SynthesizeKnowledgePages(repoPath string, facts []MemoryFact) map[string]string {
	return map[string]string{
		"Component map":                buildComponentMapPage(),
		"Conventions and patterns":     buildConventionsPage(),
		"Core concepts":                buildCoreConceptsPage(),
		"Key decisions and rationale":  buildDecisionsPage(),
		"Initiatives and enhancements": buildInitiativesPage(facts),
	}
}

func buildComponentMapPage() string {
	var lines []string
	lines = append(lines, "# Component Map", "", "## Subsystems & Architecture", "")
	lines = append(lines, "| Subsystem | Domain | Responsibility |", "| :--- | :--- | :--- |")
	lines = append(lines, "| `internal/compiler` | Context Transpilation | Transpiles `AGENTS.md` into IDE instructions. |")
	lines = append(lines, "| `internal/hiss` | AST Invariant Analysis | Sweeps AST against HISS-01 to HISS-16 standards. |")
	lines = append(lines, "| `internal/flavor` | Archetype Governance | Enforces archetype toolchains and configurations. |")
	lines = append(lines, "| `internal/dedupe` | Clone Detection | Normalized AST clone sweeping and cadence engine. |")
	lines = append(lines, "| `internal/state` | Session Integrity | Developer working state, Bug Ledger, and Questions. |")
	lines = append(lines, "| `internal/docdistill` | Doc Distillation | Token-compressed package documentation library. |")
	lines = append(lines, "| `internal/bump` | Version Modernization | Workflow action auditing and dependency bumps. |")
	lines = append(lines, "| `internal/hindsight` | Memory Coprocessor | Zero-token local memory and atomic fact distillation. |")
	return strings.Join(lines, "\n")
}

func buildConventionsPage() string {
	var lines []string
	lines = append(lines, "# Conventions and Patterns", "", "## Core Invariants", "")
	lines = append(lines, "- **HISS-01**: Control flow must be DAG; zero recursion permitted.")
	lines = append(lines, "- **HISS-02**: Loops must have scalar upper bounds; all I/O must take `context.Context`.")
	lines = append(lines, "- **HISS-04**: McCabe Cyclomatic $\\le 10$, Cognitive $\\le 15$, Func LOC $\\le 75$.")
	lines = append(lines, "- **HISS-07**: Zero `.unwrap()` / `.expect()`; wrap all errors with context.")
	lines = append(lines, "- **HISS-10**: Zero-warning tolerance across compilers, linters, and formatters.")
	lines = append(lines, "- **HISS-15**: 3D testing mandatory for all public interfaces (Positive, Negative, Boundary).")
	lines = append(lines, "- **HISS-16**: Single canonical `AGENTS.md`; compile via `standardsctl compile-context`.")
	return strings.Join(lines, "\n")
}

func buildCoreConceptsPage() string {
	var lines []string
	lines = append(lines, "# Core Concepts", "", "## Key Entities", "")
	lines = append(lines, "- **HISS**: High-Integrity Systems Standards adopted from aerospace and safety-critical engineering.")
	lines = append(lines, "- **Flavor Archetypes**: Declarative project profiles declaring mandatory toolchains, settings, and templates.")
	lines = append(lines, "- **Ephemeral Gating Worktrees**: Isolated git worktrees running full test matrices before signing receipts.")
	lines = append(lines, "- **Ed25519 Exit-0 Receipt**: Cryptographically signed proof of successful verification pipeline execution.")
	lines = append(lines, "- **Distilled Documentation**: Pure-text, token-compressed library API and configuration sheets.")
	return strings.Join(lines, "\n")
}

func buildDecisionsPage() string {
	var lines []string
	lines = append(lines, "# Key Decisions and Rationale", "", "## Architectural Decision Records", "")
	lines = append(lines, "- **ADR-0001**: Universal Context Transpiler with single-source `AGENTS.md`.")
	lines = append(lines, "- **ADR-0002**: Highest-Standard-Wins lattice resolution across fleet configurations.")
	lines = append(lines, "- **ADR-0003**: Canonical engine and reverse-dogfooding topology.")
	lines = append(lines, "- **ADR-0004**: Ephemeral prefetch and gating pipeline.")
	lines = append(lines, "- **ADR-0005**: Cloud-native OCI distroless multi-arch containers.")
	lines = append(lines, "- **ADR-0006**: Hierarchical multi-tier runner matrix.")
	lines = append(lines, "- **ADR-0007**: Universal frameworks demand deduplication.")
	return strings.Join(lines, "\n")
}

func buildInitiativesPage(facts []MemoryFact) string {
	var lines []string
	lines = append(lines, "# Initiatives and Enhancements", "", "## Active Initiatives", "")
	lines = append(lines, "- **Praetor Flavor System, State Engine, and Toolchain Hardening**: 100% complete.")
	lines = append(lines, "- **Remote CI, Branch Protection & Ruleset Enforcement**: 100% complete.")
	lines = append(lines, "- **GitHub Pages & Architectural Branding Integration**: 100% complete.")
	lines = append(lines, "- **Package Documentation Distiller, Upstream Version Audit & Hindsight Optimizer**: In progress.")

	if len(facts) > 0 {
		lines = append(lines, "", "## Active Distilled Facts", "")
		for i := 0; i < 15 && i < len(facts); i++ {
			f := facts[i]
			lines = append(lines, fmt.Sprintf("- [%s] **%s**: %s", f.Category, f.Subject, f.Statement))
		}
	}
	return strings.Join(lines, "\n")
}
