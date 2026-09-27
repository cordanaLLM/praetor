import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Lattice Join',
  alt: 'Policy layers fold in a fixed order through the lattice join; repository control overrides apply after the join.',
  evidence: [
    'internal/config/effective_load.go:loadEffectivePolicy',
    'internal/config/effective_load.go:effectiveLoader.manifest',
    'internal/config/effective_load.go:externalLayers',
    'internal/config/effective_load.go:auditCompatibilityLayer',
    'internal/config/effective_load.go:finishEffectivePolicy',
    'internal/config/effective_pins.go:pinnedLayers',
    'internal/config/effective_pins.go:pinnedLayer',
    'internal/config/effective_pins.go:ErrLockDigestMismatch',
    'internal/config/effective.go:newEffectivePolicy',
    'internal/config/effective.go:ResolvePolicy',
    'internal/config/effective.go:applyLayer',
    'internal/config/config.go:DefaultPolicy',
    'internal/config/config.go:Join',
    'internal/config/config.go:joinReviewMode',
    'internal/config/config.go:ApplyOverrides',
    'internal/config/archetype.go:ArchetypeControls.validate',
    'internal/config/operator_decode.go:operatorSectionNames',
    'internal/config/devcontainer_features.go:ResolveDevContainerFeatures',
    'internal/config/devcontainer_features.go:mergeDevContainerFeature',
    'internal/hiss/hiss.go:DefaultMaxFuncLOC',
    '.config/archetypes/native-gpu-systems.yaml:max_func_loc',
    '.config/archetypes/facets/security-high.yaml:gitleaks',
    'cmd/standardsctl/audit.go:auditManifestAndLockfile',
    'internal/adopt/policy_dryrun.go:planPolicyCatalog',
    'cmd/standardsctl/devcontainer.go:runDevContainer',
    'cmd/standardsctl/workstation.go:loadInstallSettings',
  ],
  props: {
    speed: 1100,
    layout: {
      direction: 'row',
      gap: 56,
      children: [
        {
          id: 'layers',
          label: 'Layers, folded in this order',
          direction: 'column',
          gap: 16,
          children: [
            { id: 'defaults', label: '1. Defaults', sub: 'builtin:defaults-v1', width: 270 },
            { id: 'profiles', label: '2. Pinned profiles', sub: '.config/archetypes, lock digest', width: 270 },
            { id: 'facets', label: '3. Pinned facets', sub: '.config/archetypes/facets, lock digest', width: 270 },
            { id: 'external', label: '4. External', sub: 'fleet, org, deployment, workstation', width: 270 },
            { id: 'repo', label: '5. Repository complexity', sub: '.standards.yaml overrides.complexity', width: 270 },
            { id: 'compat', label: '6. Audit compat', sub: 'builtin:audit-compat-v1, if Audit set', width: 270 },
          ],
        },
        { id: 'join', label: 'Lattice join', sub: 'ResolvePolicy', shape: 'decision' },
        {
          direction: 'column',
          gap: 56,
          children: [
            { id: 'overrides', label: 'Control overrides', sub: 'branch protection & supply chain', width: 290 },
            { id: 'resolved', label: 'Effective policy', sub: 'EffectivePolicy, sealed', shape: 'store', width: 290 },
            { id: 'consumers', label: 'Consumers', sub: 'audit, adopt plan, devcontainer, workstation', width: 290 },
          ],
        },
      ],
    },
    edges: [
      { from: 'layers', to: 'join' },
      { from: 'join', to: 'resolved' },
      { from: 'overrides', to: 'resolved', label: 'ApplyOverrides' },
      { from: 'resolved', to: 'consumers' },
    ],
    steps: [
      {
        label: 'defaults',
        caption: 'The join starts from the built-in defaults.',
        flow: [
          {
            light: ['defaults'],
            say: 'newEffectivePolicy seeds the result with DefaultPolicy.',
            show: {
              defaults: [
                { tag: 'complexity', tone: 'gray', text: '15 / 20 / 60 / 75', meta: 'cyc / cog / loc / stmt' },
                { tag: 'supply_chain', tone: 'gray', text: 'slsa_level: 1' },
                { tag: 'branch', tone: 'gray', text: 'review_mode: independent' },
              ],
            },
          },
        ],
      },
      {
        label: 'pinned profiles',
        caption: 'A profile joins only through its .standards.lock pin.',
        flow: [
          {
            light: ['profiles'],
            say: 'pinnedLayer rejects a file whose digest differs from its lock pin (ErrLockDigestMismatch) or whose id changed.',
            show: {
              profiles: [
                { tag: 'pin', tone: 'green', text: 'sha256 equals the lock digest' },
                { tag: 'pin', tone: 'green', text: 'id: native-gpu-systems' },
              ],
            },
          },
          {
            light: ['profiles'],
            say: 'native-gpu-systems then contributes its complexity caps and controls.',
            show: {
              profiles: [
                { tag: 'complexity', tone: 'blue', text: '10 / 12 / 75 / 40', meta: 'cyc / cog / loc / stmt' },
                { tag: 'supply_chain', tone: 'blue', text: 'slsa_level: 3' },
                { tag: 'linters', tone: 'gray', text: 'clang-tidy, clippy, semgrep, cppcheck' },
              ],
            },
          },
        ],
      },
      {
        label: 'pinned facets',
        caption: 'Facets pass the same pin check and fold in after the profiles.',
        flow: [
          {
            light: ['facets'],
            say: 'security:high adds two required reviewers, SLSA level 3 and two linters.',
            show: {
              facets: [
                { tag: 'branch', tone: 'blue', text: 'required_approving_reviewers: 2' },
                { tag: 'supply_chain', tone: 'blue', text: 'slsa_level: 3' },
                { tag: 'linters', tone: 'gray', text: 'gitleaks, trivy' },
              ],
            },
          },
        ],
      },
      {
        label: 'external, repository, audit',
        caption: 'These layers add complexity limits and operator settings, never controls.',
        flow: [
          {
            light: ['external'],
            say: 'Selected fleet, organization, deployment and workstation files fold in, in that order.',
            show: {
              external: [
                { tag: 'complexity', tone: 'gray', text: 'limits only' },
                { tag: 'operator', tone: 'gray', text: 'clients, hooks, update, framework, forge, topology' },
              ],
            },
          },
          {
            light: ['repo'],
            say: 'The manifest\'s overrides.complexity joins as one more layer.',
          },
          {
            light: ['compat'],
            say: 'When Audit is set, as in audit and adopt, the audit-compat ceiling folds in last.',
            show: {
              compat: [{ tag: 'complexity', tone: 'gray', text: 'max_func_loc: 60', meta: 'AuditMaxFuncLOC' }],
            },
          },
        ],
      },
      {
        label: 'join',
        caption: 'ResolvePolicy joins every layer in order.',
        flow: [
          {
            edges: 'layers->join',
            say: 'Join keeps the lowest positive complexity cap, the higher reviewer count and SLSA level, true over false, strict_ban over allow_with_comment, and deduplicated unions.',
          },
          {
            edges: 'join->resolved',
            say: 'ArchetypeControls.validate rejects review_mode on every layer, so the joined policy keeps the independent default.',
            show: {
              resolved: [
                { tag: 'complexity', tone: 'blue', text: '10 / 12 / 60 / 40', meta: 'loc 60 beats 75' },
                { tag: 'branch', tone: 'blue', text: 'reviewers: 2, review_mode: independent' },
                { tag: 'supply_chain', tone: 'blue', text: 'slsa_level: 3' },
                { tag: 'linters', tone: 'gray', text: 'govet, clang-tidy, clippy, semgrep, cppcheck, gitleaks, trivy' },
              ],
            },
          },
        ],
      },
      {
        label: 'control overrides',
        caption: 'Repository branch and supply-chain overrides apply after the join.',
        flow: [
          {
            edges: 'overrides->resolved',
            say: 'finishEffectivePolicy applies only branch_protection and supply_chain through ApplyOverrides; review_mode may relax to single_maintainer here and nowhere else.',
            show: {
              overrides: [
                { tag: 'branch', tone: 'gray', text: 'tighten only' },
                { tag: 'supply_chain', tone: 'gray', text: 'tighten only' },
                { tag: 'review_mode', tone: 'orange', text: 'single_maintainer allowed here only' },
              ],
            },
          },
        ],
      },
      {
        label: 'consumers',
        caption: 'The sealed effective policy drives every consumer.',
        flow: [
          {
            edges: 'resolved->consumers',
            say: 'standardsctl audit, adopt plans, devcontainer and workstation install each resolve policy through this loader.',
            show: {
              consumers: [
                { tag: 'audit', tone: 'gray', text: 'auditManifestAndLockfile' },
                { tag: 'adopt', tone: 'gray', text: 'planPolicyCatalog' },
                { tag: 'devcontainer', tone: 'gray', text: 'runDevContainer' },
                { tag: 'workstation', tone: 'gray', text: 'loadInstallSettings' },
              ],
            },
          },
          {
            say: 'ResolveDevContainerFeatures re-reads the pinned files: one feature identity with another tag, digest or options fails closed.',
            show: {
              consumers: [
                { tag: 'features', tone: 'green', text: 'common-utils:2 twice, same ref: kept once' },
                { tag: 'features', tone: 'orange', text: 'same identity, other tag or options: error' },
              ],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
