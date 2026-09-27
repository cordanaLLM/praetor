// Twenty-one HISS invariants structured across deterministic execution, memory safety,
// governance, and operations families, drawn from the canonical registry in internal/hisscatalog.
import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'HISS invariant taxonomy',
  alt: 'Twenty-one HISS invariants structured across deterministic execution, memory safety, governance, and operations families.',
  evidence: [
    'internal/hisscatalog/catalog.go:catalog',
    'internal/hisscatalog/catalog.go:Rules',
    'internal/hisscatalog/catalog.go:Rule',
    'internal/hiss/rules.go:scanNativeLineInvariants',
  ],
  describe: [
    'The 21 invariants of the High-Integrity Systems Standard form four governing families from static determinism to agent operations.',
  ],
  props: {
    layout: {
      direction: 'row',
      gap: 36,
      children: [
        {
          id: 'exec',
          label: 'Deterministic Execution',
          direction: 'column',
          gap: 16,
          children: [
            { id: 'h01', label: 'HISS-01', sub: 'Acyclic DAG Control Flow', width: 210 },
            { id: 'h02', label: 'HISS-02', sub: 'Bounded Loops & I/O Timeouts', width: 210 },
            { id: 'h03', label: 'HISS-03', sub: 'Zero Frame Malloc', width: 210 },
            { id: 'h04', label: 'HISS-04', sub: 'Complexity & Sizing Bounds', width: 210 },
            { id: 'h05', label: 'HISS-05', sub: 'Variable Scoping', width: 210 },
            { id: 'h06', label: 'HISS-06', sub: 'Bounded Concurrency', width: 210 },
          ],
        },
        {
          id: 'safety',
          label: 'Memory & Error Integrity',
          direction: 'column',
          gap: 16,
          children: [
            { id: 'h07', label: 'HISS-07', sub: 'Checked Errors & Zero Unwrap', width: 210 },
            { id: 'h08', label: 'HISS-08', sub: 'Static Determinism & Banned Funcs', width: 210 },
            { id: 'h09', label: 'HISS-09', sub: 'Mandatory // SAFETY: Proofs', width: 210 },
            { id: 'h10', label: 'HISS-10', sub: '5-Layer Zero-Warnings Cascade', width: 210 },
          ],
        },
        {
          id: 'gov',
          label: 'Contracts & Fleet Governance',
          direction: 'column',
          gap: 16,
          children: [
            { id: 'h11', label: 'HISS-11', sub: 'Hermetic Supply Chain', width: 210 },
            { id: 'h12', label: 'HISS-12', sub: 'Secret Leak Prevention', width: 210 },
            { id: 'h13', label: 'HISS-13', sub: 'Monotonic Debt Ratchet', width: 210 },
            { id: 'h14', label: 'HISS-14', sub: 'Append-Only ABI & Migration', width: 210 },
            { id: 'h15', label: 'HISS-15', sub: '3D Test Discipline', width: 210 },
            { id: 'h16', label: 'HISS-16', sub: 'Canonical AGENTS.md & Server Gates', width: 210 },
          ],
        },
        {
          id: 'ops',
          label: 'Agent Operations & Evidence',
          direction: 'column',
          gap: 16,
          children: [
            { id: 'h17', label: 'HISS-17', sub: 'State Ledger Discipline', width: 210 },
            { id: 'h18', label: 'HISS-18', sub: 'Diff-Aware CI Efficiency', width: 210 },
            { id: 'h19', label: 'HISS-19', sub: 'Reuse Before Writing', width: 210 },
            { id: 'h20', label: 'HISS-20', sub: 'Replayable Enforcement Evidence', width: 210 },
            { id: 'h21', label: 'HISS-21', sub: 'Platform Neutrality', width: 210 },
          ],
        },
      ],
    },
    edges: [
      { from: 'exec', to: 'safety' },
      { from: 'safety', to: 'gov' },
      { from: 'gov', to: 'ops' },
    ],
    steps: [],
  },
} satisfies PraetorFigure;
