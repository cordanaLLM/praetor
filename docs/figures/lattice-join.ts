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
    '.config/archetypes/native-gpu-systems.yaml:max_func_loc',
    'internal/config/archetype_test.go:TestResolvePolicyTiedArchetypesOnMemoryAndErrorUnwraps'
  ],
  describe: [
    'Numbers keep minimum positive bounds, booleans keep true, lists are deduplicated unions, tied values keep the first pinned.',
    'Exception: ReviewMode keeps the stricter mode (independent) when both sides are known, but passes through an unknown/invalid mode unchanged so validation catches it later.'
  ],
  props: {
    layout: {
      gap: 48,
      children: [
        {
          direction: 'row',
          gap: 40,
          children: [
            { id: 'profiles', label: 'Profiles', sub: '.config/archetypes/*.yaml' },
            { id: 'facets', label: 'Facets', sub: '.config/archetypes/facets/*.yaml' }
          ]
        },
        { id: 'join', label: 'Lattice Engine', sub: 'Join(a, b)', shape: 'decision' },
        { id: 'overrides', label: 'Project Overrides', sub: '.standards.yaml' },
        { id: 'resolved', label: 'Resolved Policy', sub: 'EffectivePolicy', shape: 'store' },
        { id: 'consumers', label: 'Consumers', sub: 'CI pipelines & Invariant Scans' }
      ]
    },
    edges: [
      { from: 'profiles', to: 'join' },
      { from: 'facets', to: 'join' },
      { from: 'join', to: 'resolved' },
      { from: 'overrides', to: 'resolved', label: 'ApplyOverrides' },
      { from: 'resolved', to: 'consumers' }
    ],
    steps: [
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
            say: 'Numbers keep the minimum positive bound. Booleans keep true. Lists are deduplicated unions. Tied pins keep the first declared. ReviewMode keeps the stricter mode (independent) when both sides are known, but passes through an unknown/invalid mode unchanged so validation catches it later.'
          }
        ]
      },
      {
        label: 'overrides relative to join',
        caption: 'Project overrides apply on top of the joined policy.',
        flow: [
          {
            edges: 'overrides->resolved',
            say: 'ApplyOverrides keeps monotonic strictness, except for ReviewMode which can be explicitly relaxed.'
          }
        ]
      },
      {
        label: 'resolved policy',
        caption: 'The resulting effective policy drives gating.',
        flow: [
          {
            edges: 'resolved->consumers',
            say: 'The unified policy is consumed by flavor audit, invariant scans, and CI pipelines.',
            show: {
              resolved: [
                { tag: 'memory', tone: 'blue', text: 'zero_frame_malloc: true' },
                { tag: 'branch', tone: 'blue', text: 'enforce_linear_history: true' },
                { tag: 'linters', tone: 'gray', text: 'clang-tidy, clippy, semgrep, cppcheck, gitleaks, trivy' }
              ]
            }
          }
        ]
      }
    ]
  }
} satisfies PraetorFigure;
