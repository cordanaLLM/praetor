import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Lattice Join',
  alt: 'The lattice engine resolves profiles and facets into an effective policy, then project overrides apply.',
  evidence: [
    'internal/config/config.go:Join',
    'internal/config/config.go:ApplyOverrides',
    'internal/config/config.go:joinReviewMode',
    'internal/config/config.go:knownBranchReviewMode',
    'internal/config/effective.go:ResolvePolicy',
    'internal/config/effective_load.go:loadEffectivePolicy',
    'internal/config/effective_load.go:finishEffectivePolicy',
    '.config/archetypes/native-gpu-systems.yaml:max_func_loc',
    'internal/config/archetype_test.go:TestResolvePolicyTiedArchetypesOnMemoryAndErrorUnwraps'
  ],
  describe: [
    'Complexity caps take the lowest positive bound. Reviewer count and SLSA level take the maximum. Booleans keep true, lists are deduplicated unions, and strict_ban wins for error_unwraps.',
    'Exception: ReviewMode keeps the stricter mode (independent) when both sides are known, but passes through an unknown/invalid mode unchanged so validation catches it later.'
  ],
  props: {
    layout: {
      direction: 'column',
      gap: 48,
      children: [
        {
          direction: 'row',
          gap: 40,
          children: [
            { id: 'defaults', label: 'Defaults', sub: 'DefaultPolicy' },
            { id: 'profiles', label: 'Profiles', sub: '.config/archetypes/*.yaml' },
            { id: 'facets', label: 'Facets', sub: '.config/archetypes/facets/*.yaml' },
            { id: 'external', label: 'External', sub: 'Fleet, Org, Workstation' },
            { id: 'repo', label: 'Repository', sub: 'Complexity Overrides' },
            { id: 'compat', label: 'Audit Compat', sub: 'builtin:audit-compat-v1' }
          ]
        },
        { id: 'join', label: 'Lattice Engine', sub: 'ResolvePolicy', shape: 'decision' },
        { id: 'overrides', label: 'Control Overrides', sub: 'Branch & Supply Chain' },
        { id: 'resolved', label: 'Resolved Policy', sub: 'EffectivePolicy', shape: 'store' },
        { id: 'consumers', label: 'Consumers', sub: 'audits, plans & workstation config' }
      ]
    },
    edges: [
      { from: 'defaults', to: 'join' },
      { from: 'profiles', to: 'join' },
      { from: 'facets', to: 'join' },
      { from: 'external', to: 'join' },
      { from: 'repo', to: 'join' },
      { from: 'compat', to: 'join' },
      { from: 'join', to: 'resolved' },
      { from: 'overrides', to: 'resolved', label: 'ApplyOverrides' },
      { from: 'resolved', to: 'consumers' }
    ],
    steps: [
      {
        label: 'default values',
        caption: 'DefaultPolicy provides the baseline.',
        flow: [
          {
            light: ['defaults'],
            say: 'DefaultPolicy sets govet, common-utils, SLSA 1, and independent ReviewMode.',
            show: {
              defaults: [
                { tag: 'linters', tone: 'gray', text: 'govet' },
                { tag: 'supply_chain', tone: 'gray', text: 'slsa_level: 1' }
              ]
            }
          }
        ]
      },
      {
        label: 'profile values',
        caption: 'A profile sets baseline governance for a stack.',
        flow: [
          {
            light: ['profiles'],
            say: 'native-gpu-systems sets memory bounds, SLSA level 3, and linters.',
            show: {
              profiles: [
                { tag: 'memory', tone: 'blue', text: 'zero_frame_malloc: true' },
                { tag: 'linters', tone: 'gray', text: 'clang-tidy, clippy, semgrep, cppcheck' },
                { tag: 'supply_chain', tone: 'gray', text: 'slsa_level: 3' }
              ]
            }
          }
        ]
      },
      {
        label: 'facet values',
        caption: 'Facets add cross-cutting modifiers.',
        flow: [
          {
            light: ['facets'],
            say: 'A security facet enforces linear history, signed commits, and security linters.',
            show: {
              facets: [
                { tag: 'branch', tone: 'blue', text: 'enforce_linear_history: true' },
                { tag: 'branch', tone: 'blue', text: 'require_signed_commits: true' },
                { tag: 'linters', tone: 'gray', text: 'gitleaks, trivy' }
              ]
            }
          }
        ]
      },
      {
        label: 'join stricter value',
        caption: 'The Join keeps the strictest value per dimension.',
        flow: [
          {
            edges: 'profiles->join',
            say: 'Profiles and facets are joined.'
          },
          {
            edges: 'facets->join',
            say: 'Complexity caps take the lowest positive bound. Reviewer count and SLSA level take the maximum. Booleans keep true. Lists are deduplicated unions. strict_ban wins for error_unwraps. ReviewMode keeps the stricter mode (independent) when both sides are known, but passes through an unknown/invalid mode unchanged so validation catches it later.'
          }
        ]
      },
      {
        label: 'overrides relative to join',
        caption: 'Project overrides apply on top of the joined policy.',
        flow: [
          {
            edges: 'overrides->resolved',
            say: 'ApplyOverrides keeps monotonic strictness for branch protection and supply chain, except for ReviewMode which can be explicitly relaxed.'
          }
        ]
      },
      {
        label: 'resolved policy',
        caption: 'The resulting effective policy drives gating.',
        flow: [
          {
            edges: 'resolved->consumers',
            say: 'The unified policy is consumed by standardsctl audits, adoption plans, devcontainers, and workstation environments.',
            show: {
              resolved: [
                { tag: 'memory', tone: 'blue', text: 'zero_frame_malloc: true' },
                { tag: 'branch', tone: 'blue', text: 'enforce_linear_history: true' },
                { tag: 'linters', tone: 'gray', text: 'govet, clang-tidy, clippy, semgrep, cppcheck, gitleaks, trivy' }
              ]
            }
          }
        ]
      }
    ]
  }
} satisfies PraetorFigure;
