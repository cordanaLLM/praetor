---
name: standards-sync
description: Synchronize and reconcile local repository configurations, DevContainers, and universal agent harnesses with the upstream cordanaLLM fleet governance lattice.
---

# Standards Synchronization & Fleet Reconciliation (`standards-sync`)

Reconcile local repository configurations, DevContainers, and universal AI agent harnesses with the declared `cordanaLLM/praetor` lattice.

## 5-Step Synchronization Workflow

1. **Validate Declared Manifest**:
   - Verify `.standards.yaml` parses cleanly:
     ```bash
     go run ./cmd/standardsctl plan
     ```
   - Confirm active profiles and facets resolve to the highest-standard supremum.

2. **Transpile Universal Agent Context (HISS-16)**:
   - Compile canonical `AGENTS.md` into vendor-specific agent harnesses:
     ```bash
     go run ./cmd/standardsctl compile-context
     ```
   - Target files generated automatically:
     - `CLAUDE.md` ($\le 300$ LOC)
     - `.cursor/rules/*.mdc`
     - `.windsurfrules`
     - `.github/copilot-instructions.md`
   - Verify zero uncommitted diffs:
     ```bash
     go run ./cmd/standardsctl compile-context --verify
     ```

3. **Reconcile Hermetic DevContainer**:
   - Regenerate `.devcontainer/devcontainer.json` from declared profiles:
     ```bash
     go run ./cmd/standardsctl devcontainer generate
     ```

4. **Verify Technical Debt Ratchet**:
   - Check `.standards-baseline.json` against current repository state:
     ```bash
     go run ./cmd/standardsctl baseline --verify
     ```
   - Invariant: Technical debt must monotonically decrease ($V_{\text{total}}(t_1) \le V_{\text{total}}(t_0)$).

5. **Execute Fleet Verification Gate**:
   - Run the final local status gate:
     ```bash
     make verify-all
     ```
