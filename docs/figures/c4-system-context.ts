import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'System Context Diagram',
  alt: 'Praetor boundaries, agent interactions, git providers, and downstream infrastructure.',
  evidence: [
    'internal/forge/forge.go:NewForge',
    'internal/runner/matrix.go:ResolveRunner',
    'cmd/standardsctl/compile_context.go:runCompileContext',
    'internal/operationalsync/overlay.go:ownerOnlyPrefixes',
    'cmd/standardsctl/audit.go:auditRunnerMatrix',
    'internal/config/hierarchy.go:DefaultRunnerPolicy',
    'cmd/standardsctl/harvest.go:runHarvest',
    'cmd/standardsctl/dogfood.go:runDogfood',
    'cmd/standardsctl/needs.go:runNeeds',
    'deploy/helm/praetor/Chart.yaml:apiVersion',
  ],
  props: {
    layout: {
      direction: 'column',
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
            { id: 'k8s_arc', label: 'ARC Runner Scale Sets', sub: 'fork deploy/arc/' },
            { id: 'gitops', label: 'GitOps Controller', sub: 'fork deploy/k8s/, Helm chart' },
          ],
        },
      ],
    },
    edges: [
      { from: 'developer', to: 'praetor', label: 'invokes, verifies PRs' },
      { from: 'agent', to: 'praetor', label: 'reads AGENTS.md' },
      { from: 'fork', to: 'praetor', label: 'harvests, dogfoods' },
      { from: 'praetor', to: 'git_provider', label: 'branch protection' },
      { from: 'praetor', to: 'k8s_arc', label: 'resolves routing' },
      { from: 'gitops', to: 'praetor', label: 'syncs Helm chart' },
    ],
    steps: [],
  },
} satisfies PraetorFigure;
