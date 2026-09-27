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
    'internal/config/hierarchy.go:DefaultRunnerPolicy',
    'cmd/standardsctl/models.go:runModels',
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
      direction: 'row',
      gap: 130,
      children: [
        {
          id: 'repo_space',
          label: 'Repository Space',
          direction: 'column',
          gap: 80,
          align: 'end',
          children: [
            {
              direction: 'column',
              gap: 8,
              children: [
                { id: 'fleet', label: '.config/fleet.yaml', sub: 'fleet layer, optional' },
                { id: 'orgs', label: '.config/orgs/*.yaml', sub: 'org layer, optional' },
                { id: 'standards', label: '.standards.yaml', sub: 'manifest, repo layer' },
              ],
            },
            {
              direction: 'column',
              gap: 8,
              children: [
                { id: 'agents_md', label: 'AGENTS.md', sub: 'instructions' },
                { id: 'subagents', label: '.agents/', sub: 'personas, skills' },
              ],
            },
            { id: 'routing', label: 'routing.yaml', sub: '.config/models/' },
          ],
        },
        {
          id: 'praetor_cli',
          label: 'Praetor Core Engine (Go Binary / Container)',
          direction: 'column',
          gap: 24,
          align: 'start',
          children: [
            { id: 'runner', label: 'Runner Matrix Router', sub: 'runner.ResolveRunner' },
            {
              direction: 'row',
              gap: 20,
              children: [
                { id: 'gating', label: 'Gating Engine', sub: 'praetorctl gate run' },
                { id: 'hiss', label: 'HISS Invariant Scanner', sub: 'hiss.Scan' },
              ],
            },
            {
              direction: 'row',
              gap: 20,
              children: [
                { id: 'cli', label: 'CLI & Servers', sub: 'cmd/standardsctl, mcp, lsp' },
                { id: 'serve', label: 'HTTP Health Probes', sub: 'praetorctl serve' },
              ],
            },
            { id: 'compiler', label: 'Compiler & Transpiler', sub: 'praetorctl compile-context' },
            {
              direction: 'row',
              gap: 60,
              children: [
                { id: 'canary', label: 'Bump Canary', sub: 'bump.RunCanary' },
                { id: 'distill', label: 'SARIF Distiller', sub: 'lockdown.DistillSARIF' },
              ],
            },
            { id: 'router', label: 'Model Router', sub: 'praetorctl models' },
          ],
        },
        {
          id: 'outputs',
          label: 'Generated Projections',
          direction: 'column',
          gap: 42,
          align: 'start',
          children: [
            { id: 'oci', label: 'Distroless Image', sub: 'ghcr.io/cordanallm/praetor' },
            { id: 'vendor', label: 'Vendor Agent Files', sub: 'CLAUDE.md, etc.' },
            { id: 'sarif_out', label: 'Diagnostic Summary', sub: 'SARIF output' },
          ],
        },
      ],
    },
    edges: [
      { from: 'routing', to: 'router' },
      { from: 'fleet', to: 'runner', label: 'fleet layer' },
      { from: 'orgs', to: 'runner', label: 'org layer' },
      { from: 'standards', to: 'runner', label: 'repo layer, wins' },
      { from: 'standards', to: 'gating', label: 'lockfile policy' },
      { from: 'agents_md', to: 'compiler' },
      { from: 'subagents', to: 'compiler' },
      { from: 'cli', to: 'gating' },
      { from: 'cli', to: 'compiler' },
      { from: 'gating', to: 'hiss' },
      { from: 'compiler', to: 'vendor' },
      { from: 'canary', to: 'distill', label: 'SARIF' },
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
          { edges: 'fleet->runner', say: 'LoadCascadingRunnerConfigContext starts from DefaultRunnerPolicy and merges .config/fleet.yaml onto it.' },
          { edges: 'orgs->runner', say: 'Then .config/orgs/<org>.yaml overrides fleet.' },
          { edges: 'standards->runner', say: 'Then the runners: key of .standards.yaml overrides fleet and org; the last layer merged wins.' },
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
