import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Governance Lifecycle',
  alt: 'The governance lifecycle spans context compilation, policy resolution, and verification gating.',
  evidence: [
    'internal/agentcontext/render.go:vendorTargets',
    'internal/config/effective.go:ResolvePolicy',
    'cmd/standardsctl/audit.go:runAudit',
    'cmd/standardsctl/baseline.go:runBaseline',

    'internal/gating/pipeline.go:runReceiptStage',
  ],
  props: {
    layout: {
      direction: 'column',
      gap: 40,
      children: [
        {
          direction: 'row',
          gap: 40,
          align: 'center',
          children: [
            { id: 'agents', label: 'AGENTS.md', sub: 'Canonical Source', shape: 'store' },
            { id: 'comp', label: 'praetorctl compile-context' },
            {
              id: 'vendors',
              label: 'Vendor Projections',
              direction: 'column',
              gap: 10,
              children: [
                { id: 'c1', label: 'CLAUDE.md', sub: '< 300 LOC' },
                { id: 'c2', label: '.cursor/rules/*.mdc' },
                { id: 'c3', label: '.github/copilot-instructions.md' },
                { id: 'c4', label: '.windsurfrules' },
                { id: 'c5', label: '.gemini/GEMINI.md' },
                { id: 'c6', label: '.codex/rules.md' },
              ],
            },
          ],
        },
        {
          direction: 'row',
          gap: 40,
          align: 'center',
          children: [
            { id: 'standards', label: '.standards.yaml', sub: '& .standards.lock', shape: 'store' },
            { id: 'lattice', label: 'Effective Policy', sub: 'Lattice Supremum' },
            {
              direction: 'column',
              gap: 10,
              children: [
                { id: 'dev', label: '.devcontainer & Toolchain' },
                { id: 'audit', label: 'praetorctl audit' },
                { id: 'base', label: 'praetorctl baseline' },
              ],
            },
          ],
        },
        {
          direction: 'row',
          gap: 40,
          align: 'center',
          children: [
            { id: 'verify', label: 'make verify-all' },
            { id: 'gate', label: 'Gate stages' },
            { id: 'receipt', label: 'Ed25519 Exit-0 receipt', shape: 'store' },
          ],
        },
      ],
    },
    edges: [
      { from: 'agents', to: 'comp' },
      { from: 'comp', to: 'c1' },
      { from: 'comp', to: 'c2' },
      { from: 'comp', to: 'c3' },
      { from: 'comp', to: 'c4' },
      { from: 'comp', to: 'c5' },
      { from: 'comp', to: 'c6' },
      { from: 'standards', to: 'lattice' },
      { from: 'lattice', to: 'dev' },
      { from: 'lattice', to: 'audit' },
      { from: 'lattice', to: 'base' },
      { from: 'verify', to: 'gate' },
      { from: 'gate', to: 'receipt' },
    ],
    steps: [
      {
        label: 'compile-context',
        caption: 'AGENTS.md is transpiled to vendor-specific files.',
        flow: [
          { edges: 'agents->comp', say: 'The single source of truth is read.' },
          {
            edges: [
              'comp->c1',
              'comp->c2',
              'comp->c3',
              'comp->c4',
              'comp->c5',
              'comp->c6',
            ],
            say: 'Target-specific files are generated.',
          },
        ],
      },
      {
        label: 'policy',
        caption: 'The lattice resolves standard definitions into effective policy.',
        flow: [
          { edges: 'standards->lattice', say: 'Manifest and lockfile are merged.' },
          {
            edges: ['lattice->dev', 'lattice->audit', 'lattice->base'],
            say: 'Policy applies to tools, audits, and baselines.',
          },
        ],
      },
      {
        label: 'verify-all',
        caption: 'Local verification runs the gated pipeline.',
        flow: [
          { edges: 'verify->gate', say: 'Make target triggers the verification cascade.' },
          { edges: 'gate->receipt', say: 'A successful run mints a signed receipt.' },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
