import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'System Context Diagram',
  alt: 'Praetor boundaries, agent interactions, git providers, and downstream infrastructure.',
  evidence: [
    'internal/forge/forge.go:NewForge',
    'internal/runner/matrix.go:ResolveRunner',
    'cmd/standardsctl/compile_context.go:runCompileContext',
  ],
  props: {
    layout: {
      gap: 40,
      children: [
        {
          direction: 'row',
          gap: 40,
          children: [
            { id: 'developer', label: 'Software Engineer / Operator' },
            { id: 'agent', label: 'Autonomous AI Coding Agent', sub: 'fed by compile-context' },
          ],
        },
        {
          id: 'boundary',
          label: 'Praetor Governance Boundary',
          children: [
            { id: 'praetor', label: 'Praetor Engine', sub: 'praetorctl' },
          ],
        },
        {
          direction: 'row',
          gap: 20,
          children: [
            { id: 'fork', label: 'Operational Fork' },
            { id: 'git_provider', label: 'Git Provider', sub: 'GitHub enforces' },
          ],
        },
        {
          direction: 'row',
          gap: 20,
          children: [
            { id: 'k8s_arc', label: 'ARC Runner Scale Sets', sub: 'operational fork deploy/arc/' },
            { id: 'gitops', label: 'GitOps Controller', sub: 'operational fork deploy/k8s/' },
          ],
        },
      ],
    },
    edges: [
      { from: 'developer', to: 'praetor', label: 'invokes commands / verifies PRs' },
      { from: 'agent', to: 'praetor', label: 'reads AGENTS.md / runs checks' },
      { from: 'fork', to: 'praetor', label: 'harvests requirements & dogfoods' },
      { from: 'praetor', to: 'git_provider', label: 'enforces branch protection & checks' },
      { from: 'praetor', to: 'k8s_arc', label: 'resolves runner routing to scale-set names' },
      { from: 'gitops', to: 'praetor', label: 'syncs deploy/helm/praetor' },
    ],
    steps: [],
  },
} satisfies PraetorFigure;
