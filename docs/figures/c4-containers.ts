import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Container Diagram',
  alt: 'Runtime modules within the Praetor binary, daemon services, and configuration inputs.',
  evidence: [
    'cmd/standardsctl/serve.go:runServe',
    'cmd/standardsctl/compile_context.go:runCompileContext',
    'internal/agentcontext/render.go:NewTranspiler',
    'internal/gating/pipeline.go:RunGatedPipeline',
    'internal/hiss/hiss.go:Scan',
    'internal/runner/matrix.go:ResolveRunner',
    'internal/bump/canary.go:RunCanary',
    'internal/lockdown/distill.go:DistillSARIF',
    'internal/router/models.go:LoadRoutingConfig',
    'cmd/standards-mcp/main.go:main',
    'cmd/standards-lsp/main.go:main',
    'internal/compiler/agents.go:CompileAgents',
    'internal/config/hierarchy.go:LoadCascadingRunnerConfigContext',
    'internal/gating/prefetch.go:VerifyLockfiles',
    'internal/gating/pipeline.go:hissScanOptions',
    'Dockerfile:ENTRYPOINT',
    '.github/workflows/release-binaries.yml:workflow_dispatch',
  ],
  describe: [
    'The CLI routes commands to internal engines.',
    'SARIF Distillation condenses failure outputs for the agent.'
  ],
  props: {
    layout: {
      direction: 'column',
      gap: 30,
      children: [
        {
          id: 'repo_space',
          label: 'Repository Space',
          direction: 'row',
          gap: 20,
          children: [
            { id: 'standards', label: '.standards.yaml', sub: 'Manifest, repo-layer' },
            { id: 'fleet', label: '.config/fleet.yaml', sub: 'fleet-layer, optional' },
            { id: 'orgs', label: '.config/orgs/*.yaml', sub: 'org-layer, optional' },
            { id: 'agents_md', label: 'AGENTS.md', sub: 'Instructions' },
            { id: 'subagents', label: '.agents/', sub: 'Personas, Skills' },
          ],
        },
        {
          id: 'praetor_cli',
          label: 'Praetor Core Engine (Go Binary / Container)',
          direction: 'column',
          gap: 20,
          children: [
            {
              direction: 'row',
              gap: 20,
              children: [
                { id: 'cli', label: 'CLI & Servers', sub: 'cmd/standardsctl, mcp, lsp' },
                { id: 'serve', label: 'HTTP Health Probes', sub: 'praetorctl serve' },
              ]
            },
            {
              direction: 'row',
              gap: 20,
              children: [
                { id: 'gating', label: 'Gating Engine' },
                { id: 'compiler', label: 'Compiler & Transpiler' },
                { id: 'hiss', label: 'HISS Invariant Scanner' },
              ]
            },
            {
              direction: 'row',
              gap: 20,
              children: [
                { id: 'runner', label: 'Runner Matrix Router' },
                { id: 'canary', label: 'Bump Canary' },
                { id: 'distill', label: 'SARIF Distiller' },
                { id: 'router', label: 'Model Router' },
              ]
            },
          ],
        },
        {
          id: 'outputs',
          label: 'Generated Projections',
          direction: 'row',
          gap: 20,
          children: [
            { id: 'vendor', label: 'Vendor Agent Files', sub: 'CLAUDE.md, etc.' },
            { id: 'sarif_out', label: 'Diagnostic Summary', sub: 'SARIF output' },
            { id: 'oci', label: 'Distroless Image', sub: 'ghcr.io/cordanallm/praetor' },
          ],
        },
      ],
    },
    edges: [
      { from: 'standards', to: 'gating', label: 'lockfile policy' },
      { from: 'standards', to: 'runner', label: 'repo layer, wins' },
      { from: 'fleet', to: 'runner', label: 'fleet layer' },
      { from: 'orgs', to: 'runner', label: 'org layer' },
      { from: 'agents_md', to: 'compiler' },
      { from: 'subagents', to: 'compiler' },
      { from: 'cli', to: 'gating' },
      { from: 'cli', to: 'compiler' },
      { from: 'gating', to: 'hiss' },
      { from: 'compiler', to: 'vendor' },
      { from: 'canary', to: 'distill', label: 'wraps failure as SARIF' },
      { from: 'distill', to: 'sarif_out' },
      { from: 'serve', to: 'oci', label: 'packaged in' },
    ],
    steps: [
      {
        label: 'compile-context',
        caption: 'Compiles AGENTS.md and .agents/ to vendor-specific instructions.',
        flow: [
          { edges: 'agents_md->compiler', say: 'Reads AGENTS.md.' },
          { edges: 'subagents->compiler', say: 'Reads .agents/ personas and skills (agents.go persona projection).' },
          { edges: 'compiler->vendor', say: 'Renders 6 vendor files (CLAUDE.md, etc.).' },
        ],
      },
      {
        label: 'gating',
        caption: 'Anti-Direct-Merge Gating Pipeline.',
        flow: [
          { edges: 'cli->gating', say: 'Runs praetorctl gate run.' },
          { edges: 'standards->gating', say: 'Reads policy and verifies lockfiles.' },
          { edges: 'gating->hiss', say: '6 stages including HISS.' },
        ],
      },
      {
        label: 'runner routing',
        caption: 'Resolves runners using hierarchical policy; CI itself runs on GitHub-hosted runners and does not consume this result (ADR-0012).',
        flow: [
          { edges: 'fleet->runner', say: 'DefaultRunnerPolicy merges fleet.yaml first.' },
          { edges: 'orgs->runner', say: 'Then .config/orgs/<org>.yaml overrides fleet.' },
          { edges: 'standards->runner', say: 'Then .standards.yaml runners: overrides fleet and org (LoadCascadingRunnerConfigContext, last write wins).' },
        ],
      },
      {
        label: 'SARIF distillation',
        caption: 'Condenses SARIF logs into bounded summaries.',
        flow: [
          { edges: 'canary->distill', say: 'Canary test wraps failure in SARIF.' },
          { edges: 'distill->sarif_out', say: 'Distills summary and writes full report.' },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
