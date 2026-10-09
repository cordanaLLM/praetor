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
  alt: 'Six commands scaffold config, pin the lock, compile context, record debt, prepare a devcontainer, and verify governance.',
  evidence: [
    'cmd/standardsctl/init.go:runInit',
    'cmd/standardsctl/init.go:ensureManifestAbsent',
    'cmd/standardsctl/init.go:createInitialManifest',
    'cmd/standardsctl/init.go:initBaselineAndLockfile',
    'cmd/standardsctl/init.go:initAgentContext',
    'internal/adopt/context_write.go:CompileAgentContext',
    'internal/adopt/private_ignore.go:EnsureEvidenceIgnore',
    'internal/compiler/projection.go:CompileVendorTargets',
    'internal/agentcontext/render.go:vendorTargets',
    'cmd/standardsctl/profile.go:runProfileSet',
    'internal/adopt/profile_set.go:SetProfile',
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
    'In the onboarding guide, praetorctl adopt is the primary quickstart: one run adopts an existing repository into full governance, synthesizing AGENTS.md when missing and configuring the manifest, pinned lock, catalog, baseline, agent files, DevContainer, hooks and CI workflows.',
    'praetorctl init is the staged alternative for repositories that already carry canonical AGENTS.md instructions. Six commands run in order: init writes the manifest, lockfile placeholder and baseline, profile set pins the lockfile and catalog, compile-context projects vendor files, baseline records debt, devcontainer generate prepares the container bundle, and audit verifies governance. init refuses to run after adoption because .standards.yaml already exists.',
    'The adopt quickstart passes audit on its own once committed; the staged init path leaves remaining gaps (ruleset, CI workflows, and hooks) that do not pass audit alone.',
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
            { id: 'adopt', label: 'Quickstart', sub: 'praetorctl adopt', width: 300 },
          ],
        },
        {
          id: 'pipeline',
          label: 'Onboarding commands, in order',
          direction: 'column',
          gap: 20,
          children: [
            { id: 'init', label: '1. Scaffolding', sub: 'praetorctl init', width: 330 },
            { id: 'pin', label: '2. Lockfile Pinning', sub: 'praetorctl profile set --lock-source-root', width: 330 },
            { id: 'compile', label: '3. Context Recompilation', sub: 'praetorctl compile-context', width: 330 },
            { id: 'baseline', label: '4. Brownfield Baselining', sub: 'praetorctl baseline --record --allow-increase', width: 330 },
            { id: 'devcontainer', label: '5. Devcontainer Setup', sub: 'praetorctl devcontainer generate --source-root', width: 330 },
            { id: 'audit', label: '6. Audit Verification', sub: 'praetorctl audit --offline', width: 330 },
          ],
        },
        {
          direction: 'column',
          gap: 24,
          children: [
            { id: 'governed', label: 'Audit passes', sub: 'the quickstart', shape: 'store', width: 240 },
            { id: 'gaps', label: 'Remaining gaps', sub: 'does not pass audit on its own', shape: 'store', width: 240 },
          ],
        },
      ],
    },
    edges: [
      { from: 'repo', to: 'init', label: 'start' },
      { from: 'init', to: 'pin' },
      { from: 'pin', to: 'compile' },
      { from: 'compile', to: 'baseline' },
      { from: 'baseline', to: 'devcontainer' },
      { from: 'devcontainer', to: 'audit' },
      { from: 'adopt', to: 'audit', label: 'skips 1-5' },
      { from: 'audit', to: 'governed', label: 'audit passes' },
      { from: 'audit', to: 'gaps', label: 'remaining gaps' },
    ],
    steps: [
      {
        label: 'init',
        caption: 'Write the configuration, then compile the vendor files from AGENTS.md.',
        flow: [
          { edges: 'repo->init', say: 'init reads AGENTS.md beside the manifest; without it, init writes no agent files.' },
          {
            say: 'createInitialManifest and initBaselineAndLockfile write the manifest, an unpinned placeholder lockfile and a zero-debt baseline.',
            show: {
              init: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', meta: 'unpinned placeholder', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', meta: '0 infractions', mono: true },
              ],
            },
          },
          {
            say: 'initAgentContext runs the compile-context write: Git ignores .workingdir/evidence/, the text register is spliced into AGENTS.md, and the six vendor files and persona copies are compiled and linted.',
            show: {
              init: [
                { tag: 'created', tone: 'green', text: '.standards.yaml', mono: true },
                { tag: 'created', tone: 'green', text: '.standards.lock', meta: 'unpinned placeholder', mono: true },
                { tag: 'created', tone: 'green', text: '.standards-baseline.json', meta: '0 infractions', mono: true },
                { tag: 'updated', tone: 'blue', text: '.gitignore', meta: 'evidence ignored', mono: true },
                { tag: 'updated', tone: 'blue', text: 'AGENTS.md', meta: 'register block', mono: true },
                ...vendorFiles.map((text) => ({ tag: 'created', tone: 'green' as const, text, mono: true })),
                { tag: 'created', tone: 'green', text: 'persona copies', meta: 'per persona' },
              ],
            },
          },
        ],
      },
      {
        label: 'profile set',
        caption: 'Pin the lockfile and write the policy catalog from the Praetor checkout.',
        flow: [
          { edges: 'init->pin', say: 'profile set pins placeholder lockfile digests from the reviewed Praetor source root.' },
          {
            say: 'devcontainer generate and audit require sha256 digests; pinning also materializes the catalog under .config/archetypes/.',
            show: {
              pin: [
                { tag: 'updated', tone: 'blue', text: '.standards.lock', meta: 'sha256 digests', mono: true },
                { tag: 'created', tone: 'green', text: '.config/archetypes/', meta: 'policy catalog', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'compile-context',
        caption: 'Recompile the vendor files and persona copies; rerun after every AGENTS.md edit.',
        flow: [
          { edges: 'pin->compile', say: 'compile-context runs the same write that init ran.' },
          {
            say: 'writeAgentSurfaces copies every .agents/agents persona into each client persona directory, then the caveman lint runs.',
            show: {
              compile: [
                { tag: 'rebuilt', tone: 'blue', text: 'the six vendor files', meta: 'same as init' },
                { tag: 'rebuilt', tone: 'blue', text: '.claude/agents/', meta: 'per persona', mono: true },
                { tag: 'rebuilt', tone: 'blue', text: '.github/agents/', meta: 'per persona', mono: true },
                { tag: 'rebuilt', tone: 'blue', text: '.gemini/agents/', meta: 'per persona', mono: true },
                { tag: 'rebuilt', tone: 'blue', text: '.codex/agents/', meta: 'per persona', mono: true },
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
            say: 'JSON configuration and exact source companions are prepared; build and startup remain separate checks.',
            show: {
              devcontainer: [
                { tag: 'prepared', tone: 'green', text: 'devcontainer.json', mono: true },
                { tag: 'prepared', tone: 'green', text: 'Dockerfile.praetor', mono: true },
                { tag: 'prepared', tone: 'green', text: 'praetor-source.NNN.b64', mono: true },
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
            edges: 'audit->gaps',
            say: 'The staged alternative does not pass the audit on its own: ruleset, gate tooling and hooks remain missing.',
            show: {
              audit: [
                { tag: 'fails', tone: 'orange', text: 'audit fails on gaps', meta: 'ruleset, tooling, hooks missing' },
                { tag: 'staged', tone: 'orange', text: 'does not pass audit alone' },
              ],
              gaps: [
                { tag: 'gaps', tone: 'orange', text: 'ruleset and CI missing' },
                { tag: 'staged', tone: 'orange', text: 'does not pass audit alone' },
              ],
            },
          },
        ],
      },
      {
        label: 'adopt quickstart',
        caption: 'The quickstart that replaces steps 1 to 5, not a step before init.',
        flow: [
          {
            say: 'One adopt run writes what steps 1 to 5 write, and synthesizes AGENTS.md when it is missing.',
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
            say: 'The path continues at step 6. init would now refuse: .standards.yaml already exists.',
            show: {
              init: [{ tag: 'refused', tone: 'orange', text: '.standards.yaml already exists', meta: 'ensureManifestAbsent' }],
            },
          },
          {
            edges: 'audit->governed',
            say: 'With git add -A, praetorctl audit --offline exits 0: the quickstart passes audit on its own.',
            show: {
              audit: [
                { tag: 'pass', tone: 'green', text: 'audit --offline exits 0' },
                { tag: 'verified', tone: 'green', text: 'all gates pass' },
              ],
              governed: [
                { tag: 'pass', tone: 'green', text: 'governance contract verified' },
                { tag: 'verified', tone: 'green', text: '0 files modified', meta: 'read-only' },
              ],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
