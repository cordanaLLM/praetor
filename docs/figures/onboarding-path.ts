import type { PraetorFigure } from '../../tools/figures/types.ts';

// The six vendor files CompileVendorTargets writes, in vendorTargets order
// (internal/agentcontext/render.go). init and compile-context both write them.
const vendorFiles = [
  'CLAUDE.md',
  '.cursor/rules/hiss-invariants.mdc',
  '.github/copilot-instructions.md',
  '.windsurfrules',
  '.gemini/GEMINI.md',
  '.codex/rules.md',
];

export default {
  title: 'Repository onboarding path',
  alt: 'Five onboarding commands scaffold configuration and agent files, record debt, prepare a devcontainer, and verify governance.',
  evidence: [
    'cmd/standardsctl/init.go:runInit',
    'cmd/standardsctl/init.go:ensureManifestAbsent',
    'cmd/standardsctl/init.go:createInitialManifest',
    'cmd/standardsctl/init.go:initBaselineAndLockfile',
    'cmd/standardsctl/init.go:initAgentContext',
    'internal/compiler/projection.go:CompileVendorTargets',
    'internal/agentcontext/render.go:vendorTargets',
    'cmd/standardsctl/compile_context.go:runCompileContext',
    'internal/compiler/projection.go:CompileContextProjections',
    'internal/compiler/projection.go:writeAgentSurfaces',
    'internal/compiler/projection.go:VerifyCompiledContext',
    'internal/compiler/agent_projection.go:CanonicalAgentsRel',
    'cmd/standardsctl/baseline.go:runBaseline',
    'cmd/standardsctl/baseline.go:recordBaseline',
    'cmd/standardsctl/devcontainer.go:runDevContainer',
    'cmd/standardsctl/devcontainer.go:generateDevContainerBundle',
    'internal/devcontainer/bootstrap.go:bootstrapDockerfile',
    'internal/devcontainer/bootstrap.go:maxBootstrapParts',
    'internal/devcontainer/bootstrap_archive.go:bootstrapPartName',
    'cmd/standardsctl/audit.go:runAudit',
    'cmd/standardsctl/adopt.go:runAdopt',
    'internal/adopt/adopt.go:adoptSteps',
    'internal/adopt/adopt.go:manifestFile',
    'internal/adopt/harness.go:resolveAgentsContent',
  ],
  describe: [
    'A repository that already carries AGENTS.md runs five commands in order. init writes the manifest, lockfile and a zero-debt baseline, then compiles the six vendor files from AGENTS.md. compile-context repeats that compile and adds persona copies. baseline records existing debt, devcontainer generate prepares the container bundle, and audit verifies the result.',
    'praetorctl adopt is an alternate entry, not a step before init: one run writes what steps 1 to 4 write, AGENTS.md included when it is missing, and the path continues at step 5. init refuses to run afterwards because .standards.yaml already exists.',
  ],
  props: {
    speed: 1100,
    layout: {
      gap: 130,
      align: 'center',
      children: [
        {
          direction: 'column',
          gap: 24,
          children: [
            { id: 'repo', label: 'Target repository', sub: 'already carries AGENTS.md', shape: 'store', width: 300 },
            { id: 'adopt', label: 'Alternate entry', sub: 'praetorctl adopt', width: 300 },
          ],
        },
        {
          id: 'pipeline',
          label: 'Onboarding commands, in order',
          direction: 'column',
          gap: 20,
          children: [
            { id: 'init', label: '1. Scaffolding', sub: 'praetorctl init', width: 330 },
            { id: 'compile', label: '2. Context Recompilation', sub: 'praetorctl compile-context', width: 330 },
            { id: 'baseline', label: '3. Brownfield Baselining', sub: 'praetorctl baseline --record --allow-increase', width: 330 },
            { id: 'devcontainer', label: '4. Devcontainer Setup', sub: 'praetorctl devcontainer generate', width: 330 },
            { id: 'audit', label: '5. Audit Verification', sub: 'praetorctl audit', width: 330 },
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
      { from: 'adopt', to: 'audit', label: 'skips 1-4' },
      { from: 'audit', to: 'governed', label: 'verified' },
    ],
    steps: [
      {
        label: 'init',
        caption: 'Write the configuration, then compile the vendor files from AGENTS.md.',
        flow: [
          { edges: 'repo->init', say: 'init reads AGENTS.md beside the manifest; without it, init writes no agent files.' },
          {
            say: 'createInitialManifest and initBaselineAndLockfile write the manifest, the lockfile and a zero-debt baseline.',
            show: {
              init: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', meta: '0 infractions', mono: true },
              ],
            },
          },
          {
            say: 'initAgentContext splices the text register into AGENTS.md and compiles the six vendor files.',
            show: {
              init: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', meta: '0 infractions', mono: true },
                { tag: 'updated', tone: 'blue', text: 'AGENTS.md', meta: 'register block', mono: true },
                ...vendorFiles.map((text) => ({ tag: 'created', tone: 'green' as const, text, mono: true })),
              ],
            },
          },
        ],
      },
      {
        label: 'compile-context',
        caption: 'Recompile the vendor files and add persona copies; rerun after every AGENTS.md edit.',
        flow: [
          { edges: 'init->compile', say: 'compile-context runs the same splice and compile that init ran.' },
          {
            say: 'writeAgentSurfaces then copies every .agents/agents persona into each client persona directory.',
            show: {
              compile: [
                { tag: 'rebuilt', tone: 'blue', text: 'the six vendor files', meta: 'same as init' },
                { tag: 'created', tone: 'green', text: '.claude/agents/', meta: 'per persona', mono: true },
                { tag: 'created', tone: 'green', text: '.github/agents/', meta: 'per persona', mono: true },
                { tag: 'created', tone: 'green', text: '.gemini/agents/', meta: 'per persona', mono: true },
                { tag: 'created', tone: 'green', text: '.codex/agents/', meta: 'per persona', mono: true },
                { tag: 'check', tone: 'gray', text: 'compile-context --verify', meta: 'writes nothing', mono: true },
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
            say: 'Initial legacy debt is recorded with --allow-increase and --reason="<why>" to permit non-zero count.',
            show: {
              baseline: [
                { tag: 'updated', tone: 'blue', text: '.standards-baseline.json', meta: 'debt snapshot', mono: true },
                { tag: 'required', tone: 'orange', text: '--allow-increase --reason="<why>"', mono: true },
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
            say: 'The configuration, Dockerfile.praetor and up to eight source parts are written to .devcontainer/.',
            show: {
              devcontainer: [
                { tag: 'created', tone: 'green', text: '.devcontainer/devcontainer.json', mono: true },
                { tag: 'created', tone: 'green', text: '.devcontainer/Dockerfile.praetor', mono: true },
                { tag: 'created', tone: 'green', text: '.devcontainer/praetor-source.NNN.b64', mono: true },
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
      {
        label: 'adopt instead',
        caption: 'An alternate entry that replaces steps 1 to 4, not a step before init.',
        flow: [
          {
            say: 'One adopt run writes what steps 1 to 4 write, and synthesizes AGENTS.md when it is missing.',
            show: {
              adopt: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', meta: 'debt recorded', mono: true },
                { tag: 'created', tone: 'green', text: 'AGENTS.md', meta: 'when missing', mono: true },
                { tag: 'created', tone: 'green', text: 'the six vendor files' },
                { tag: 'created', tone: 'green', text: '.devcontainer/devcontainer.json', mono: true },
              ],
            },
          },
          {
            edges: 'adopt->audit',
            say: 'The path continues at step 5. init would now refuse: .standards.yaml already exists.',
            show: {
              init: [{ tag: 'refused', tone: 'orange', text: '.standards.yaml already exists', meta: 'ensureManifestAbsent' }],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
