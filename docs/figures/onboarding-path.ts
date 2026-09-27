import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Repository onboarding path',
  alt: 'Five onboarding commands run in sequence to scaffold configuration, transpile context, record debt, and verify governance.',
  evidence: [
    'cmd/standardsctl/init.go:runInit',
    'cmd/standardsctl/init.go:createInitialManifest',
    'cmd/standardsctl/init.go:initBaselineAndLockfile',
    'cmd/standardsctl/compile_context.go:runCompileContext',
    'internal/compiler/projection.go:CompileContextProjections',
    'cmd/standardsctl/baseline.go:runBaseline',
    'cmd/standardsctl/baseline.go:recordBaseline',
    'cmd/standardsctl/devcontainer.go:runDevContainer',
    'cmd/standardsctl/devcontainer.go:generateDevContainerBundle',
    'cmd/standardsctl/audit.go:runAudit',
    'cmd/standardsctl/adopt.go:runAdopt',
  ],
  describe: [
    'Onboarding executes five commands in sequence to establish declarative standards, vendor agent files, baseline legacy debt, generate devcontainers, and verify compliance.',
  ],
  props: {
    speed: 1100,
    layout: {
      gap: 40,
      align: 'center',
      children: [
        { id: 'repo', label: 'Target repository', sub: 'greenfield or brownfield', shape: 'store', width: 220 },
        {
          id: 'pipeline',
          label: 'Onboarding commands, in order',
          direction: 'column',
          gap: 20,
          children: [
            { id: 'init', label: '1. Scaffolding', sub: 'praetorctl init', width: 260 },
            { id: 'compile', label: '2. Context Transpilation', sub: 'praetorctl compile-context', width: 260 },
            { id: 'baseline', label: '3. Brownfield Baselining', sub: 'praetorctl baseline --record', width: 260 },
            { id: 'devcontainer', label: '4. Devcontainer Setup', sub: 'praetorctl devcontainer generate', width: 260 },
            { id: 'audit', label: '5. Audit Verification', sub: 'praetorctl audit', width: 260 },
          ],
        },
        { id: 'governed', label: 'Governed repository', sub: 'praetorctl audit pass', shape: 'store', width: 220 },
      ],
    },
    edges: [
      { from: 'repo', to: 'init', label: 'start' },
      { from: 'init', to: 'compile' },
      { from: 'compile', to: 'baseline' },
      { from: 'baseline', to: 'devcontainer' },
      { from: 'devcontainer', to: 'audit' },
      { from: 'audit', to: 'governed', label: 'verified' },
    ],
    steps: [
      {
        label: 'init',
        caption: 'Initialize configuration with declared profile and facets.',
        flow: [
          { edges: 'repo->init', say: 'Target repository is initialized with declared profile and facets.' },
          {
            say: 'Scaffolding creates .standards.yaml, .standards.lock, and initial baseline.',
            show: {
              init: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'compile-context',
        caption: 'Transpile universal agent harness from AGENTS.md.',
        flow: [
          { edges: 'init->compile', say: 'Canonical agent instructions in AGENTS.md are compiled.' },
          {
            say: 'Transpilation generates vendor instruction files and splices the text register.',
            show: {
              compile: [
                { tag: 'updated', tone: 'blue', text: 'AGENTS.md', mono: true },
                { tag: 'created', tone: 'green', text: 'CLAUDE.md', mono: true },
                { tag: 'created', tone: 'green', text: '.cursor/rules/*.mdc', mono: true },
                { tag: 'created', tone: 'green', text: '.windsurfrules', mono: true },
                { tag: 'created', tone: 'green', text: '.github/copilot-instructions.md', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'baseline',
        caption: 'Snapshot legacy technical debt into baseline file.',
        flow: [
          { edges: 'compile->baseline', say: 'HISS scanner sweeps the codebase for existing infractions.' },
          {
            say: 'Legacy infractions are snapshotted into .standards-baseline.json to prevent CI failure.',
            show: {
              baseline: [
                { tag: 'updated', tone: 'blue', text: '.standards-baseline.json', meta: 'scanned debt snapshot', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'devcontainer',
        caption: 'Prepare a portable devcontainer from reviewed Praetor sources.',
        flow: [
          { edges: 'baseline->devcontainer', say: 'DevContainer configuration and reviewed source bundle companions are generated.' },
          {
            say: 'Configuration, Dockerfile, and exact source companions are prepared in .devcontainer/.',
            show: {
              devcontainer: [
                { tag: 'created', tone: 'green', text: '.devcontainer/devcontainer.json', mono: true },
                { tag: 'created', tone: 'green', text: '.devcontainer/Dockerfile.praetor', mono: true },
                { tag: 'created', tone: 'green', text: '.devcontainer/source parts', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'audit',
        caption: 'Verify configured governance contracts and debt ratchet.',
        flow: [
          { edges: 'devcontainer->audit', say: 'Audit checks manifest, lockfile, agent context, debt baseline, and DevContainer.' },
          {
            edges: 'audit->governed',
            say: 'Every executed gate passes with zero files modified; repository is verified and governed.',
            show: {
              audit: [
                { tag: 'verified', tone: 'green', text: '0 files modified', meta: 'read-only' },
                { tag: 'pass', tone: 'green', text: 'governance contract verified' },
              ],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
